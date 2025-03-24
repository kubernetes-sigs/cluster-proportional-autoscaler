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
	UpdateReplicas(expReplicas int32) (err error)
}

// k8sClient - Wraps all Kubernetes API client functionalities
type k8sClient struct {
	scaleTargets  *scaleTargets
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
				Conditions:  node.Status.Conditions,
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

	scaleTargets, err := getScaleTargets(target, namespace)
	if err != nil {
		return nil, err
	}

	return &k8sClient{
		scaleTargets: scaleTargets,
		clientset:    clientset,
		nodeLister:   nodeLister,
		stopCh:       stopCh,
	}, nil
}

func getScaleTargets(targets, namespace string) (*scaleTargets, error) {
	st := &scaleTargets{targets: []target{}, namespace: namespace}

	for _, el := range strings.Split(targets, ",") {
		el := strings.TrimSpace(el)
		target, err := getTarget(el)
		if err != nil {
			return &scaleTargets{}, fmt.Errorf("target format error: %v", targets)
		}
		st.targets = append(st.targets, target)
	}
	return st, nil
}

func getTarget(t string) (target, error) {
	splits := strings.Split(t, "/")
	if len(splits) != 2 {
		return target{}, fmt.Errorf("target format error: %v", t)
	}
	kind := splits[0]
	name := splits[1]
	return target{kind, name}, nil
}

type target struct {
	kind string
	name string
}

// scaleTargets stores the scalable target resources
type scaleTargets struct {
	targets   []target
	namespace string
}

func (k *k8sClient) GetNamespace() (namespace string) {
	return k.scaleTargets.namespace
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

// isNodeReady checks if a node is in the "Ready" state.
func isNodeReady(node *v1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == v1.NodeReady && condition.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
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
		if !node.Spec.Unschedulable && isNodeReady(node) {
			clusterStatus.SchedulableNodes++
			sc.Add(node.Status.Allocatable[v1.ResourceCPU])
		}
	}

	clusterStatus.TotalCores = int32(tc.Value())
	clusterStatus.SchedulableCores = int32(sc.Value())
	k.clusterStatus = clusterStatus
	return clusterStatus, nil
}

func (k *k8sClient) UpdateReplicas(expReplicas int32) (err error) {
	for _, target := range k.scaleTargets.targets {
		_, err := k.UpdateTargetReplicas(expReplicas, target)
		if err != nil {
			return err
		}
	}
	return nil
}

func (k *k8sClient) UpdateTargetReplicas(expReplicas int32, target target) (prevReplicas int32, err error) {
	prevReplicas, err = k.updateReplicasAppsV1(expReplicas, target)
	if err == nil || !apierrors.IsForbidden(err) {
		return prevReplicas, err
	}
	glog.V(1).Infof("Falling back to extensions/v1beta1, error using apps/v1: %v", err)

	// Fall back to using the extensions API if we get a forbidden error
	scale, err := k.getScaleExtensionsV1beta1(&target)
	if err != nil {
		return 0, err
	}
	prevReplicas = scale.Spec.Replicas
	if expReplicas != prevReplicas {
		glog.V(0).Infof(
			"Cluster status: SchedulableNodes[%v], TotalNodes[%v], SchedulableCores[%v], TotalCores[%v]",
			k.clusterStatus.SchedulableNodes,
			k.clusterStatus.TotalNodes,
			k.clusterStatus.SchedulableCores,
			k.clusterStatus.TotalCores)
		glog.V(0).Infof("Replicas are not as expected : updating %s/%s from %d to %d",
			target.kind,
			target.name,
			prevReplicas,
			expReplicas)
		scale.Spec.Replicas = expReplicas
		_, err = k.updateScaleExtensionsV1beta1(&target, scale)
		if err != nil {
			return 0, err
		}
	}
	return prevReplicas, nil
}

func (k *k8sClient) getScaleExtensionsV1beta1(target *target) (*extensionsv1beta1.Scale, error) {
	opt := metav1.GetOptions{}
	switch strings.ToLower(target.kind) {
	case "deployment", "deployments":
		return k.clientset.ExtensionsV1beta1().Deployments(k.scaleTargets.namespace).GetScale(context.TODO(), target.name, opt)
	case "replicaset", "replicasets":
		return k.clientset.ExtensionsV1beta1().ReplicaSets(k.scaleTargets.namespace).GetScale(context.TODO(), target.name, opt)
	default:
		return nil, fmt.Errorf("unsupported target kind: %v", target.kind)
	}
}

func (k *k8sClient) updateScaleExtensionsV1beta1(target *target, scale *extensionsv1beta1.Scale) (*extensionsv1beta1.Scale, error) {
	switch strings.ToLower(target.kind) {
	case "deployment", "deployments":
		return k.clientset.ExtensionsV1beta1().Deployments(k.scaleTargets.namespace).UpdateScale(context.TODO(), target.name, scale, metav1.UpdateOptions{})
	case "replicaset", "replicasets":
		return k.clientset.ExtensionsV1beta1().ReplicaSets(k.scaleTargets.namespace).UpdateScale(context.TODO(), target.name, scale, metav1.UpdateOptions{})
	default:
		return nil, fmt.Errorf("unsupported target kind: %v", target.kind)
	}
}

func (k *k8sClient) updateReplicasAppsV1(expReplicas int32, target target) (prevReplicas int32, err error) {
	req, err := requestForTarget(k.clientset.AppsV1().RESTClient().Get(), &target, k.scaleTargets.namespace)
	if err != nil {
		return 0, err
	}

	scale := &autoscalingv1.Scale{}
	if err = req.Do(context.TODO()).Into(scale); err != nil {
		return 0, err
	}

	prevReplicas = scale.Spec.Replicas
	if expReplicas != prevReplicas {
		glog.V(0).Infof(
			"Cluster status: SchedulableNodes[%v], TotalNodes[%v], SchedulableCores[%v], TotalCores[%v]",
			k.clusterStatus.SchedulableNodes,
			k.clusterStatus.TotalNodes,
			k.clusterStatus.SchedulableCores,
			k.clusterStatus.TotalCores)
		glog.V(0).Infof("Replicas are not as expected : updating %s/%s from %d to %d",
			target.kind,
			target.name,
			prevReplicas,
			expReplicas)
		scale.Spec.Replicas = expReplicas
		req, err = requestForTarget(k.clientset.AppsV1().RESTClient().Put(), &target, k.scaleTargets.namespace)
		if err != nil {
			return 0, err
		}
		if err = req.Body(scale).Do(context.TODO()).Error(); err != nil {
			return 0, err
		}
	}

	return prevReplicas, nil
}

func requestForTarget(req *rest.Request, target *target, namespace string) (*rest.Request, error) {
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

	return req.AbsPath(absPath).Namespace(namespace).Resource(resource).Name(target.name).SubResource("scale"), nil
}
