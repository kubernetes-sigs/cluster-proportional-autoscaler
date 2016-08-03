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
	"testing"

	"github.com/davecgh/go-spew/spew"
)

type scalerParserTestStruct struct {
	scaler        *Scaler
	jsonData      string
	expError      bool
	params        *scalerParams
	coreLadder    []int
	coreScalerMap map[int]int
	nodeLadder    []int
	nodeScalerMap map[int]int
}

// Validates deserialization from JSON
func verifyParams(t *testing.T, scalerParams, expScalerParams *scalerParams) {
	if len(expScalerParams.CoresToReplicasMap) != len(scalerParams.CoresToReplicasMap) {
		t.Errorf("Scaler Params length mismatch Expected: %d, Got %d", len(expScalerParams.CoresToReplicasMap), len(scalerParams.CoresToReplicasMap))
		return
	}
	for n, e := range expScalerParams.CoresToReplicasMap {
		e1 := scalerParams.CoresToReplicasMap[n]
		if e[0] != e1[0] || e[1] != e1[1] {
			t.Errorf("scaler parser error - Expected value %s MISMATCHED: Got %s", e, e1)
		}
	}
	if len(expScalerParams.NodesToReplicasMap) != len(scalerParams.NodesToReplicasMap) {
		t.Errorf("Scaler Params length mismatch Expected: %d, Got %d", len(expScalerParams.NodesToReplicasMap), len(scalerParams.NodesToReplicasMap))
		return
	}
	for n, e := range expScalerParams.NodesToReplicasMap {
		e1 := scalerParams.NodesToReplicasMap[n]
		if e[0] != e1[0] || e[1] != e1[1] {
			t.Errorf("scaler parser error - Expected value %s MISMATCHED: Got %s", e, e1)
		}
	}
}

// Validates construction of the lookup ladder and maps
func verifyScalerBuilder(t *testing.T, tc scalerParserTestStruct) {
	if len(tc.scaler.coreLadder) != len(tc.coreLadder) {
		t.Errorf("Core Scaler Ladder Length mismatch - Expected %d Got %d", len(tc.coreLadder), len(tc.scaler.coreLadder))
	} else {
		for n, v := range tc.coreLadder {
			if v != tc.scaler.coreLadder[n] {
				t.Errorf("Core Scaler ladder index %d wrong - Expected %d, Got %d", n, v, tc.scaler.coreLadder[n])
			}
		}
	}
	if len(tc.scaler.coreScalerMap) != len(tc.coreScalerMap) {
		t.Errorf("Core Scaler Map Size mismatch - Expected %d Got %d", len(tc.coreScalerMap), len(tc.scaler.coreScalerMap))
	} else {
		for k, v := range tc.coreScalerMap {
			v1, ok := tc.scaler.coreScalerMap[k]
			if !ok {
				t.Errorf("Expected key %d not found in actual map", k)
			}
			if v != v1 {
				t.Errorf("Expected value %d for key %d MISMATCHED: Got %d", v, k, v1)
			}
		}
	}
}

