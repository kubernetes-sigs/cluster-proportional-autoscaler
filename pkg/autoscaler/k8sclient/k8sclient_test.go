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

package k8sclient

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestGetScaleTarget(t *testing.T) {
	testCases := []struct {
		target        string
		expectedGroup *schema.GroupResource
		expName       string
		expError      bool
	}{
		{
			"deployment/anything",
			&schema.GroupResource{Group: "apps", Resource: "deployments"},
			"anything",
			false,
		},
		{
			"replicationcontroller/anotherthing",
			&schema.GroupResource{Group: "", Resource: "replicationcontrollers"},
			"anotherthing",
			false,
		},
		{
			"scalecrd.v1.example/anything",
			&schema.GroupResource{Group: "example", Resource: "scalecrd"},
			"anything",
			false,
		},
		{
			"scalecrd.example/anything",
			&schema.GroupResource{Group: "example", Resource: "scalecrd"},
			"anything",
			false,
		},
		{
			"replicationcontroller",
			nil,
			"",
			true,
		},
		{
			"replicaset/anything/what",
			nil,
			"",
			true,
		},
	}

	for _, tc := range testCases {
		res, err := getScaleTarget(tc.target, "default")
		if err != nil && !tc.expError {
			t.Errorf("Expect no error, got error for target: %v", tc.target)
			continue
		} else if err == nil && tc.expError {
			t.Errorf("Expect error, got no error for target: %v", tc.target)
			continue
		}
		if !reflect.DeepEqual(res.groupResource, tc.expectedGroup) || res.name != tc.expName {
			t.Errorf("Expect kind: %v, name: %v\ngot kind: %v, name: %v", tc.expectedGroup, tc.expName, res.groupResource, res.name)
		}
	}
}
