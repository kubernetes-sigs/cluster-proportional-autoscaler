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

package main

import (
	"fmt"
	"os"

	"k8s.io/kubernetes/pkg/util/flag"

	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/cmd/cluster-proportional-autoscaler/options"
	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler"
	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/version"

	"github.com/golang/glog"
	"github.com/spf13/pflag"
)

func main() {
	config := options.NewAutoScalerConfig()
	config.AddFlags(pflag.CommandLine)
	flag.InitFlags()

	if config.PrintVer {
		fmt.Printf("%s\n", version.VERSION)
		os.Exit(0)
	}

	// Perform further validation of flags.
	if err := config.ValidateFlags(); err != nil {
		glog.Errorf("%v\n", err)
		os.Exit(1)
	}

	glog.V(0).Infof("Scaling Namespace: %s, Target: %s, Mode: %v\n", config.Namespace, config.Target, config.Mode)
	scaler, err := autoscaler.NewAutoScaler(config)
	if err != nil {
		glog.Errorf("%v\n", err)
		os.Exit(1)
	}
	// Begin autoscaling.
	scaler.Run()
}
