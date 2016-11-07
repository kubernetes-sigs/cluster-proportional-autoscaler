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

package autoscaler

import (
	"fmt"
	"time"

	"k8s.io/client-go/1.4/pkg/util/clock"

	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/cmd/cluster-proportional-autoscaler/options"
	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/controller"
	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/controller/laddercontroller"
	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/controller/linearcontroller"
	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/k8sclient"

	"github.com/golang/glog"
)

// AutoScaler determines the number of replicas to run
type AutoScaler struct {
	k8sClient     k8sclient.K8sClient
	controller    controller.Controller
	configMapName string
	defaultParams map[string]string
	pollPeriod    time.Duration
	clock         clock.Clock
	stopCh        chan struct{}
	readyCh       chan<- struct{} // For testing.
}

func NewAutoScaler(c *options.AutoScalerConfig) (*AutoScaler, error) {
	var controller controller.Controller
	switch c.Mode {
	case laddercontroller.ControllerType:
		controller = laddercontroller.NewLadderController()
	case linearcontroller.ControllerType:
		controller = linearcontroller.NewLinearController()
	default:
		return nil, fmt.Errorf("not a supported control mode: %v", c.Mode)
	}
	newK8sClient, err := k8sclient.NewK8sClient(c.Namespace, c.Target)
	if err != nil {
		return nil, err
	}
	return &AutoScaler{
		k8sClient:     newK8sClient,
		controller:    controller,
		configMapName: c.ConfigMap,
		defaultParams: c.DefaultParams,
		pollPeriod:    time.Second * time.Duration(c.PollPeriodSeconds),
		clock:         clock.RealClock{},
		stopCh:        make(chan struct{}),
		readyCh:       make(chan struct{}, 1),
	}, nil
}

// Run periodically counts the number of nodes and cores, estimates the expected
// number of replicas, compares them to the actual replicas, and
// updates the target resource with the expected replicas if necessary.
func (s *AutoScaler) Run() {
	ticker := s.clock.Tick(s.pollPeriod)
	s.readyCh <- struct{}{} // For testing.

	// Don't wait for ticker and execute pollAPIServer() for the first time.
	s.pollAPIServer()

	for {
		select {
		case <-ticker:
			s.pollAPIServer()
		case <-s.stopCh:
			return
		}
	}
}

func (s *AutoScaler) pollAPIServer() {
	// Query the apiserver for the cluster status --- number of nodes and cores
	clusterStatus, err := s.k8sClient.GetClusterStatus()
	if err != nil {
		glog.Errorf("Error while getting cluster status: %v\n", err)
		return
	}
	glog.V(4).Infof("Total nodes %5d, schedulable nodes: %5d\n", clusterStatus.TotalNodes, clusterStatus.SchedulableNodes)
	glog.V(4).Infof("Total cores %5d, schedulable cores: %5d\n", clusterStatus.TotalCores, clusterStatus.SchedulableCores)

	// Sync autoscaler ConfigMap with apiserver
	configMap, err := s.syncConfigWithServer()
	if err != nil {
		glog.Errorf("Error syncing configMap with apiserver: %v", err)
		return
	}

	// Only sync updated ConfigMap
	if configMap.Version != s.controller.GetParamsVersion() {
		// Update the config
		if err := s.controller.SyncConfig(configMap); err != nil {
			glog.Errorf("error syncing configMap: %v\n", err)
			return
		}
	}

	// Query the controller for the expected replicas number
	expReplicas, err := s.controller.GetExpectedReplicas(clusterStatus)
	if err != nil {
		glog.Errorf("Error calculating expected replicas number: %v\n", err)
		return
	}
	glog.V(4).Infof("Expected replica count: %3d\n", expReplicas)

	// Update resource target with expected replicas.
	_, err = s.k8sClient.UpdateReplicas(expReplicas)
	if err != nil {
		glog.Errorf("Update failure: %s\n", err)
	}
}

func (s *AutoScaler) syncConfigWithServer() (*k8sclient.ConfigMap, error) {
	// Fetch autoscaler ConfigMap data from apiserver
	configMap, err := s.k8sClient.FetchConfigMap(s.k8sClient.GetNamespace(), s.configMapName)
	if err == nil {
		return configMap, nil
	}
	if s.defaultParams == nil {
		return nil, err
	}
	glog.V(0).Infof("ConfigMap not found: %v, will create one with default params", err)
	configMap, err = s.k8sClient.CreateConfigMap(s.k8sClient.GetNamespace(), s.configMapName, s.defaultParams)
	if err != nil {
		return nil, err
	}
	return configMap, nil
}
