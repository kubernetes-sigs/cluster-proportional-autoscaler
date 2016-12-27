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

// Package options contains flags for initializing an autoscaler.
package options

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/golang/glog"
	"github.com/spf13/pflag"
)

// AutoScalerConfig configures and runs an autoscaler server
type AutoScalerConfig struct {
	Target            string
	ConfigMap         string
	Namespace         string
	DefaultParams     configMapData
	PollPeriodSeconds int
	PrintVer          bool
}

func NewAutoScalerConfig() *AutoScalerConfig {
	return &AutoScalerConfig{
		Namespace:         os.Getenv("MY_POD_NAMESPACE"),
		PollPeriodSeconds: 10,
		PrintVer:          false,
	}
}

func (c *AutoScalerConfig) ValidateFlags() error {
	var errorsFound bool
	c.Target = strings.ToLower(c.Target)
	if !isTargetFormatValid(c.Target) {
		errorsFound = true
	}
	if c.ConfigMap == "" {
		errorsFound = true
		glog.Errorf("--configmap parameter cannot be empty")
	}
	if c.Namespace == "" {
		errorsFound = true
		glog.Errorf("--namespace parameter not set and failed to fallback")
	}
	if c.PollPeriodSeconds < 1 {
		errorsFound = true
		glog.Errorf("--poll-period-seconds cannot be less than 1")
	}

	// Log all sanity check errors before returning a single error string
	if errorsFound {
		return fmt.Errorf("failed to validate all input parameters")
	}
	return nil
}

func isTargetFormatValid(target string) bool {
	if target == "" {
		glog.Errorf("--target parameter cannot be empty")
		return false
	}
	if !strings.HasPrefix(target, "deployment/") &&
		!strings.HasPrefix(target, "replicationcontroller/") &&
		!strings.HasPrefix(target, "replicaset/") {
		glog.Errorf("Target format error. Please use deployment/*, replicationcontroller/* or replicaset/* (not case sensitive).")
		return false
	}
	return true
}

type configMapData map[string]string

func (c *configMapData) Set(raw string) error {
	var rawData map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &rawData); err != nil {
		return err
	}
	*c = make(map[string]string)
	for key, param := range rawData {
		marshaled, err := json.Marshal(param)
		if err != nil {
			return err
		}
		(*c)[key] = string(marshaled)
	}
	return nil
}

func (c *configMapData) String() string {
	return fmt.Sprintf("%v", *c)
}

func (c *configMapData) Type() string {
	return "configMapData"
}

// AddFlags adds flags for a specific AutoScaler to the specified FlagSet
func (c *AutoScalerConfig) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.Target, "target", c.Target, "Target to scale. In format: deployment/*, replicationcontroller/* or replicaset/* (not case sensitive).")
	fs.StringVar(&c.ConfigMap, "configmap", c.ConfigMap, "ConfigMap containing our scaling parameters.")
	fs.StringVar(&c.Namespace, "namespace", c.Namespace, "Namespace for all operations, fallback to the namespace of this autoscaler(through MY_POD_NAMESPACE env) if not specified.")
	fs.IntVar(&c.PollPeriodSeconds, "poll-period-seconds", c.PollPeriodSeconds, "The time, in seconds, to check cluster status and perform autoscale.")
	fs.BoolVar(&c.PrintVer, "version", c.PrintVer, "Print the version and exit.")
	fs.Var(&c.DefaultParams, "default-params", "Default parameters(JSON format) for auto-scaling. Will create/re-create a ConfigMap with this default params if ConfigMap is not present.")
}
