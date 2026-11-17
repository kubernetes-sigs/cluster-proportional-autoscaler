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
	"time"

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

func TestHealthzFlags(t *testing.T) {
	defaults := NewAutoScalerConfig()
	if defaults.HealthzPort != 8080 || defaults.HealthzBindAddress != "" {
		t.Fatalf("expected defaults HealthzPort=8080 HealthzBindAddress=\"\", got %d %q",
			defaults.HealthzPort, defaults.HealthzBindAddress)
	}

	tests := []struct {
		name    string
		bind    string
		port    int
		wantErr bool
	}{
		{name: "default all interfaces", port: 8080},
		{name: "valid port 0 disabled", port: 0},
		{name: "valid port 1", port: 1},
		{name: "valid loopback IPv4 and custom port", bind: "127.0.0.1", port: 9154},
		{name: "valid loopback IPv6 and max port", bind: "::1", port: 65535},
		{name: "valid wildcard IPv4", bind: "0.0.0.0", port: 8080},
		{name: "valid wildcard IPv6", bind: "::", port: 8080},
		{name: "negative port", port: -1, wantErr: true},
		{name: "port above 65535", port: 65536, wantErr: true},
		{name: "invalid bind hostname", bind: "localhost", port: 8080, wantErr: true},
		{name: "invalid bind with port", bind: "127.0.0.1:8080", port: 8080, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := NewAutoScalerConfig()
			cfg.Target = "deployment/my-deployment"
			cfg.ConfigMap = "my-autoscaler"
			cfg.Namespace = "default"
			cfg.HealthzBindAddress = tc.bind
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

	if err := fs.Parse([]string{"--healthz-bind-address=127.0.0.1", "--healthz-port=9154"}); err != nil {
		t.Fatalf("fs.Parse() error = %v", err)
	}
	if cfg.HealthzBindAddress != "127.0.0.1" {
		t.Errorf("HealthzBindAddress = %q, want %q", cfg.HealthzBindAddress, "127.0.0.1")
	}
	if cfg.HealthzPort != 9154 {
		t.Errorf("HealthzPort = %d, want 9154", cfg.HealthzPort)
	}
}

func TestLeaderElectFlags(t *testing.T) {
	newValidCfg := func() *AutoScalerConfig {
		cfg := NewAutoScalerConfig()
		cfg.Target = "deployment/my-deployment"
		cfg.ConfigMap = "my-autoscaler"
		cfg.Namespace = "default"
		cfg.LeaderElect = true
		return cfg
	}

	cfg := newValidCfg()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	cfg.AddFlags(fs)
	if err := fs.Parse([]string{
		"--leader-elect=true",
		"--leader-elect-lease-duration=20s",
		"--leader-elect-renew-deadline=12s",
		"--leader-elect-retry-period=3s",
	}); err != nil {
		t.Fatalf("fs.Parse() error = %v", err)
	}
	if err := cfg.ValidateFlags(); err != nil {
		t.Fatalf("ValidateFlags() unexpected error = %v", err)
	}
	if cfg.LeaderElectResourceName != "my-autoscaler" || cfg.LeaderElectResourceNamespace != "default" {
		t.Errorf("defaults = (%q, %q), want (%q, %q)", cfg.LeaderElectResourceName, cfg.LeaderElectResourceNamespace, "my-autoscaler", "default")
	}

	invalidCases := []struct {
		name   string
		mutate func(*AutoScalerConfig)
	}{
		{name: "lease-duration equal to renew-deadline", mutate: func(c *AutoScalerConfig) { c.LeaderElectLeaseDuration = c.LeaderElectRenewDeadline }},
		{name: "renew-deadline equal to retry-period", mutate: func(c *AutoScalerConfig) { c.LeaderElectRenewDeadline = c.LeaderElectRetryPeriod }},
		{name: "sub-second lease-duration", mutate: func(c *AutoScalerConfig) {
			c.LeaderElectLeaseDuration = 500 * time.Millisecond
			c.LeaderElectRenewDeadline = 300 * time.Millisecond
			c.LeaderElectRetryPeriod = 100 * time.Millisecond
		}},
		{name: "non-positive retry-period", mutate: func(c *AutoScalerConfig) { c.LeaderElectRetryPeriod = 0 }},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			c := newValidCfg()
			tc.mutate(c)
			if err := c.ValidateFlags(); err == nil {
				t.Errorf("ValidateFlags() = nil, want error for %s", tc.name)
			}
		})
	}
}