func TestScalerParser(t *testing.T) {
	testCases := []scalerParserTestStruct{
		/*
			{
				&Scaler{},
				`{ "cores_to_replicas_map" : [ [1,1] ] }`,
				false,
				&scalerParams{CoresToReplicasMap: []scalerEntry{[]int{1, 1}}},
				[]int{1},
				map[int]int{1: 1},
				[]int{},
				map[int]int{},
			},
			{ // Invalid JSON
				&Scaler{},
				`{ "cores_to_replicas_map" : {{ 1:1 } }`,
				true,
				&scalerParams{},
				[]int{},
				map[int]int{},
				[]int{},
				map[int]int{},
			},
			{ // Invalid string value in list
				&Scaler{},
				`{ "cores_to_replicas_map" : [[ "1, "a"]] }`,
				true,
				&scalerParams{},
				[]int{},
				map[int]int{},
				[]int{},
				map[int]int{},
			},
			{ // Invalid value 0 in list
				&Scaler{},
				`{ "cores_to_replicas_map" :  [[ 0, 1]] }`,
				true,
				&scalerParams{},
				[]int{},
				map[int]int{},
				[]int{},
				map[int]int{},
			},
			{ // Invalid negative in list
				&Scaler{},
				`{ "cores_to_replicas_map" : [[:-200]] }`,
				true,
				&scalerParams{},
				[]int{},
				map[int]int{},
				[]int{},
				map[int]int{},
			},
		*/
		{
			&Scaler{},
			`{ "cores_to_replicas_map" : [ [1, 1], [2, 2], [3, 3], [512, 5], [1024, 7], [2048, 10], [4096, 15], [8192, 20],
		                            [12288, 30], [16384, 40], [20480, 50], [24576, 60], [28672, 70], [65535, 100], [32768, 80 ] ] }`,
			false,
			&scalerParams{CoresToReplicasMap: []scalerEntry{
				scalerEntry{1, 1}, scalerEntry{2, 2}, scalerEntry{3, 3}, scalerEntry{512, 5}, scalerEntry{1024, 7},
				scalerEntry{2048, 10}, scalerEntry{4096, 15}, scalerEntry{8192, 20}, scalerEntry{12288, 30},
				scalerEntry{16384, 40}, scalerEntry{20480, 50},
				scalerEntry{24576, 60}, scalerEntry{28672, 70}, scalerEntry{32768, 80}, scalerEntry{65535, 100},
			},
			},
			[]int{1, 2, 3, 512, 1024, 2048, 4096, 8192, 12288, 16384, 20480, 24576, 28672, 32768, 65535},
			map[int]int{1: 1, 2: 2, 3: 3, 512: 5, 1024: 7, 2048: 10,
				4096: 15, 8192: 20, 12288: 30, 16384: 40, 20480: 50,
				24576: 60, 28672: 70, 32768: 80, 65535: 100},
			[]int{},
			map[int]int{},
		},
		/*
			{
				&Scaler{},
				`{ "cores_to_replicas_map" : [ [1, 1], [1, 1], [2, 2], [3, 3], [512, 5], [1024, 7], [2048, 10], [4096, 15], [8192, 20], [12288, 30], [16384, 40], [20480, 50], [24576, 60], [28672, 70], [65535, 100], [32768, 80] ] }`,
				false,
				&scalerParams{CoresToReplicasMap: []scalerEntry{[]int{1: 1}, []int{2: 2}, []int{3: 3}, []int{512: 5}, []int{1024: 7},
					[]int{2048: 10}, []int{4096: 15}, []int{8192: 20}, []int{12288: 30}, []int{16384: 40}, []int{20480: 50},
					[]int{24576: 60}, []int{28672: 70}, []int{32768: 80}, []int{65535: 100}}},
				[]int{1, 2, 3, 512, 1024, 2048, 4096, 8192, 12288, 16384, 20480, 24576, 28672, 32768, 65535},
				map[int]int{1: 1, 2: 2, 3: 3, 512: 5, 1024: 7, 2048: 10, 4096: 15, 8192: 20, 12288: 30, 16384: 40, 20480: 50, 24576: 60, 28672: 70, 32768: 80, 65535: 100},
				[]int{},
				map[int]int{},
			},
		*/
	}
	for _, tc := range testCases {
		fmt.Println("Running testcase")
		spew.Dump(tc)
		params, err := parseScalerParams([]byte(tc.jsonData))
		if tc.expError {
			if err == nil {
				t.Errorf("Unexpected parsing success. Expected failure")
			}
			continue
		}
		if err != nil && !tc.expError {
			t.Errorf("Unexpected parse failure")
			continue
		}
		verifyParams(t, params, tc.params)
		tc.scaler.coreScalerMap, tc.scaler.coreLadder = tc.scaler.buildScalerMap(params.CoresToReplicasMap)
		tc.scaler.nodeScalerMap, tc.scaler.nodeLadder = tc.scaler.buildScalerMap(params.NodesToReplicasMap)
		verifyScalerBuilder(t, tc)
	}
}

func TestScaling(t *testing.T) {
	referenceScaler := Scaler{
		Verbose:    true,
		coreLadder: []int{1, 2, 3, 4, 10, 20},
		coreScalerMap: map[int]int{
			1: 1, 2: 2, 3: 3, 4: 4, 10: 10, 20: 20,
		},
		nodeLadder: []int{1, 2},
		nodeScalerMap: map[int]int{
			1: 1, 2: 2,
		},
	}
	testCases := []struct {
		scaler      *Scaler
		numNodes    int
		numCores    int
		expReplicas int
	}{
		{&referenceScaler, 1, 1, 1},
		{&referenceScaler, 2, 1, 2},
		{&referenceScaler, 2, 2, 2},
		{&referenceScaler, 2, 3, 3},
		{&referenceScaler, 2, 4, 4},
		{&referenceScaler, 2, 6, 4},
		{&referenceScaler, 2, 6, 4},
		{&referenceScaler, 2, 10, 10},
		{&referenceScaler, 2, 11, 10},
		{&referenceScaler, 2, 19, 10},
		{&referenceScaler, 2, 20, 20},
		{&referenceScaler, 2, 21, 20},
		{&referenceScaler, 1, 21, 20},
	}
	for _, tc := range testCases {
		if replicas := tc.scaler.scalerLookup(tc.numNodes, tc.numCores); tc.expReplicas != replicas {
			t.Errorf("Scaler Lookup failed Expected %d, Got %d", tc.expReplicas, replicas)
		}
	}
}
