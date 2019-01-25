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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	apiv1 "k8s.io/client-go/pkg/api/v1"
	autoscalingv1 "k8s.io/client-go/pkg/apis/autoscaling/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"github.com/golang/glog"
)

// K8sClient - Wraps all needed client functionalities for autoscaler
type K8sClient interface {
	// FetchConfigMap fetches the requested configmap from the Apiserver
	FetchConfigMap(namespace, configmap string) (*apiv1.ConfigMap, error)
	// CreateConfigMap creates a configmap with given namespace, name and params
	CreateConfigMap(namespace, configmap string, params map[string]string) (*apiv1.ConfigMap, error)
	// UpdateConfigMap updates a configmap with given namespace, name and params
	UpdateConfigMap(namespace, configmap string, params map[string]string) (*apiv1.ConfigMap, error)
	// GetClusterStatus counts schedulable nodes and cores in the cluster
	GetClusterStatus() (clusterStatus *ClusterStatus, err error)
	// GetNamespace returns the namespace of target resource.
	GetNamespace() (namespace string)
	// UpdateReplicas updates the number of replicas for the resource and return the previous replicas count
	UpdateReplicas(expReplicas int32) (prevReplicas int32, err error)
}

// k8sClient - Wraps all Kubernetes API client functionalities
type k8sClient struct {
	target        *scaleTarget
	clientset     *kubernetes.Clientset
	clusterStatus *ClusterStatus
	nodeStore     cache.Store
	reflector     *cache.Reflector
}

// NewK8sClient gives a k8sClient with the given dependencies.
func NewK8sClient(namespace, target string) (K8sClient, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	// Use protobufs for communication with apiserver.
	config.ContentType = "application/vnd.kubernetes.protobuf"
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	scaleTarget, err := getScaleTarget(target, namespace)
	if err != nil {
		return nil, err
	}

	// Start propagating contents of the nodeStore.
	nodeListWatch := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return clientset.Core().Nodes().List(options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return clientset.Core().Nodes().Watch(options)
		},
	}
	nodeStore := cache.NewStore(cache.MetaNamespaceKeyFunc)
	reflector := cache.NewReflector(nodeListWatch, &apiv1.Node{}, nodeStore, 0)
	reflector.Run()

	return &k8sClient{
		target:    scaleTarget,
		clientset: clientset,
		nodeStore: nodeStore,
		reflector: reflector,
	}, nil
}

func getScaleTarget(target, namespace string) (*scaleTarget, error) {
	splits := strings.Split(target, "/")
	if len(splits) != 2 {
		return &scaleTarget{}, fmt.Errorf("target format error: %v", target)
	}
	kind := splits[0]
	name := splits[1]
	return &scaleTarget{kind, name, namespace}, nil
}

// scaleTarget stores the scalable target recourse
type scaleTarget struct {
	kind      string
	name      string
	namespace string
}

func (k *k8sClient) GetNamespace() (namespace string) {
	return k.target.namespace
}

