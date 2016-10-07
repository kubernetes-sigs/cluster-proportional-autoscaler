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

package options

import (
	"strings"
	"testing"
)

func TestIsTargetFormatValid(t *testing.T) {
	testCases := []struct {
		target    string
		expResult bool
	}{
		{
			"deployment/anything",
			true,
		},
		{
			"replicationcontroller/anything",
			true,
		},
		{
			"replicaset/anything",
			true,
		},
		{
			"DeplOymEnT/anything",
			true,
		},
		{
			"deployments/anything",
			false,
		},
		{
			"noexist/anything",
			false,
		},
		{
			"deployment",
			false,
		},
	}

	for _, tc := range testCases {
		tc.target = strings.ToLower(tc.target)
		res := isTargetFormatValid(tc.target)
		if res != tc.expResult {
			t.Errorf("target format verification for [%v] failed. Expected %v, Got %v", tc.target, tc.expResult, res)
		}
	}
}
