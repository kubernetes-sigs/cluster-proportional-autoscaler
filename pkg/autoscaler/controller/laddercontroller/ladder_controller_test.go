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
)

func verifyParams(t *testing.T, scalerParams, expScalerParams *ladderParams) {
	if len(expScalerParams.CoresToReplicas) != len(scalerParams.CoresToReplicas) {
		t.Errorf("scaler Params length mismatch Expected: %d, Got %d", len(expScalerParams.CoresToReplicas), len(scalerParams.CoresToReplicas))
		return
	}
	for n, expected := range expScalerParams.CoresToReplicas {
		parsed := scalerParams.CoresToReplicas[n]
		if expected[0] != parsed[0] || expected[1] != parsed[1] {
			t.Errorf("scaler parser error - Expected value %v MISMATCHED: Got %v", expected, parsed)
		}
	}

	if len(expScalerParams.NodesToReplicas) != len(scalerParams.NodesToReplicas) {
		t.Errorf("scaler Params length mismatch Expected: %d, Got %d", len(expScalerParams.NodesToReplicas), len(scalerParams.NodesToReplicas))
		return
	}
	for n, expected := range expScalerParams.NodesToReplicas {
		parsed := scalerParams.NodesToReplicas[n]
		if expected[0] != parsed[0] || expected[1] != parsed[1] {
			t.Errorf("scaler parser error - Expected value %v MISMATCHED: Got %v", expected, parsed)
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
			&ladderParams{CoresToReplicas: []paramEntry{paramEntry{1, 1}}},
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
		{ // Invalid value 0 in list
			`{ "coresToReplicas" :  [[ 0, 1]] }`,
			true,
			&ladderParams{},
		},
		{ // Invalid negative in list
			`{ "coresToReplicas" : [[:-200]] }`,
			true,
			&ladderParams{},
		},
		{
			`{
				"coresToReplicas":
				[
					[1, 1],
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
					paramEntry{1, 1},
					paramEntry{2, 2},
					paramEntry{3, 3},
					paramEntry{512, 5},
					paramEntry{1024, 7},
					paramEntry{2048, 10},
					paramEntry{4096, 15},
					paramEntry{8192, 20},
					paramEntry{12288, 30},
					paramEntry{16384, 40},
					paramEntry{20480, 50},
					paramEntry{24576, 60},
					paramEntry{28672, 70},
					paramEntry{65535, 100},
					paramEntry{32768, 80},
				},
			},
		},
	}

	for _, tc := range testCases {
		params, err := parseParams([]byte(tc.jsonData))
		if tc.expError {
			if err == nil {
				t.Errorf("unexpected parsing success. Expected failure")
				spew.Dump(tc)
				spew.Dump(params)
			}
			continue
		}
		if err != nil && !tc.expError {
			t.Errorf("unexpected parse failure")
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
					paramEntry{2, 2},
					paramEntry{3, 3},
					paramEntry{512, 5},
					paramEntry{1024, 7},
					paramEntry{20480, 50},
					paramEntry{4096, 15},
					paramEntry{2048, 10},
					paramEntry{8192, 20},
					paramEntry{65535, 100},
					paramEntry{16384, 40},
					paramEntry{12288, 30},
					paramEntry{1, 1},
					paramEntry{24576, 60},
					paramEntry{32768, 80},
					paramEntry{28672, 70},
				},
			},
			&ladderParams{
				CoresToReplicas: []paramEntry{
					paramEntry{1, 1},
					paramEntry{2, 2},
					paramEntry{3, 3},
					paramEntry{512, 5},
					paramEntry{1024, 7},
					paramEntry{2048, 10},
					paramEntry{4096, 15},
					paramEntry{8192, 20},
					paramEntry{12288, 30},
					paramEntry{16384, 40},
					paramEntry{20480, 50},
					paramEntry{24576, 60},
					paramEntry{28672, 70},
					paramEntry{32768, 80},
					paramEntry{65535, 100},
				},
			},
		},
		{
			&ladderParams{
				CoresToReplicas: []paramEntry{
					paramEntry{65535, 100},
					paramEntry{32768, 80},
					paramEntry{28672, 70},
					paramEntry{24576, 60},
					paramEntry{20480, 50},
					paramEntry{16384, 40},
					paramEntry{12288, 30},
					paramEntry{8192, 20},
					paramEntry{4096, 15},
					paramEntry{2048, 10},
					paramEntry{1024, 7},
					paramEntry{512, 5},
					paramEntry{3, 3},
					paramEntry{2, 2},
					paramEntry{1, 1},
				},
			},
			&ladderParams{
				CoresToReplicas: []paramEntry{
					paramEntry{1, 1},
					paramEntry{2, 2},
					paramEntry{3, 3},
					paramEntry{512, 5},
					paramEntry{1024, 7},
					paramEntry{2048, 10},
					paramEntry{4096, 15},
					paramEntry{8192, 20},
					paramEntry{12288, 30},
					paramEntry{16384, 40},
					paramEntry{20480, 50},
					paramEntry{24576, 60},
					paramEntry{28672, 70},
					paramEntry{32768, 80},
					paramEntry{65535, 100},
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
		paramEntry{1, 1},
		paramEntry{2, 2},
		paramEntry{3, 3},
		paramEntry{4, 4},
		paramEntry{10, 10},
		paramEntry{20, 20},
	}

	testCases := []struct {
		entries      []paramEntry
		numResources int
		expReplicas  int
	}{
		{testEntries, 0, 1},
		{testEntries, 1, 1},
		{testEntries, 2, 2},
		{testEntries, 3, 3},
		{testEntries, 4, 4},
		{testEntries, 6, 4},
		{testEntries, 6, 4},
		{testEntries, 10, 10},
		{testEntries, 11, 10},
		{testEntries, 19, 10},
		{testEntries, 20, 20},
		{testEntries, 21, 20},
		{testEntries, 21, 20},
		{testEntries, 40, 20},
	}

	for _, tc := range testCases {
		if replicas := getExpectedReplicasFromEntries(tc.numResources, tc.entries); tc.expReplicas != replicas {
			t.Errorf("scaler Lookup failed Expected %d, Got %d", tc.expReplicas, replicas)
		}
	}
}
