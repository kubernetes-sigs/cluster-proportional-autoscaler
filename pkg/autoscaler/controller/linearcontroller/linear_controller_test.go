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
	"testing"

	"github.com/davecgh/go-spew/spew"
)

func verifyParams(t *testing.T, scalerParams, expScalerParams *linearParams) {
	if scalerParams.CoresPerReplica != expScalerParams.CoresPerReplica ||
		scalerParams.NodesPerReplica != expScalerParams.NodesPerReplica ||
		scalerParams.Min != expScalerParams.Min ||
		scalerParams.Max != expScalerParams.Max {
		t.Errorf("Parser error - Expected params %v MISMATCHED: Got %v", expScalerParams, scalerParams)
	}
}

func TestControllerParser(t *testing.T) {
	testCases := []struct {
		jsonData  string
		expError  bool
		expParams *linearParams
	}{
		{
			`{
		      "coresPerReplica": 2,
		      "nodesPerReplica": 1,
		      "min": 1,
		      "max": 100,
		      "preventSinglePointFailure": true
		    }`,
			false,
			&linearParams{
				CoresPerReplica:           2,
				NodesPerReplica:           1,
				Min:                       1,
				Max:                       100,
				PreventSinglePointFailure: true,
			},
		},
		{ // Invalid JSON
			`{ "coresPerReplica": {{ 1:1 } }`,
			true,
			&linearParams{},
		},
		{ // Invalid string value
			`{ "coresPerReplica": "whatisthis"`,
			true,
			&linearParams{},
		},
		{ // Invalid negative value
			`{ "nodesPerReplica":  -20 }`,
			true,
			&linearParams{},
		},
		{ // Invalid max that smaller tham min
			`{ 
		      "nodesPerReplica": 1,
		      "min": 100,
		      "max": 50
		    }`,
			true,
			&linearParams{},
		},
		{ // Both coresPerReplica and nodesPerReplica are unset
			`{ 
		      "min": 1,
		      "max": 100
		    }`,
			true,
			&linearParams{},
		},
		// Wrong input for PreventSinglePointFailure.
		{
			`{
		      "coresPerReplica": 2,
		      "nodesPerReplica": 1,
		      "min": 1,
		      "max": 100,
		      "preventSinglePointFailure": invalid,
		    }`,
			true,
			&linearParams{},
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

func TestScaleFromSingleParam(t *testing.T) {
	testController := &LinearController{}
	testController.params = &linearParams{
		CoresPerReplica: 2,
		Min:             2,
		Max:             100,
	}

	testCases := []struct {
		numResources int
		expReplicas  int
	}{
		{0, 2},
		{1, 2},
		{2, 2},
		{3, 2},
		{4, 2},
		{6, 3},
		{6, 3},
		{10, 5},
		{11, 6},
		{19, 10},
		{20, 10},
		{21, 11},
		{30, 15},
		{40, 20},
	}

	for _, tc := range testCases {
		if replicas := testController.getExpectedReplicasFromParam(tc.numResources, testController.params.CoresPerReplica); tc.expReplicas != replicas {
			t.Errorf("Scaler Lookup failed Expected %d, Got %d", tc.expReplicas, replicas)
		}
	}
}

func TestScaleFromMultipleParams(t *testing.T) {
	testController := &LinearController{}
	testController.params = &linearParams{
		CoresPerReplica:           2,
		NodesPerReplica:           2.5,
		Min:                       1,
		Max:                       100,
		PreventSinglePointFailure: true,
	}

	testCases := []struct {
		numCores    int
		numNodes    int
		expReplicas int
	}{
		{0, 0, 1},
		{1, 2, 2},
		{2, 3, 2},
		{3, 4, 2},
		{4, 4, 2},
		{6, 4, 3},
		{6, 5, 3},
		{8, 5, 4},
		{8, 15, 6},
		{8, 16, 7},
		{19, 21, 10},
		{23, 20, 12},
		{26, 38, 16},
		{30, 49, 20},
		{40, 20, 20},
	}

	for _, tc := range testCases {
		if replicas := testController.getExpectedReplicasFromParams(tc.numNodes, tc.numCores); tc.expReplicas != replicas {
			t.Errorf("Scaler Lookup failed Expected %d, Got %d", tc.expReplicas, replicas)
		}
	}
}
