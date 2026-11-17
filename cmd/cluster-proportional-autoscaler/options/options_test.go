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

	"github.com/spf13/pflag"
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
			"DeplOymEnT/anything, replicaset/anything,replicationcontroller/anything",
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
			t.Errorf("Target format verification for [%v] failed. Expected %v, Got %v", tc.target, tc.expResult, res)
		}
	}
}

func TestHealthzPortFlags(t *testing.T) {
	defaults := NewAutoScalerConfig()
	if defaults.HealthzPort != 8080 {
		t.Fatalf("expected default HealthzPort=8080, got %d", defaults.HealthzPort)
	}

	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{name: "valid port 0 disabled", port: 0},
		{name: "valid port 1", port: 1},
		{name: "valid custom port", port: 9154},
		{name: "valid max port", port: 65535},
		{name: "negative port", port: -1, wantErr: true},
		{name: "port above 65535", port: 65536, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := NewAutoScalerConfig()
			cfg.Target = "deployment/my-deployment"
			cfg.ConfigMap = "my-autoscaler"
			cfg.Namespace = "default"
			cfg.HealthzPort = tc.port

			err := cfg.ValidateFlags()
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateFlags() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestHealthzAddFlags(t *testing.T) {
	cfg := NewAutoScalerConfig()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	cfg.AddFlags(fs)

	if err := fs.Parse([]string{"--healthz-port=9154"}); err != nil {
		t.Fatalf("fs.Parse() error = %v", err)
	}
	if cfg.HealthzPort != 9154 {
		t.Errorf("HealthzPort = %d, want 9154", cfg.HealthzPort)
	}
}
