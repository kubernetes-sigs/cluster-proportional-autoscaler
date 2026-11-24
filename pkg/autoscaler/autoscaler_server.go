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
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/utils/clock"

	"github.com/kubernetes-sigs/cluster-proportional-autoscaler/cmd/cluster-proportional-autoscaler/options"
	"github.com/kubernetes-sigs/cluster-proportional-autoscaler/pkg/autoscaler/controller"
	"github.com/kubernetes-sigs/cluster-proportional-autoscaler/pkg/autoscaler/controller/plugin"
	"github.com/kubernetes-sigs/cluster-proportional-autoscaler/pkg/autoscaler/k8sclient"

	"github.com/golang/glog"
)

// AutoScaler determines the number of replicas to run
type AutoScaler struct {
	k8sClient              k8sclient.K8sClient
	k8sClientInitFn        func() (k8sclient.K8sClient, error)
	leaderElection         *leaderelection.LeaderElectionConfig
	controller             controller.Controller
	inUpdateModeOff        bool
	lastUseDefaultsWarnVer string
	configMapName          string
	defaultParams          map[string]string
	pollPeriod             time.Duration
	clock                  clock.WithTicker
	stopCh                 chan struct{}
	stopOnce               sync.Once
	readyCh                chan<- struct{} // For testing.
	healthServer           HealthServer
	lastPollCycleHealth    *healthInfo
	maxSyncFailures        int
	exitFn                 func()
}

// NewAutoScaler returns a new AutoScaler
func NewAutoScaler(c *options.AutoScalerConfig) (*AutoScaler, error) {
	config, err := buildKubeconfig(c.KubeConfig)
	if err != nil {
		return nil, err
	}
	// Use protobufs for communication with apiserver.
	config.ContentType = "application/vnd.kubernetes.protobuf"
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	initK8sClient := func() (k8sclient.K8sClient, error) {
		return k8sclient.NewK8sClient(clientset, c.Namespace, c.Target, c.NodeLabels)
	}
	var (
		newK8sClient   k8sclient.K8sClient
		leaderElection *leaderelection.LeaderElectionConfig
	)
	if c.LeaderElect {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, err
		}
		var randBytes [4]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return nil, err
		}
		leaseName := c.LeaderElectResourceName
		if leaseName == "" {
			leaseName = c.ConfigMap
		}
		leaseNamespace := c.LeaderElectResourceNamespace
		if leaseNamespace == "" {
			leaseNamespace = c.Namespace
		}
		leaderElection = &leaderelection.LeaderElectionConfig{
			Name: leaseName,
			Lock: &resourcelock.LeaseLock{
				LeaseMeta: metav1.ObjectMeta{
					Name:      leaseName,
					Namespace: leaseNamespace,
				},
				Client: clientset.CoordinationV1(),
				LockConfig: resourcelock.ResourceLockConfig{
					Identity: fmt.Sprintf("%s_%x", hostname, randBytes),
				},
			},
			ReleaseOnCancel: true,
			LeaseDuration:   c.LeaderElectLeaseDuration,
			RenewDeadline:   c.LeaderElectRenewDeadline,
			RetryPeriod:     c.LeaderElectRetryPeriod,
		}
	} else {
		newK8sClient, err = initK8sClient()
		if err != nil {
			return nil, err
		}
	}
	healthInfo := newHealthInfo()
	healthServer := httpHealthServer{
		lastPollCycleHealth: healthInfo,
		bindAddress:         c.HealthzBindAddress,
		port:                c.HealthzPort,
	}
	return &AutoScaler{
		k8sClient:           newK8sClient,
		k8sClientInitFn:     initK8sClient,
		leaderElection:      leaderElection,
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

// buildKubeConfig returns a *rest.Config
func buildKubeconfig(kubeConfigPath string) (*rest.Config, error) {
	if kubeConfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	} else if err != rest.ErrNotInCluster {
		glog.V(1).Infof("in-cluster config is not usable, falling back to default loading rules: %v", err)
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, &clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		glog.V(1).Info("default kubeconfig loading rules failed: %v", err)
	}
	return cfg, err
}

// Run periodically counts the number of nodes and cores, estimates the expected
// number of replicas, compares them to the actual replicas, and
// updates the target resource with the expected replicas if necessary.
func (s *AutoScaler) Run() {
	go s.healthServer.Start()
	if s.leaderElection == nil {
		s.runPollLoop(s.stopCh, nil)
		return
	}
	s.runWithLeaderElection()
}

// Stop signals the autoscaler loop and leader election to shut down gracefully.
func (s *AutoScaler) Stop() {
	s.stopOnce.Do(func() {
		if s.stopCh != nil {
			close(s.stopCh)
		}
	})
}

