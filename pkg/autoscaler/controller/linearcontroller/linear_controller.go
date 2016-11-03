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

package linearcontroller

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/controller"
	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/k8sclient"

	"github.com/golang/glog"
)

var _ = controller.Interface(&LinearController{})

const (
	ControllerType = "linear"
)

type LinearController struct {
	params  *linearParams
	version string
}

func NewLinearController() controller.Interface {
	return &LinearController{}
}

type linearParams struct {
	CoresPerReplica int `json:"coresPerReplica"`
	NodesPerReplica int `json:"nodesPerReplica"`
	Min             int `json:"min"`
	Max             int `json:"max"`
}

func (c *LinearController) SyncConfig(configMap k8sclient.ConfigMap) error {
	glog.V(0).Infof("ConfigMap version change (old: %s new: %s) - rebuilding params\n", c.version, configMap.Version)
	params, err := parseParams([]byte(configMap.Data[ControllerType]))
	if err != nil {
		return fmt.Errorf("error parsing linear params: %s", err)
	}
	glog.V(4).Infof("Parsed new params: %v\n", params)
	c.params = params
	c.version = configMap.Version
	return nil
}

// parseParams Parse the params from JSON string
func parseParams(data []byte) (*linearParams, error) {
	var p linearParams
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("could not parse parameters (%s)", err)
	}
	if p.Min < 0 {
		return nil, fmt.Errorf("invalid negative value for min: %v", p.Min)
	} else if p.Min == 0 {
		glog.V(2).Infof("Defaulting min replicas count to 1 for linear controller")
		p.Min = 1
	}
	if p.Max != 0 && p.Max < p.Min {
		return nil, fmt.Errorf("max replicas count %v should be greater than / equal to min replicas count %v", p.Max, p.Min)
	}
	if p.CoresPerReplica == 0 && p.NodesPerReplica == 0 {
		return nil, fmt.Errorf("should at least provide either CoresPerReplica or NodesPerReplica (Greater than 0)")
	}
	if p.CoresPerReplica < 0 {
		return nil, fmt.Errorf("invalid negative value for coresPerReplica: %v", p.CoresPerReplica)
	}
	if p.NodesPerReplica < 0 {
		return nil, fmt.Errorf("invalid negative value for nodesPerReplica: %v", p.NodesPerReplica)
	}
	return &p, nil
}

func (c *LinearController) GetParamsVersion() string {
	return c.version
}

func (c *LinearController) GetExpectedReplicas(status k8sclient.ClusterStatus) (int32, error) {
	// Get the expected replicas for the currently schedulable nodes and cores
	expReplicas := int32(c.getExpectedReplicasFromParams(int(status.SchedulableNodes), int(status.SchedulableCores)))

	return expReplicas, nil
}

func (c *LinearController) getExpectedReplicasFromParams(schedulableNodes, schedulableCores int) int {
	replicasFromCore := c.getExpectedReplicasFromParam(schedulableCores, c.params.CoresPerReplica)
	replicasFromNode := c.getExpectedReplicasFromParam(schedulableNodes, c.params.NodesPerReplica)

	// Returns the results which yields the most replicas
	if replicasFromCore > replicasFromNode {
		return replicasFromCore
	}
	return replicasFromNode
}

func (c *LinearController) getExpectedReplicasFromParam(schedulableResources, resourcesPerReplica int) int {
	if resourcesPerReplica == 0 {
		return 1
	}
	res := math.Ceil(float64(schedulableResources) / float64(resourcesPerReplica))
	if c.params.Max != 0 {
		res = math.Min(float64(c.params.Max), res)
	}
	return int(math.Max(float64(c.params.Min), res))
}
