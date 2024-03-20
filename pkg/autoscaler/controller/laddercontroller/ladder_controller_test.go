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
	"sort"
	"testing"

	"github.com/davecgh/go-spew/spew"

	"github.com/kubernetes-sigs/cluster-proportional-autoscaler/pkg/autoscaler/k8sclient"
)

func verifyParams(t *testing.T, scalerParams, expScalerParams *ladderParams) {
	if len(expScalerParams.CoresToReplicas) != len(scalerParams.CoresToReplicas) {
		t.Errorf("Scaler Params length mismatch Expected: %d, Got %d", len(expScalerParams.CoresToReplicas), len(scalerParams.CoresToReplicas))
		return
	}
	for n, expected := range expScalerParams.CoresToReplicas {
		parsed := scalerParams.CoresToReplicas[n]
		if expected[0] != parsed[0] || expected[1] != parsed[1] {
			t.Errorf("Scaler parser error - Expected value %v MISMATCHED: Got %v", expected, parsed)
		}
	}

	if len(expScalerParams.NodesToReplicas) != len(scalerParams.NodesToReplicas) {
		t.Errorf("Scaler Params length mismatch Expected: %d, Got %d", len(expScalerParams.NodesToReplicas), len(scalerParams.NodesToReplicas))
		return
	}
	for n, expected := range expScalerParams.NodesToReplicas {
		parsed := scalerParams.NodesToReplicas[n]
		if expected[0] != parsed[0] || expected[1] != parsed[1] {
			t.Errorf("Scaler parser error - Expected value %v MISMATCHED: Got %v", expected, parsed)
		}
	}
}

func TestControllerParser(t *testing.T) {
	testCases := []struct {
		jsonData  string
		expError  bool
		expParams *ladderParams
	}{
		{
			`{ "coresToReplicas" : [ [1,1] ] }`,
			false,
			&ladderParams{CoresToReplicas: []paramEntry{{1, 1}}},
		},
		{ // Invalid JSON
			`{ "coresToReplicas" : {{ 1:1 } }`,
			true,
			&ladderParams{},
		},
		{ // Invalid string value in list
			`{ "coresToReplicas" : [[ "1, "a"]] }`,
			true,
			&ladderParams{},
		},
		{ // Invalid negative in list
			`{ "coresToReplicas" : [[:-200]] }`,
			true,
			&ladderParams{},
		},
		// IncludeUnschedulableNodes must default to false for backwards compatibility.
		{
			`{
				"coresToReplicas":
				[
					[0, 0],
					[1, 0],
					[2, 2],
					[3, 3],
					[512, 5],
					[1024, 7],
					[2048, 10],
					[4096, 15],
					[8192, 20],
					[12288, 30],
					[16384, 40],
					[20480, 50],
					[24576, 60],
					[28672, 70],
					[65535, 100],
					[32768, 80 ]
				]
			}`,
			false,
			&ladderParams{
				CoresToReplicas: []paramEntry{
					{0, 0},
					{1, 0},
					{2, 2},
					{3, 3},
					{512, 5},
					{1024, 7},
					{2048, 10},
					{4096, 15},
					{8192, 20},
					{12288, 30},
					{16384, 40},
					{20480, 50},
					{24576, 60},
					{28672, 70},
					{65535, 100},
					{32768, 80},
				},
				IncludeUnschedulableNodes: false,
			},
		},
		{
			`{
				"coresToReplicas":
				[
					[0, 0],
					[1, 0],
					[2, 2],
					[3, 3]
				],
				"nodesToReplicas":
				[
					[1, 1],
					[2, 2],
					[3, 3]
				],
				"includeUnschedulableNodes": true
			}`,
			false,
			&ladderParams{
				CoresToReplicas: []paramEntry{
					{0, 0},
					{1, 0},
					{2, 2},
					{3, 3},
				},
				NodesToReplicas: []paramEntry{
					{1, 1},
					{2, 2},
					{3, 3},
				},
				IncludeUnschedulableNodes: true,
			},
		},
	}

	for _, tc := range testCases {
		params, err := parseParams([]byte(tc.jsonData))
		if tc.expError {
			if err == nil {
				t.Errorf("Unexpected parsing success. Expected failure")
				spew.Dump(tc)
				spew.Dump(params)
			}
			continue
		}
		if err != nil && !tc.expError {
			t.Errorf("Unexpected parse failure: %v", err)
			spew.Dump(tc)
			continue
		}
		verifyParams(t, params, tc.expParams)
	}
}