func isClosed(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (s *AutoScaler) runWithLeaderElection() {
	// stopCtx is canceled only when s.Stop() triggers graceful shutdown, whereas
	// electCtx is also canceled on post-acquisition k8sClient init failure so
	// OnStoppedLeading can distinguish clean shutdown from fatal errors.
	stopCtx, cancelStop := context.WithCancel(context.Background())
	defer cancelStop()
	electCtx, cancelElect := context.WithCancel(stopCtx)
	defer cancelElect()

	var (
		leaderMu sync.Mutex // guards OnStartedLeading lifecycle
		pollMu   sync.Mutex // held while runPollLoop is actively reconciling
	)
	go func() {
		select {
		case <-s.stopCh:
			// Wait for any active poll cycle to finish before canceling electCtx so
			// ReleaseOnCancel releases the Lease only after guarded work completes.
			pollMu.Lock()
			pollMu.Unlock()
			cancelStop()
		case <-stopCtx.Done():
		}
	}()

	cfg := *s.leaderElection
	cfg.Callbacks = leaderelection.LeaderCallbacks{
		OnStartedLeading: func(leaderCtx context.Context) {
			leaderMu.Lock()
			defer leaderMu.Unlock()
			if leaderCtx.Err() != nil || isClosed(s.stopCh) {
				return
			}
			glog.V(0).Infof("Acquired leader lease %s as %s; starting autoscaling loop", cfg.Lock.Describe(), cfg.Lock.Identity())
			if s.k8sClient == nil {
				if s.k8sClientInitFn == nil {
					glog.Errorf("Failed to initialize k8sClient after acquiring leader lease: initializer is nil")
					cancelElect()
					return
				}
				client, err := s.k8sClientInitFn()
				if err != nil {
					glog.Errorf("Failed to initialize k8sClient after acquiring leader lease: %v", err)
					cancelElect()
					return
				}
				if client == nil {
					glog.Errorf("Failed to initialize k8sClient after acquiring leader lease: returned nil client")
					cancelElect()
					return
				}
				s.k8sClient = client
			}
			pollMu.Lock()
			defer pollMu.Unlock()
			if leaderCtx.Err() != nil || isClosed(s.stopCh) {
				return
			}
			s.runPollLoop(leaderCtx.Done(), s.stopCh)
		},
		OnStoppedLeading: func() {
			if stopCtx.Err() != nil {
				glog.V(0).Infof("Leader election stopped cleanly for %s", cfg.Lock.Identity())
				return
			}
			glog.Errorf("Stopped leading on lease %s; exiting", cfg.Lock.Describe())
			s.exitFn()
		},
		OnNewLeader: func(identity string) {
			if identity == "" || identity == cfg.Lock.Identity() {
				return
			}
			glog.V(0).Infof("New leader elected: %s", identity)
		},
	}

	elector, err := leaderelection.NewLeaderElector(cfg)
	if err != nil {
		glog.Errorf("Failed to create leader elector: %v", err)
		s.exitFn()
		return
	}
	elector.Run(electCtx)

	// Wait for any active OnStartedLeading goroutine to finish exiting before returning.
	leaderMu.Lock()
	leaderMu.Unlock()
}

func (s *AutoScaler) runPollLoop(stopCh, secondaryStopCh <-chan struct{}) {
	ticker := s.clock.NewTicker(s.pollPeriod)
	defer ticker.Stop()
	if s.readyCh != nil {
		s.readyCh <- struct{}{} // For testing.
	}

	// Don't wait for ticker and execute pollAPIServer() for the first time.
	s.tryPollAPIServer()

	for {
		select {
		case <-ticker.C():
			if isClosed(stopCh) || isClosed(secondaryStopCh) {
				return
			}
			s.tryPollAPIServer()
		case <-stopCh:
			return
		case <-secondaryStopCh:
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

	resolved, err := resolveConfigMap(configMap, s.defaultParams)
	if err != nil {
		glog.Errorf("Error parsing ConfigMap parameters: %v", err)
		return err
	}
	if resolved.paused {
		if !s.inUpdateModeOff {
			glog.V(0).Infof("ConfigMap %s/%s has updateMode=Off (version=%s); skipping replica reconciliation",
				configMap.Namespace, configMap.Name, configMap.ResourceVersion)
			s.inUpdateModeOff = true
		} else {
			glog.V(4).Infof("ConfigMap %s/%s has updateMode=Off (version=%s); skipping replica reconciliation",
				configMap.Namespace, configMap.Name, configMap.ResourceVersion)
		}
		s.lastUseDefaultsWarnVer = ""
		return nil
	}
	s.inUpdateModeOff = false
	if resolved.warning != "" {
		if configMap.ResourceVersion == "" || configMap.ResourceVersion != s.lastUseDefaultsWarnVer {
			glog.Warning(resolved.warning)
			s.lastUseDefaultsWarnVer = configMap.ResourceVersion
		}
	} else {
		s.lastUseDefaultsWarnVer = ""
	}
	controllerConfigMap := resolved.controllerCM

	// Only sync updated ConfigMap or before controller is set.
	if s.controller == nil || controllerConfigMap.ObjectMeta.ResourceVersion != s.controller.GetParamsVersion() {
		// Ensure corresponding controller type and scaling params.
		s.controller, err = plugin.EnsureController(s.controller, controllerConfigMap)
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
	err = s.k8sClient.UpdateReplicas(expReplicas)
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
