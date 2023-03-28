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
	"os"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/clock"

	"github.com/kubernetes-sigs/cluster-proportional-autoscaler/cmd/cluster-proportional-autoscaler/options"
	"github.com/kubernetes-sigs/cluster-proportional-autoscaler/pkg/autoscaler/controller"
	"github.com/kubernetes-sigs/cluster-proportional-autoscaler/pkg/autoscaler/controller/plugin"
	"github.com/kubernetes-sigs/cluster-proportional-autoscaler/pkg/autoscaler/k8sclient"

	"github.com/golang/glog"
)

// AutoScaler determines the number of replicas to run
type AutoScaler struct {
	k8sClient           k8sclient.K8sClient
	controller          controller.Controller
	configMapName       string
	defaultParams       map[string]string
	pollPeriod          time.Duration
	clock               clock.WithTicker
	stopCh              chan struct{}
	readyCh             chan<- struct{} // For testing.
	healthServer        HealthServer
	lastPollCycleHealth *healthInfo
	maxSyncFailures     int
	exitFn              func()
}

// NewAutoScaler returns a new AutoScaler
func NewAutoScaler(c *options.AutoScalerConfig) (*AutoScaler, error) {
	newK8sClient, err := k8sclient.NewK8sClient(c.Namespace, c.Target, c.NodeLabels)
	if err != nil {
		return nil, err
	}
	healthInfo := newHealthInfo()
	healthServer := httpHealthServer{lastPollCycleHealth: healthInfo}
	return &AutoScaler{
		k8sClient:           newK8sClient,
		configMapName:       c.ConfigMap,
		defaultParams:       c.DefaultParams,
		pollPeriod:          time.Second * time.Duration(c.PollPeriodSeconds),
		clock:               clock.RealClock{},
		stopCh:              make(chan struct{}),
		readyCh:             make(chan struct{}, 1),
		lastPollCycleHealth: healthInfo,
		healthServer:        &healthServer,
		maxSyncFailures:     c.MaxSyncFailures,
		exitFn:              func() { os.Exit(1) },
	}, nil
}

// Run periodically counts the number of nodes and cores, estimates the expected
// number of replicas, compares them to the actual replicas, and
// updates the target resource with the expected replicas if necessary.
func (s *AutoScaler) Run() {
	ticker := s.clock.NewTicker(s.pollPeriod)
	s.readyCh <- struct{}{} // For testing.

	go s.healthServer.Start()
	// Don't wait for ticker and execute pollAPIServer() for the first time.
	s.tryPollAPIServer()

	for {
		select {
		case <-ticker.C():
			s.tryPollAPIServer()
		case <-s.stopCh:
			return
		}
	}
}

func (s *AutoScaler) tryPollAPIServer() {
	err := s.pollAPIServer()
	attempts := s.lastPollCycleHealth.setLastPollError(err)
	// if we've tried polling the apiserver more times than allowed
	if s.maxSyncFailures > 0 && attempts == s.maxSyncFailures {
		glog.Errorf("Maximum number of api server polling attempts (%d) have been reached. Exiting application.", s.maxSyncFailures)
		s.exitFn()
	}
}

func (s *AutoScaler) pollAPIServer() error {
	// Query the apiserver for the cluster status --- number of nodes and cores
	clusterStatus, err := s.k8sClient.GetClusterStatus()
	if err != nil {
		glog.Errorf("Error while getting cluster status: %v", err)
		return err
	}
	glog.V(4).Infof("Total nodes %5d, schedulable nodes: %5d", clusterStatus.TotalNodes, clusterStatus.SchedulableNodes)
	glog.V(4).Infof("Total cores %5d, schedulable cores: %5d", clusterStatus.TotalCores, clusterStatus.SchedulableCores)

	// Sync autoscaler ConfigMap with apiserver
	configMap, err := s.syncConfigWithServer()
	if err != nil || configMap == nil {
		glog.Errorf("Error syncing configMap with apiserver: %v", err)
		return err
	}

	// Only sync updated ConfigMap or before controller is set.
	if s.controller == nil || configMap.ObjectMeta.ResourceVersion != s.controller.GetParamsVersion() {
		// Ensure corresponding controller type and scaling params.
		s.controller, err = plugin.EnsureController(s.controller, configMap)
		if err != nil || s.controller == nil {
			glog.Errorf("Error ensuring controller: %v", err)
			return err
		}
	}

	// Query the controller for the expected replicas number
	expReplicas, err := s.controller.GetExpectedReplicas(clusterStatus)
	if err != nil {
		glog.Errorf("Error calculating expected replicas number: %v", err)
		return err
	}
	glog.V(4).Infof("Expected replica count: %3d", expReplicas)

	// Update resource target with expected replicas.
	_, err = s.k8sClient.UpdateReplicas(expReplicas)
	if err != nil {
		glog.Errorf("Update failure: %s", err)
	}
	return err
}

func (s *AutoScaler) syncConfigWithServer() (*v1.ConfigMap, error) {
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