func TestControllerSorter(t *testing.T) {
	testCases := []struct {
		testParams *ladderParams
		expParams  *ladderParams
	}{
		{
			&ladderParams{
				CoresToReplicas: []paramEntry{
					{2, 2},
					{3, 3},
					{512, 5},
					{1024, 7},
					{20480, 50},
					{4096, 15},
					{2048, 10},
					{8192, 20},
					{65535, 100},
					{16384, 40},
					{12288, 30},
					{1, 1},
					{24576, 60},
					{32768, 80},
					{28672, 70},
				},
			},
			&ladderParams{
				CoresToReplicas: []paramEntry{
					{1, 1},
					{2, 2},
					{3, 3},
					{512, 5},
					{1024, 7},
					{2048, 10},
					{4096, 15},
					{8192, 20},
					{12288, 30},
					{16384, 40},
					{20480, 50},
					{24576, 60},
					{28672, 70},
					{32768, 80},
					{65535, 100},
				},
			},
		},
		{
			&ladderParams{
				CoresToReplicas: []paramEntry{
					{65535, 100},
					{32768, 80},
					{28672, 70},
					{24576, 60},
					{20480, 50},
					{16384, 40},
					{12288, 30},
					{8192, 20},
					{4096, 15},
					{2048, 10},
					{1024, 7},
					{512, 5},
					{3, 3},
					{2, 2},
					{1, 1},
				},
			},
			&ladderParams{
				CoresToReplicas: []paramEntry{
					{1, 1},
					{2, 2},
					{3, 3},
					{512, 5},
					{1024, 7},
					{2048, 10},
					{4096, 15},
					{8192, 20},
					{12288, 30},
					{16384, 40},
					{20480, 50},
					{24576, 60},
					{28672, 70},
					{32768, 80},
					{65535, 100},
				},
			},
		},
	}

	for _, tc := range testCases {
		sort.Sort(tc.testParams.CoresToReplicas)
		verifyParams(t, tc.testParams, tc.expParams)
	}
}

func TestControllerScaler(t *testing.T) {
	testEntries := []paramEntry{
		{1, 1},
		{2, 2},
		{3, 3},
		{4, 4},
		{10, 10},
		{20, 20},
	}

	testCases := []struct {
		numResources int
		expReplicas  int
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 3},
		{4, 4},
		{6, 4},
		{6, 4},
		{10, 10},
		{11, 10},
		{19, 10},
		{20, 20},
		{21, 20},
		{21, 20},
		{40, 20},
	}

	for _, tc := range testCases {
		if replicas := getExpectedReplicasFromEntries(tc.numResources, testEntries); tc.expReplicas != replicas {
			t.Errorf("Scaler Lookup failed Expected %d, Got %d", tc.expReplicas, replicas)
		}
	}
}

func TestControllerScalerFromZero(t *testing.T) {
	testEntries := []paramEntry{
		{0, 0},
		{3, 3},
	}

	testEntriesFromOne := []paramEntry{
		{1, 0},
		{3, 3},
	}

	testCases := []struct {
		numResources int
		expReplicas  int
	}{
		{0, 0},
		{1, 0},
		{2, 0},
		{3, 3},
		{4, 3},
	}

	for _, tc := range testCases {
		if replicas := getExpectedReplicasFromEntries(tc.numResources, testEntries); tc.expReplicas != replicas {
			t.Errorf("Scaler Lookup failed Expected %d, Got %d", tc.expReplicas, replicas)
		}
		if replicas := getExpectedReplicasFromEntries(tc.numResources, testEntriesFromOne); tc.expReplicas != replicas {
			t.Errorf("Scaler Lookup failed Expected %d, Got %d", tc.expReplicas, replicas)
		}
	}
}

func TestScaleFromUnschedulableNodes(t *testing.T) {
	nodesToReplicas := []paramEntry{
		{0, 0},
		{1, 1},
		{2, 2},
		{3, 3},
	}
	coresToReplicas := []paramEntry{
		{0, 0},
		{4, 1},
		{8, 2},
		{12, 3},
	}

	testcases := []struct {
		clusterStatus           *k8sclient.ClusterStatus
		expectedReplicas        int32
		includeSchedulableNodes bool
	}{
		{
			clusterStatus: &k8sclient.ClusterStatus{
				TotalNodes:       3,
				SchedulableNodes: 2,
				TotalCores:       12,
				SchedulableCores: 8,
			},
			expectedReplicas:        3,
			includeSchedulableNodes: true,
		},
		{
			clusterStatus: &k8sclient.ClusterStatus{
				TotalNodes:       3,
				SchedulableNodes: 1,
				TotalCores:       12,
				SchedulableCores: 4,
			},
			expectedReplicas:        1,
			includeSchedulableNodes: false,
		},
	}

	for _, tc := range testcases {
		c := &LadderController{
			params: &ladderParams{
				CoresToReplicas:           coresToReplicas,
				NodesToReplicas:           nodesToReplicas,
				IncludeUnschedulableNodes: tc.includeSchedulableNodes,
			},
		}
		actualReplicas, err := c.GetExpectedReplicas(tc.clusterStatus)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
			spew.Dump(tc)
			continue
		}
		if tc.expectedReplicas != actualReplicas {
			t.Errorf("ScaleFromUnschedulableNodes failed Expected %d, Got %d", tc.expectedReplicas, actualReplicas)
			spew.Dump(tc)
			continue
		}
	}
}
