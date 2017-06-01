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

package nanny

import (
	"fmt"
	"log"

	api "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/resource"
	apiv1 "k8s.io/kubernetes/pkg/api/v1"
	client "k8s.io/kubernetes/pkg/client/clientset_generated/release_1_3"
	"k8s.io/kubernetes/pkg/client/restclient"
)

// KubernetesClient - Wraps all Kubernetes API client functionality
type KubernetesClient struct {
	namespace  string
	rc         string
	rs         string
	deployment string
	clientset  *client.Clientset
}

// FetchConfigMap - fetch the requested key from the configmap
func (k *KubernetesClient) FetchConfigMap(namespace, configmap, key string) (data string, version string, err error) {
	cm, err := k.clientset.CoreClient.ConfigMaps(k.namespace).Get(configmap)
	if err != nil {
		return "", "", err
	}
	return cm.Data[key], cm.ObjectMeta.ResourceVersion, nil
}

// CountNodes Count schedulable nodes and cores in our cluster
func (k *KubernetesClient) CountNodes() (totalNodes, schedulableNodes, totalCores, schedulableCores int32, err error) {
	opt := api.ListOptions{Watch: false}

	nodes, err := k.clientset.CoreClient.Nodes().List(opt)
	if err != nil {
		log.Println(err)
		return 0, 0, 0, 0, err
	}
	totalNodes = int32(len(nodes.Items))
	var tc resource.Quantity
	var sc resource.Quantity
	for _, node := range nodes.Items {
		tc.Add(node.Status.Capacity[apiv1.ResourceCPU])
		if !node.Spec.Unschedulable {
			schedulableNodes++
			sc.Add(node.Status.Capacity[apiv1.ResourceCPU])
		}
	}

	tcInt64, tcOk := tc.AsInt64()
	scInt64, scOk := sc.AsInt64()
	if !tcOk || !scOk {
		log.Println("Unable to compute integer values of schedulable cores in the cluster")
		return 0, 0, 0, 0, fmt.Errorf("Unable to compute number of cores in cluster")
	}
	return totalNodes, schedulableNodes, int32(tcInt64), int32(scInt64), nil
}

// PodReplicas Get number of replicas configured in the parent RC/Deployment/RS, in that order
func (k *KubernetesClient) PodReplicas() (int32, error) {
	if len(k.rc) > 0 {
		rc, err := k.clientset.CoreClient.ReplicationControllers(k.namespace).Get(k.rc)
		if err != nil {
			log.Printf("Failed to fetch RC %s/%s (%s)\n", k.namespace, k.rc, err)
			return 0, err
		}
		return int32(*rc.Spec.Replicas), nil
	} else if len(k.deployment) > 0 {
		deployment, err := k.clientset.ExtensionsClient.Deployments(k.namespace).Get(k.deployment)
		if err != nil {
			return 0, err
		}
		return int32(*deployment.Spec.Replicas), nil
	} else if len(k.rs) > 0 {
		rs, err := k.clientset.ExtensionsClient.ReplicaSets(k.namespace).Get(k.rs)
		if err != nil {
			return 0, err
		}
		return int32(*rs.Spec.Replicas), nil
	}
	return 0, fmt.Errorf("Invalid RC/RS/Deployment configured")
}

// UpdateReplicas Update the number of replicas in the controller
func (k *KubernetesClient) UpdateReplicas(replicas int32) error {
	if replicas == 0 {
		log.Fatalf("Cannot update to 0 replicas")
	}
	if len(k.rc) > 0 {
		rc, err := k.clientset.CoreClient.ReplicationControllers(k.namespace).Get(k.rc)
		if err != nil {
			log.Printf("Failed to fetch RC %s/%s (%s)\n", k.namespace, k.rc, err)
			return err
		}
		*rc.Spec.Replicas = replicas
		_, err = k.clientset.CoreClient.ReplicationControllers(k.namespace).Update(rc)
		if err != nil {
			log.Printf("Failed to update RC %s/%s (%s)\n", k.namespace, k.rc, err)
			return err
		}
	} else if len(k.deployment) > 0 {
		deployment, err := k.clientset.ExtensionsClient.Deployments(k.namespace).Get(k.deployment)
		if err != nil {
			return err
		}
		*deployment.Spec.Replicas = replicas
		_, err = k.clientset.ExtensionsClient.Deployments(k.namespace).Update(deployment)
		if err != nil {
			return err
		}
	} else {
		rs, err := k.clientset.ExtensionsClient.ReplicaSets(k.namespace).Get(k.rs)
		if err != nil {
			return err
		}
		*rs.Spec.Replicas = replicas
		_, err = k.clientset.ExtensionsClient.ReplicaSets(k.namespace).Update(rs)
		if err != nil {
			return err
		}
	}

	return nil
}

// NewKubernetesClient gives a KubernetesClient with the given dependencies.
func NewKubernetesClient(namespace, rc, rs, deployment string) *KubernetesClient {
	config, err := restclient.InClusterConfig()
	if err != nil {
		log.Fatal(err)
	}
	clientset, err := client.NewForConfig(config)
	if err != nil {
		log.Fatal(err)
	}
	return &KubernetesClient{
		namespace:  namespace,
		clientset:  clientset,
		rc:         rc,
		rs:         rs,
		deployment: deployment,
	}
}
