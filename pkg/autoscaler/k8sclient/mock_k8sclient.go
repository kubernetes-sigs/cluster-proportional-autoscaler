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

	"k8s.io/api/core/v1"
)

var _ = K8sClient(&MockK8sClient{})

// MockK8sClient implements K8sClientInterface
type MockK8sClient struct {
	NumOfNodes    int
	NumOfCores    int
	NumOfReplicas int
	ConfigMap     *v1.ConfigMap
}

// FetchConfigMap mocks fetching the requested configmap from the Apiserver
func (k *MockK8sClient) FetchConfigMap(namespace, configmap string) (*v1.ConfigMap, error) {
	if k.ConfigMap.ObjectMeta.ResourceVersion == "" {
		return nil, fmt.Errorf("config map not exist")
	}
	return k.ConfigMap, nil
}

// CreateConfigMap mocks creating a configmap with given namespace, name and params
func (k *MockK8sClient) CreateConfigMap(namespace, configmap string, params map[string]string) (*v1.ConfigMap, error) {
	return nil, nil
}

// UpdateConfigMap mocks updating a configmap with given namespace, name and params
func (k *MockK8sClient) UpdateConfigMap(namespace, configmap string, params map[string]string) (*v1.ConfigMap, error) {
	return nil, nil
}

// GetClusterStatus mocks counting schedulable nodes and cores in the cluster
func (k *MockK8sClient) GetClusterStatus() (*ClusterStatus, error) {
	return &ClusterStatus{int32(k.NumOfNodes), int32(k.NumOfNodes), int32(k.NumOfCores), int32(k.NumOfCores)}, nil
}

// GetNamespace mocks returning the namespace of target resource.
func (k *MockK8sClient) GetNamespace() string {
	return ""
}

// UpdateReplicas mocks updating the number of replicas for the resource and return the previous replicas count
func (k *MockK8sClient) UpdateReplicas(expReplicas int32) (int32, error) {
	prevReplicas := int32(k.NumOfReplicas)
	k.NumOfReplicas = int(expReplicas)
	return prevReplicas, nil
}
