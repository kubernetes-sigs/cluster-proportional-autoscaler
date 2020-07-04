/*
Copyright 2016 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package k8sclient

import (
	"fmt"
	"strings"
	"time"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	cacheddiscovery "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/scale"
	"k8s.io/client-go/tools/cache"

	"github.com/golang/glog"
)

// K8sClient - Wraps all needed client functionalities for autoscaler
type K8sClient interface {
	// FetchConfigMap fetches the requested configmap from the Apiserver
	FetchConfigMap(namespace, configmap string) (*v1.ConfigMap, error)
	// CreateConfigMap creates a configmap with given namespace, name and params
	CreateConfigMap(namespace, configmap string, params map[string]string) (*v1.ConfigMap, error)
	// UpdateConfigMap updates a configmap with given namespace, name and params
	UpdateConfigMap(namespace, configmap string, params map[string]string) (*v1.ConfigMap, error)
	// GetClusterStatus counts schedulable nodes and cores in the cluster
	GetClusterStatus() (clusterStatus *ClusterStatus, err error)
	// GetNamespace returns the namespace of target resource.
	GetNamespace() (namespace string)
	// UpdateReplicas updates the number of replicas for the resource and return the previous replicas count
	UpdateReplicas(expReplicas int32) (prevReplicas int32, err error)
}

// k8sClient - Wraps all Kubernetes API client functionalities
type k8sClient struct {
	target          *scaleTarget
	clientset       *kubernetes.Clientset
	clusterStatus   *ClusterStatus
	nodeStore       cache.Store
	reflector       *cache.Reflector
	scaleNamespacer scale.ScalesGetter
	stopCh          chan struct{}
}

// NewK8sClient gives a k8sClient with the given dependencies.
func NewK8sClient(namespace, target string, nodelabels string) (K8sClient, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	// Use protobufs for communication with apiserver.
	config.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
	config.ContentType = "application/vnd.kubernetes.protobuf"
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	scaleTarget, err := getScaleTarget(target, namespace)
	if err != nil {
		return nil, err
	}

	restClient := clientset.RESTClient()
	discoveryClient := discovery.NewDiscoveryClient(restClient)

	// Informers don't seem to do a good job logging error messages when it
	// can't reach the server, making debugging hard. This makes it easier to
	// figure out if apiserver is configured incorrectly.
	glog.Infof("Testing communication with server")
	v, err := discoveryClient.ServerVersion()
	if err != nil {
		glog.Errorf("error while trying to communicate with apiserver: %s", err.Error())
		return nil, err
	}
	glog.Infof("Running with Kubernetes cluster version: v%s.%s. git version: %s. git tree state: %s. commit: %s. platform: %s",
		v.Major, v.Minor, v.GitVersion, v.GitTreeState, v.GitCommit, v.Platform)
	glog.Infof("Communication with server successful")

	resolver := scale.NewDiscoveryScaleKindResolver(discoveryClient)
	cachedDiscoveryClient := cacheddiscovery.NewMemCacheClient(discoveryClient)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscoveryClient)
	scaleNamespacer := scale.New(restClient, mapper, dynamic.LegacyAPIPathResolverFunc, resolver)

	// Start propagating contents of the nodeStore.
	opts := metav1.ListOptions{LabelSelector: nodelabels}
	nodeListWatch := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return clientset.CoreV1().Nodes().List(opts)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return clientset.CoreV1().Nodes().Watch(opts)
		},
	}
	nodeStore := cache.NewStore(cache.MetaNamespaceKeyFunc)
	reflector := cache.NewReflector(nodeListWatch, &v1.Node{}, nodeStore, 0)
	stopCh := make(chan struct{})
	go reflector.Run(stopCh)

	return &k8sClient{
		target:          scaleTarget,
		clientset:       clientset,
		nodeStore:       nodeStore,
		reflector:       reflector,
		scaleNamespacer: scaleNamespacer,
		stopCh:          stopCh,
	}, nil
}

func getScaleTarget(target, namespace string) (*scaleTarget, error) {

	splits := strings.Split(target, "/")
	if len(splits) != 2 {
		return &scaleTarget{}, fmt.Errorf("target format error: %v", target)
	}
	resourceArg := splits[0]
	name := splits[1]

	// check for apps resources without group set
	switch resourceArg {
	case "replicaset", "replicasets":
		resourceArg = "replicasets.apps"
	case "deployment", "deployments":
		resourceArg = "deployments.apps"
	case "statefulset", "statefulsets":
		resourceArg = "statefulsets.apps"
	case "replicationcontroller", "replicationcontrollers":
		resourceArg = "replicationcontrollers"
	}

	gvr, groupResource := schema.ParseResourceArg(resourceArg)
	if gvr != nil {
		groupResource = gvr.GroupResource()
	}
	return &scaleTarget{&groupResource, name, namespace}, nil
}

// scaleTarget stores the scalable target recourse
type scaleTarget struct {
	groupResource *schema.GroupResource
	name          string
	namespace     string
}

func (k *k8sClient) GetNamespace() (namespace string) {
	return k.target.namespace
}

func (k *k8sClient) FetchConfigMap(namespace, configmap string) (*v1.ConfigMap, error) {
	cm, err := k.clientset.CoreV1().ConfigMaps(namespace).Get(configmap, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return cm, nil
}

func (k *k8sClient) CreateConfigMap(namespace, configmap string, params map[string]string) (*v1.ConfigMap, error) {
	providedConfigMap := v1.ConfigMap{}
	providedConfigMap.ObjectMeta.Name = configmap
	providedConfigMap.ObjectMeta.Namespace = namespace
	providedConfigMap.Data = params
	cm, err := k.clientset.CoreV1().ConfigMaps(namespace).Create(&providedConfigMap)
	if err != nil {
		return nil, err
	}
	glog.V(0).Infof("Created ConfigMap %v in namespace %v", configmap, namespace)
	return cm, nil
}

func (k *k8sClient) UpdateConfigMap(namespace, configmap string, params map[string]string) (*v1.ConfigMap, error) {
	providedConfigMap := v1.ConfigMap{}
	providedConfigMap.ObjectMeta.Name = configmap
	providedConfigMap.ObjectMeta.Namespace = namespace
	providedConfigMap.Data = params
	cm, err := k.clientset.CoreV1().ConfigMaps(namespace).Update(&providedConfigMap)
	if err != nil {
		return nil, err
	}
	glog.V(0).Infof("Updated ConfigMap %v in namespace %v", configmap, namespace)
	return cm, nil
}

// ClusterStatus defines the cluster status
type ClusterStatus struct {
	TotalNodes       int32
	SchedulableNodes int32
	TotalCores       int32
	SchedulableCores int32
}

func (k *k8sClient) GetClusterStatus() (clusterStatus *ClusterStatus, err error) {
	err = wait.PollImmediate(250*time.Millisecond, 5*time.Second, func() (bool, error) {
		if k.reflector.LastSyncResourceVersion() == "" {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	nodes := k.nodeStore.List()

	clusterStatus = &ClusterStatus{}
	clusterStatus.TotalNodes = int32(len(nodes))
	var tc resource.Quantity
	var sc resource.Quantity
	for i := range nodes {
		node, ok := nodes[i].(*v1.Node)
		if !ok {
			glog.Errorf("Unexpected object: %#v", nodes[i])
			continue
		}
		tc.Add(node.Status.Allocatable[v1.ResourceCPU])
		if !node.Spec.Unschedulable {
			clusterStatus.SchedulableNodes++
			sc.Add(node.Status.Allocatable[v1.ResourceCPU])
		}
	}

	clusterStatus.TotalCores = int32(tc.Value())
	clusterStatus.SchedulableCores = int32(sc.Value())
	k.clusterStatus = clusterStatus
	return clusterStatus, nil
}

func (k *k8sClient) UpdateReplicas(expReplicas int32) (int32, error) {
	prevScale, err := k.scaleNamespacer.Scales(k.target.namespace).Get(*k.target.groupResource, k.target.name)
	if err != nil {
		return 0, err
	}
	prevReplicas := prevScale.Spec.Replicas
	if expReplicas != prevReplicas {
		glog.V(0).Infof("Cluster status: SchedulableNodes[%v], SchedulableCores[%v]", k.clusterStatus.SchedulableNodes, k.clusterStatus.SchedulableCores)
		glog.V(0).Infof("Replicas are not as expected : updating replicas from %d to %d", prevReplicas, expReplicas)

		scaleResource := &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{
				Name:      k.target.name,
				Namespace: k.target.namespace,
			},
			Spec: autoscalingv1.ScaleSpec{
				Replicas: expReplicas,
			},
		}
		_, err = k.scaleNamespacer.Scales(k.target.namespace).Update(*k.target.groupResource, scaleResource)
		if err != nil {
			return prevReplicas, err
		}
	}
	return prevReplicas, nil
}
