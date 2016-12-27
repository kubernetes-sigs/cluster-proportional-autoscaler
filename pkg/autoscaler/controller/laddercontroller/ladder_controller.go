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

package laddercontroller

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/controller"
	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/k8sclient"

	"github.com/golang/glog"
)

var _ = controller.Controller(&LadderController{})

const (
	ControllerType = "ladder"
)

type LadderController struct {
	params  *ladderParams
	version string
}

func NewLadderController() controller.Controller {
	return &LadderController{}
}

type paramEntry [2]int

type paramEntries []paramEntry

func (entries paramEntries) Len() int {
	return len(entries)
}

func (entries paramEntries) Less(i, j int) bool {
	return entries[i][0] < entries[j][0]
}

func (entries paramEntries) Swap(i, j int) {
	entries[i], entries[j] = entries[j], entries[i]
}

type ladderParams struct {
	CoresToReplicas paramEntries `json:"coresToReplicas"`
	NodesToReplicas paramEntries `json:"nodesToReplicas"`
}

func (c *LadderController) SyncConfig(configMap *k8sclient.ConfigMap) error {
	glog.V(0).Infof("Detected ConfigMap version change (old: %s new: %s) - rebuilding lookup entries", c.version, configMap.Version)
	glog.V(2).Infof("Params from apiserver: \n%v", configMap.Data[ControllerType])
	params, err := parseParams([]byte(configMap.Data[ControllerType]))
	if err != nil {
		return fmt.Errorf("error parsing ladder params: %s", err)
	}
	sort.Sort(params.CoresToReplicas)
	sort.Sort(params.NodesToReplicas)
	c.params = params
	c.version = configMap.Version
	return nil
}

// parseParams Parse the params from JSON string
func parseParams(data []byte) (*ladderParams, error) {
	var p ladderParams
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("could not parse parameters (%s)", err)
	}
	for _, e := range p.CoresToReplicas {
		if len(e) != 2 {
			return nil, fmt.Errorf("invalid element %v in cores_to_replicas_map", e)
		}
		if e[0] < 1 || e[1] < 1 {
			return nil, fmt.Errorf("invalid negative values in entry %v in cores_to_replicas_map", e)
		}
	}
	for _, e := range p.NodesToReplicas {
		if len(e) != 2 {
			return nil, fmt.Errorf("invalid element %b in nodes_to_replicas_map", e)
		}
		if e[0] < 1 || e[1] < 1 {
			return nil, fmt.Errorf("invalid negative values in entry %v in nodes_to_replicas_map", e)
		}
	}
	return &p, nil
}

func (c *LadderController) GetParamsVersion() string {
	return c.version
}

func (c *LadderController) GetExpectedReplicas(status k8sclient.ClusterStatus) (int32, error) {
	// Get the expected replicas for the currently schedulable nodes and cores
	expReplicas := int32(c.getExpectedReplicasFromParams(int(status.SchedulableNodes), int(status.SchedulableCores)))

	return expReplicas, nil
}

func (c *LadderController) getExpectedReplicasFromParams(schedulableNodes, schedulableCores int) int {
	replicasFromCore := getExpectedReplicasFromEntries(schedulableCores, c.params.CoresToReplicas)
	replicasFromNode := getExpectedReplicasFromEntries(schedulableNodes, c.params.NodesToReplicas)

	// Returns the results which yields the most replicas
	if replicasFromCore > replicasFromNode {
		return replicasFromCore
	}
	return replicasFromNode
}

func getExpectedReplicasFromEntries(schedulableResources int, entries []paramEntry) int {
	if len(entries) == 0 {
		return 1
	}
	// Binary search for the corresponding replicas number
	pos := sort.Search(
		len(entries),
		func(i int) bool {
			return schedulableResources < entries[i][0]
		})
	if pos > 0 {
		pos = pos - 1
	}
	return entries[pos][1]
}

func (c *LadderController) GetControllerType() string {
	return ControllerType
}
