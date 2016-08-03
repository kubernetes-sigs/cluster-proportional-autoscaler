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

/*
Package nanny implements logic to poll the k8s apiserver for cluster status,
and update a deployment based on that status.
*/
package nanny

import (
	"log"
	"time"
)

// PollAPIServer periodically counts the number of nodes, estimates the expected
// number of replicas, compares them to the actual replicas, and
// updates the parent ReplicationController with the expected replicas if necessary.
func PollAPIServer(k8s *KubernetesClient, scaler *Scaler, pollPeriod time.Duration, configMap string, verbose bool) {
	for i := 0; true; i++ {
		if i != 0 {
			// Sleep for the poll period.
			time.Sleep(pollPeriod)
		}

		// Query the apiserver for the number of nodes and cores
		total, schedulableNodes, totalCores, schedulableCores, err := k8s.CountNodes()
		if err != nil {
			continue
		}
		if verbose {
			log.Printf("Total nodes %5d, schedulable nodes: %5d\n", total, schedulableNodes)
			log.Printf("Total cores %5d, schedulable cores: %5d\n", totalCores, schedulableCores)
		}

		// Query the apiserver for this pod's information.
		replicas, err := k8s.PodReplicas()
		if err != nil {
			log.Printf("Error while querying apiserver for pod replicas: %v\n", err)
			continue
		}
		if verbose {
			log.Printf("Current replica count: %3d\n", replicas)
		}

		params, version, err := fetchAndParseScalerParams(k8s, configMap)
		if err != nil {
			log.Printf("Error fetching/parsing scaler params: %s", err)
			continue
		}
		// Get the expected replicas for the currently schedulable nodes and cores
		expReplicas := int32(scaler.scaleWithNodesAndCores(params, version, int(schedulableNodes), int(schedulableCores)))
		if verbose {
			log.Printf("Expected replica count: %3d\n", expReplicas)
		}

		if expReplicas < 1 {
			log.Fatalf("Cannot scale to replica count of %d\n", expReplicas)
		}

		if replicas == expReplicas {
			continue
		}
		// If there's a difference, go ahead and set the new values.
		log.Printf("Replicas are not as expected : updating controller to %d replicas\n", expReplicas)
		if err := k8s.UpdateReplicas(expReplicas); err != nil {
			log.Printf("Update failure: %s\n", err)
		}
	}
}
