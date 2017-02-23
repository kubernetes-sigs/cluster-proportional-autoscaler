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

	"k8s.io/client-go/1.4/kubernetes"
	"k8s.io/client-go/1.4/pkg/api"
	"k8s.io/client-go/1.4/pkg/api/resource"
	apiv1 "k8s.io/client-go/1.4/pkg/api/v1"
	"k8s.io/client-go/1.4/rest"

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

	return &k8sClient{
		clientset: clientset,
		target:    scaleTarget,
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
	cm, err := k.clientset.CoreClient.ConfigMaps(namespace).Get(configmap)
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
	cm, err := k.clientset.CoreClient.ConfigMaps(namespace).Create(&providedConfigMap)
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
	cm, err := k.clientset.CoreClient.ConfigMaps(namespace).Update(&providedConfigMap)
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
	opt := api.ListOptions{Watch: false}

	nodes, err := k.clientset.CoreClient.Nodes().List(opt)
	if err != nil || nodes == nil {
		return nil, err
	}
	clusterStatus = &ClusterStatus{}
	clusterStatus.TotalNodes = int32(len(nodes.Items))
	var tc resource.Quantity
	var sc resource.Quantity
	for _, node := range nodes.Items {
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
