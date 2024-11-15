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
	"context"
	"fmt"
	"strings"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	v1 "k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"

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
	target        *scaleTarget
	clientset     kubernetes.Interface
	clusterStatus *ClusterStatus
	nodeLister    corelisters.NodeLister
	stopCh        chan struct{}
}

func getTrimmedNodeClients(clientset kubernetes.Interface, labelOptions informers.SharedInformerOption) (informers.SharedInformerFactory, corelisters.NodeLister, error) {
	factory := informers.NewSharedInformerFactoryWithOptions(clientset, 0, labelOptions)
	nodeInformer := factory.Core().V1().Nodes().Informer()
	err := nodeInformer.SetTransform(func(obj any) (any, error) {
		// Trimming unneeded fields to reduce memory consumption under large-scale.
		if node, ok := obj.(*v1.Node); ok {
			node.ObjectMeta = metav1.ObjectMeta{
				Name: node.Name,
			}
			node.Spec = v1.NodeSpec{
				Unschedulable: node.Spec.Unschedulable,
			}
			node.Status = v1.NodeStatus{
				Allocatable: node.Status.Allocatable,
			}
		}
		return obj, nil
	})
	if err != nil {
		return nil, nil, err
	}
	nodeLister := factory.Core().V1().Nodes().Lister()
	return factory, nodeLister, nil
}

// NewK8sClient gives a k8sClient with the given dependencies.
func NewK8sClient(clientset kubernetes.Interface, namespace, target string, nodelabels string) (K8sClient, error) {
	// Start the informer to list and watch nodes.
	stopCh := make(chan struct{})
	labelOptions := informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
		opts.LabelSelector = nodelabels
	})
	factory, nodeLister, err := getTrimmedNodeClients(clientset, labelOptions)
	if err != nil {
		return nil, err
	}
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)

	scaleTarget, err := getScaleTarget(target, namespace)
	if err != nil {
		return nil, err
	}

	return &k8sClient{
		target:     scaleTarget,
		clientset:  clientset,
		nodeLister: nodeLister,
		stopCh:     stopCh,
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

func (k *k8sClient) FetchConfigMap(namespace, configmap string) (*v1.ConfigMap, error) {
	cm, err := k.clientset.CoreV1().ConfigMaps(namespace).Get(context.TODO(), configmap, metav1.GetOptions{})
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
	cm, err := k.clientset.CoreV1().ConfigMaps(namespace).Create(context.TODO(), &providedConfigMap, metav1.CreateOptions{})
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
	cm, err := k.clientset.CoreV1().ConfigMaps(namespace).Update(context.TODO(), &providedConfigMap, metav1.UpdateOptions{})
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
	nodes, err := k.nodeLister.List(labels.NewSelector())
	if err != nil {
		return nil, err
	}

	clusterStatus = &ClusterStatus{}
	clusterStatus.TotalNodes = int32(len(nodes))
	var tc resource.Quantity
	var sc resource.Quantity
	for _, node := range nodes {
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

func (k *k8sClient) UpdateReplicas(expReplicas int32) (prevReplicas int32, err error) {
	prevReplicas, err = k.updateReplicasAppsV1(expReplicas)
	if err == nil || !apierrors.IsForbidden(err) {
		return prevReplicas, err
	}
	glog.V(1).Infof("Falling back to extensions/v1beta1, error using apps/v1: %v", err)

	// Fall back to using the extensions API if we get a forbidden error
	scale, err := k.getScaleExtensionsV1beta1(k.target)
	if err != nil {
		return 0, err
	}
	prevReplicas = scale.Spec.Replicas
	if expReplicas != prevReplicas {
		glog.V(0).Infof("Cluster status: SchedulableNodes[%v], TotalNodes[%v], SchedulableCores[%v], TotalCores[%v]", k.clusterStatus.SchedulableNodes, k.clusterStatus.TotalNodes, k.clusterStatus.SchedulableCores, k.clusterStatus.TotalCores)
		glog.V(0).Infof("Replicas are not as expected : updating replicas from %d to %d", prevReplicas, expReplicas)
		scale.Spec.Replicas = expReplicas
		_, err = k.updateScaleExtensionsV1beta1(k.target, scale)
		if err != nil {
			return 0, err
		}
	}
	return prevReplicas, nil
}

func (k *k8sClient) getScaleExtensionsV1beta1(target *scaleTarget) (*extensionsv1beta1.Scale, error) {
	opt := metav1.GetOptions{}
	switch strings.ToLower(target.kind) {
	case "deployment", "deployments":
		return k.clientset.ExtensionsV1beta1().Deployments(target.namespace).GetScale(context.TODO(), target.name, opt)
	case "replicaset", "replicasets":
		return k.clientset.ExtensionsV1beta1().ReplicaSets(target.namespace).GetScale(context.TODO(), target.name, opt)
	default:
		return nil, fmt.Errorf("unsupported target kind: %v", target.kind)
	}
}

func (k *k8sClient) updateScaleExtensionsV1beta1(target *scaleTarget, scale *extensionsv1beta1.Scale) (*extensionsv1beta1.Scale, error) {
	switch strings.ToLower(target.kind) {
	case "deployment", "deployments":
		return k.clientset.ExtensionsV1beta1().Deployments(target.namespace).UpdateScale(context.TODO(), target.name, scale, metav1.UpdateOptions{})
	case "replicaset", "replicasets":
		return k.clientset.ExtensionsV1beta1().ReplicaSets(target.namespace).UpdateScale(context.TODO(), target.name, scale, metav1.UpdateOptions{})
	default:
		return nil, fmt.Errorf("unsupported target kind: %v", target.kind)
	}
}

func (k *k8sClient) updateReplicasAppsV1(expReplicas int32) (prevReplicas int32, err error) {
	req, err := requestForTarget(k.clientset.AppsV1().RESTClient().Get(), k.target)
	if err != nil {
		return 0, err
	}

	scale := &autoscalingv1.Scale{}
	if err = req.Do(context.TODO()).Into(scale); err != nil {
		return 0, err
	}

	prevReplicas = scale.Spec.Replicas
	if expReplicas != prevReplicas {
		glog.V(0).Infof("Cluster status: SchedulableNodes[%v], TotalNodes[%v], SchedulableCores[%v], TotalCores[%v]", k.clusterStatus.SchedulableNodes, k.clusterStatus.TotalNodes, k.clusterStatus.SchedulableCores, k.clusterStatus.TotalCores)
		glog.V(0).Infof("Replicas are not as expected : updating replicas from %d to %d", prevReplicas, expReplicas)
		scale.Spec.Replicas = expReplicas
		req, err = requestForTarget(k.clientset.AppsV1().RESTClient().Put(), k.target)
		if err != nil {
			return 0, err
		}
		if err = req.Body(scale).Do(context.TODO()).Error(); err != nil {
			return 0, err
		}
	}

	return prevReplicas, nil
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
