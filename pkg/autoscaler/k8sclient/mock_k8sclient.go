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
)

var _ = Interface(&MockK8sClient{})

// MockK8sClient implements K8sClientInterface
type MockK8sClient struct {
	NumOfNodes    int
	NumOfCores    int
	ConfigMap     ConfigMap
	NumOfReplicas int
}

func (k *MockK8sClient) FetchConfigMap(namespace, configmap string) (ConfigMap, error) {
	if k.ConfigMap.Version == "" {
		return ConfigMap{}, fmt.Errorf("config map not exist")
	}
	return k.ConfigMap, nil
}

func (k *MockK8sClient) GetClusterStatus() (ClusterStatus, error) {
	return ClusterStatus{int32(k.NumOfNodes), int32(k.NumOfNodes), int32(k.NumOfCores), int32(k.NumOfCores)}, nil
}

func (k *MockK8sClient) GetNamespace() string {
	return ""
}

func (k *MockK8sClient) UpdateReplicas(expReplicas int32) (int32, error) {
	prevReplicas := int32(k.NumOfReplicas)
	k.NumOfReplicas = int(expReplicas)
	return prevReplicas, nil
}