func (k *k8sClient) FetchConfigMap(namespace, configmap string) (*apiv1.ConfigMap, error) {
	cm, err := k.clientset.CoreV1().ConfigMaps(namespace).Get(configmap, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return cm, nil
}

func (k *k8sClient) CreateConfigMap(namespace, configmap string, params map[string]string) (*apiv1.ConfigMap, error) {
	providedConfigMap := apiv1.ConfigMap{}
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

func (k *k8sClient) UpdateConfigMap(namespace, configmap string, params map[string]string) (*apiv1.ConfigMap, error) {
	providedConfigMap := apiv1.ConfigMap{}
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
	// TODO: Consider moving this to NewK8sClient method and failing fast when
	// reflector can't initialize. That is a tradeoff between silently non-working
	// component and explicit restarts of it. In majority of the cases the restart
	// won't repair it - though it may give better visibility into problems.
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
		node, ok := nodes[i].(*apiv1.Node)
		if !ok {
			glog.Errorf("Unexpected object: %#v", nodes[i])
			continue
		}
		tc.Add(node.Status.Capacity[apiv1.ResourceCPU])
		if !node.Spec.Unschedulable {
			clusterStatus.SchedulableNodes++
			sc.Add(node.Status.Capacity[apiv1.ResourceCPU])
		}
	}

	tcInt64, tcOk := tc.AsInt64()
	scInt64, scOk := sc.AsInt64()
	if !tcOk || !scOk {
		return nil, fmt.Errorf("unable to compute integer values of schedulable cores in the cluster")
	}
	clusterStatus.TotalCores = int32(tcInt64)
	clusterStatus.SchedulableCores = int32(scInt64)
	k.clusterStatus = clusterStatus
	return clusterStatus, nil
}

func (k *k8sClient) UpdateReplicas(expReplicas int32) (prevRelicas int32, err error) {
	prevRelicas, err = k.updateReplicasAppsV1(expReplicas)
	if err == nil || !apierrors.IsForbidden(err) {
		return prevRelicas, err
	}
	glog.V(1).Infof("Falling back to extensions/v1beta1, error using apps/v1: %v", err)

	// Fall back to using the extensions API if we get a forbidden error
	scale, err := k.clientset.Extensions().Scales(k.target.namespace).Get(k.target.kind, k.target.name)
	if err != nil {
		return 0, err
	}
	prevRelicas = scale.Spec.Replicas
	if expReplicas != prevRelicas {
		glog.V(0).Infof("Cluster status: SchedulableNodes[%v], SchedulableCores[%v]", k.clusterStatus.SchedulableNodes, k.clusterStatus.SchedulableCores)
		glog.V(0).Infof("Replicas are not as expected : updating replicas from %d to %d", prevRelicas, expReplicas)
		scale.Spec.Replicas = expReplicas
		_, err = k.clientset.Extensions().Scales(k.target.namespace).Update(k.target.kind, scale)
		if err != nil {
			return 0, err
		}
	}
	return prevRelicas, nil
}

func (k *k8sClient) updateReplicasAppsV1(expReplicas int32) (prevRelicas int32, err error) {
	req, err := requestForTarget(k.clientset.Apps().RESTClient().Get(), k.target)
	if err != nil {
		return 0, err
	}

	scale := &autoscalingv1.Scale{}
	if err = req.Do().Into(scale); err != nil {
		return 0, err
	}

	prevRelicas = scale.Spec.Replicas
	if expReplicas != prevRelicas {
		glog.V(0).Infof("Cluster status: SchedulableNodes[%v], SchedulableCores[%v]", k.clusterStatus.SchedulableNodes, k.clusterStatus.SchedulableCores)
		glog.V(0).Infof("Replicas are not as expected : updating replicas from %d to %d", prevRelicas, expReplicas)
		scale.Spec.Replicas = expReplicas
		req, err = requestForTarget(k.clientset.Apps().RESTClient().Put(), k.target)
		if err != nil {
			return 0, err
		}
		if err = req.Body(scale).Do().Error(); err != nil {
			return 0, err
		}
	}

	return prevRelicas, nil
}

func requestForTarget(req *rest.Request, target *scaleTarget) (*rest.Request, error) {
	var absPath, resource string
	// Support the kinds we allowed scaling via the extensions API group
	// TODO: switch to use the polymorphic scale client once client-go versions are updated
	switch strings.ToLower(target.kind) {
	case "deployment", "deployments":
		absPath = "/apis/apps/v1"
		resource = "deployments"
	case "replicaset", "replicasets":
		absPath = "/apis/apps/v1"
		resource = "replicasets"
	case "statefulset", "statefulsets":
		absPath = "/apis/apps/v1"
		resource = "statefulsets"
	case "replicationcontroller", "replicationcontrollers":
		absPath = "/api/v1"
		resource = "replicationcontrollers"
	default:
		return nil, fmt.Errorf("unsupported target kind: %v", target.kind)
	}

	return req.AbsPath(absPath).Namespace(target.namespace).Resource(resource).Name(target.name).SubResource("scale"), nil
}
