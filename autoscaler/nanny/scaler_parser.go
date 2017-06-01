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
	"encoding/json"
	"fmt"
)

type scalerEntry [2]int

type scalerParams struct {
	CoresToReplicasMap []scalerEntry `json:"cores_to_replicas_map"`
	NodesToReplicasMap []scalerEntry `json:"nodes_to_replicas_map"`
}

const simpleScalarParamsKeyName = "simple-scalar.scale-map"

// parseScalerParams Parse the scaler params JSON string
func parseScalerParams(data []byte) (params *scalerParams, err error) {
	var p scalerParams
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("Could not parse scaler parameters (%s)", err)
	}
	for _, e := range p.CoresToReplicasMap {
		if len(e) != 2 {
			return nil, fmt.Errorf("Invalid element %s in cores_to_replicas_map", e)
		}
		if e[0] < 1 || e[1] < 1 {
			return nil, fmt.Errorf("Invalid negative values in entry %s in cores_to_replicas_map", e)
		}
	}
	for _, e := range p.NodesToReplicasMap {
		if len(e) != 2 {
			return nil, fmt.Errorf("Invalid element %s in nodes_to_replicas_map", e)
		}
		if e[0] < 1 || e[1] < 1 {
			return nil, fmt.Errorf("Invalid negative values in entry %s in nodes_to_replicas_map", e)
		}
	}
	return &p, nil
}

func fetchAndParseScalerParams(k *KubernetesClient, configmap string) (*scalerParams, string, error) {
	data, version, err := k.FetchConfigMap(k.namespace, configmap, simpleScalarParamsKeyName)
	if err != nil {
		return nil, "", err
	}
	params, err := parseScalerParams([]byte(data))
	if err != nil {
		return nil, "", err
	}
	return params, version, nil
}
