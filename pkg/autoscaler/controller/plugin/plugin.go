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

package plugin

import (
	"fmt"

	apiv1 "k8s.io/client-go/pkg/api/v1"

	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/controller"
	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/controller/laddercontroller"
	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/controller/linearcontroller"

	"github.com/golang/glog"
)

// EnsureController ensures controller type and scaling params
func EnsureController(cont controller.Controller, configMap *apiv1.ConfigMap) (controller.Controller, error) {
	// Expect only one entry, which uses the name of control mode as the key
	if len(configMap.Data) != 1 {
		return nil, fmt.Errorf("invalid configMap format, expected only one entry, got: %v", configMap.Data)
	}
	for mode := range configMap.Data {
		// No need to reset controller if control pattern doesn't change
		if cont != nil && mode == cont.GetControllerType() {
			break
		}
		switch mode {
		case laddercontroller.ControllerType:
			cont = laddercontroller.NewLadderController()
		case linearcontroller.ControllerType:
			cont = linearcontroller.NewLinearController()
		default:
			return nil, fmt.Errorf("not a supported control mode: %v", mode)
		}
		glog.V(1).Infof("Set control mode to %v", mode)
	}

	// Sync config with controller
	if err := cont.SyncConfig(configMap); err != nil {
		return nil, fmt.Errorf("Error syncing configMap with controller: %v", err)
	}
	return cont, nil
}
