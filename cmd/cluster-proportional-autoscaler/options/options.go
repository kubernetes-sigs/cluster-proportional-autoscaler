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

// AutoscalerServerConfig configures and runs an autoscaler server
type AutoScalerConfig struct {
	Target            string
	ConfigMap         string
	Namespace         string
	Mode              string
	DefaultParams     map[string]string
	PollPeriodSeconds int
	PrintVer          bool
}

func NewAutoScalerConfig() *AutoScalerConfig {
	return &AutoScalerConfig{
		Namespace:         os.Getenv("MY_POD_NAMESPACE"),
		Mode:              "linear",
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
		glog.Errorf("--configmap parameter cannot be empty\n")
	}
	if c.Namespace == "" {
		errorsFound = true
		glog.Errorf("--namespace parameter not set and failed to fallback\n")
	}
	if c.PollPeriodSeconds < 1 {
		errorsFound = true
		glog.Errorf("--poll-period-seconds cannot be less than 1\n")
	}
	params, err := pflag.CommandLine.GetString("default-params")
	if err != nil {
		errorsFound = true
		glog.Errorf("Error getting --default-params: %v\n", err)
	} else {
		if params != "" {
			c.DefaultParams, err = parseParamsFromString(params)
			if err != nil {
				errorsFound = true
				glog.Errorf("Error parsing given configMap params: %v\n", err)
			}
		}
	}

	// Log all sanity check errors before returning a single error string
	if errorsFound {
		return fmt.Errorf("failed to validate all input parameters")
	}
	return nil
}

func parseParamsFromString(params string) (map[string]string, error) {
	var rawData map[string]interface{}
	if err := json.Unmarshal([]byte(params), &rawData); err != nil {
		return nil, err
	}
	configData := make(map[string]string)
	for key, param := range rawData {
		marshaled, err := json.Marshal(param)
		if err != nil {
			return nil, err
		}
		configData[key] = string(marshaled)
	}
	return configData, nil
}

func isTargetFormatValid(target string) bool {
	if target == "" {
		glog.Errorf("--target parameter cannot be empty\n")
		return false
	}
	if !strings.HasPrefix(target, "deployment/") &&
		!strings.HasPrefix(target, "replicationcontroller/") &&
		!strings.HasPrefix(target, "replicaset/") {
		glog.Errorf("Target format error. Please use deployment/*, replicationcontroller/* or replicaset/* (not case sensitive).\n")
		return false
	}
	return true
}

// AddFlags adds flags for a specific ProxyServer to the specified FlagSet
func (c *AutoScalerConfig) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.Target, "target", c.Target, "Target to scale. In format: deployment/*, replicationcontroller/* or replicaset/* (not case sensitive).")
	fs.StringVar(&c.ConfigMap, "configmap", c.ConfigMap, "ConfigMap containing our scaling parameters.")
	fs.StringVar(&c.Namespace, "namespace", c.Namespace, "Namespace for all operations, fallback to the namespace of this autoscaler(through MY_POD_NAMESPACE env) if not specified.")
	fs.StringVar(&c.Mode, "mode", c.Mode, "Control mode(linear/ladder).")
	fs.IntVar(&c.PollPeriodSeconds, "poll-period-seconds", c.PollPeriodSeconds, "The time, in seconds, to check cluster status and perform autoscale.")
	fs.BoolVar(&c.PrintVer, "version", c.PrintVer, "Print the version and exit.")
	fs.String("default-params", "", "Default parameters(JSON format) for auto-scaling. Will create/re-create a ConfigMap with this default params if not present.")
}
