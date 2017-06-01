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
	"log"
	"sort"
)

// Scaler determines the number of replicas to run
type Scaler struct {
	version       string
	Verbose       bool
	coreLadder    []int
	coreScalerMap map[int]int
	nodeLadder    []int
	nodeScalerMap map[int]int
}

func (s *Scaler) buildScalerMap(params []scalerEntry) (map[int]int, []int) {
	scalerMap := make(map[int]int)
	for _, e := range params {
		k := e[0]
		v := e[1]
		scalerMap[k] = v
	}
	// construct a search coreLadder from the map
	ladder := make([]int, 0, len(scalerMap))
	for k := range scalerMap {
		ladder = append(ladder, k)
	}
	sort.Ints(ladder)
	return scalerMap, ladder
}

func (s *Scaler) scaleWithNodesAndCores(params *scalerParams, resourceVersion string, numCurrentNodes, schedulableCores int) int {
	if !(resourceVersion == s.version) {
		// Only rebuild our tables if the configmap changed
		log.Printf("Detected ConfigMap version change (old: %s new: %s) - rebuilding lookup tables\n", s.version, resourceVersion)
		s.coreScalerMap, s.coreLadder = s.buildScalerMap(params.CoresToReplicasMap)
		for _, k := range s.coreLadder {
			log.Printf("Cores > %5d => %3d replicas\n", k, s.coreScalerMap[k])
		}
		s.nodeScalerMap, s.nodeLadder = s.buildScalerMap(params.NodesToReplicasMap)
		for _, k := range s.nodeLadder {
			log.Printf("Nodes > %5d => %3d replicas\n", k, s.coreScalerMap[k])
		}
		s.version = resourceVersion
	}
	return s.scalerLookup(numCurrentNodes, schedulableCores)
}

func (s *Scaler) scalerLookup(schedulableNodes, schedulableCores int) int {
	var neededReplicas int = 1
	for _, coreCount := range s.coreLadder {
		if int(coreCount) > schedulableCores {
			break
		}
		neededReplicas = s.coreScalerMap[coreCount]
	}
	for _, nodeCount := range s.nodeLadder {
		if int(nodeCount) > schedulableNodes {
			break
		}
		replicas := s.coreScalerMap[nodeCount]
		if replicas > neededReplicas {
			neededReplicas = replicas
		}
	}
	// Returns the lookup which yields the most replicas
	return neededReplicas
}
