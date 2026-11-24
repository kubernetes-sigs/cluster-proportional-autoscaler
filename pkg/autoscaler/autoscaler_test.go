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
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/kubernetes-sigs/cluster-proportional-autoscaler/pkg/autoscaler/controller/laddercontroller"
	"github.com/kubernetes-sigs/cluster-proportional-autoscaler/pkg/autoscaler/controller/linearcontroller"
	"github.com/kubernetes-sigs/cluster-proportional-autoscaler/pkg/autoscaler/k8sclient"
)

const validKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: local
  cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
contexts:
- name: local
  context:
    cluster: local
    user: local
users:
- name: local
  user:
    token: test-token
current-context: local
`

func TestRun(t *testing.T) {
	testConfigMap := v1.ConfigMap{
		Data: make(map[string]string),
	}
	testConfigMap.ObjectMeta.ResourceVersion = `1`
	testConfigMap.Data[laddercontroller.ControllerType] = `{
			"coresToReplicas":
			[
				[1, 1],
				[2, 2],
				[3, 3],
				[512, 5],
				[1024, 7],
				[2048, 10],
				[4096, 15],
				[8192, 20],
				[12288, 30],
				[16384, 40],
				[20480, 50],
				[24576, 60],
				[28672, 70],
				[32768, 80],
				[65535, 100]
			],
			"nodesToReplicas":
			[
				[ 1,1 ],
				[ 2,2 ]
			]
		}`
	mockK8s := k8sclient.MockK8sClient{
		NumOfNodes:    0,
		NumOfCores:    0,
		NumOfReplicas: 0,
		ConfigMap:     &testConfigMap,
	}

	fakeClock := testingclock.NewFakeClock(time.Now())
	fakePollPeriod := 5 * time.Second
	fakeConfigMapName := "fake-cluster-proportional-autoscaler-params"
	autoScaler := &AutoScaler{
		k8sClient:           &mockK8s,
		controller:          laddercontroller.NewLadderController(),
		clock:               fakeClock,
		pollPeriod:          fakePollPeriod,
		configMapName:       fakeConfigMapName,
		stopCh:              make(chan struct{}),
		readyCh:             make(chan<- struct{}, 1),
		lastPollCycleHealth: newHealthInfo(),
		healthServer:        mockHealthServer{},
	}

	go autoScaler.Run()
	defer close(autoScaler.stopCh)

	t.Logf("Scenario: cluster size changing\n")
	t.Logf("Wait for the number of replicas be scaled to 1 even no node and no core)\n")
	if err := waitForReplicasNumberSatisfy(t, &mockK8s, 1); err != nil {
		t.Fatalf("Timeout waiting for condition: %v", err)
	}

	mockK8s.NumOfCores = 800
	mockK8s.NumOfNodes = 1
	fakeClock.Step(fakePollPeriod)
	t.Logf("Wait for the number of replicas be scaled to 5 when there are 800 cores and 1 node\n")
	if err := waitForReplicasNumberSatisfy(t, &mockK8s, 5); err != nil {
		t.Fatalf("Timeout waiting for condition: %v", err)
	}

	mockK8s.NumOfCores = 1
	mockK8s.NumOfNodes = 3
	fakeClock.Step(fakePollPeriod)
	t.Logf("Wait for the number of replicas be scaled to 2 when there are 1 cores and 3 node\n")
	if err := waitForReplicasNumberSatisfy(t, &mockK8s, 2); err != nil {
		t.Fatalf("Timeout waiting for condition: %v", err)
	}

	mockK8s.NumOfCores = 200000
	mockK8s.NumOfNodes = 50000
	fakeClock.Step(fakePollPeriod)
	t.Logf("Wait for the number of replicas be scaled to 100 when there are 200000 cores and 50000 node\n")
	if err := waitForReplicasNumberSatisfy(t, &mockK8s, 100); err != nil {
		t.Fatalf("Timeout waiting for condition: %v", err)
	}

	t.Logf("Scenario: ConfigMap is changed\n")
	mockK8s.ConfigMap.Data[laddercontroller.ControllerType] = `{
			"coresToReplicas":
			[
				[1, 1],
				[2, 2],
				[3, 4],
				[512, 5],
				[1024, 7],
				[2048, 10],
				[4096, 15],
				[8192, 20],
				[12288, 30],
				[16384, 40],
				[20480, 50],
				[24576, 60],
				[28672, 70],
				[32768, 80],
				[65535, 200]
			],
			"nodesToReplicas":
			[
				[ 1,1 ],
				[ 2,2 ]
			]
		}`
	mockK8s.ConfigMap.ObjectMeta.ResourceVersion = `2`

	fakeClock.Step(fakePollPeriod)
	t.Logf("Wait for the number of replicas be scaled to 200 with new configuration)\n")
	if err := waitForReplicasNumberSatisfy(t, &mockK8s, 200); err != nil {
		t.Fatalf("Timeout waiting for condition: %v", err)
	}

	mockK8s.NumOfCores = 500
	mockK8s.NumOfNodes = 100
	fakeClock.Step(fakePollPeriod)
	t.Logf("Wait for the number of replicas be scaled to 4 when there are 500 cores and 100 node\n")
	if err := waitForReplicasNumberSatisfy(t, &mockK8s, 4); err != nil {
		t.Fatalf("Timeout waiting for condition: %v", err)
	}

	t.Logf("Scenario: ConfigMap is missing and later appears again\n")
	mockK8s.ConfigMap.ObjectMeta.ResourceVersion = ""
	fakeClock.Step(fakePollPeriod)
	t.Logf("And cluster size changed in between\n")
	mockK8s.NumOfCores = 2000
	mockK8s.NumOfNodes = 400
	mockK8s.ConfigMap.ObjectMeta.ResourceVersion = "3"
	fakeClock.Step(fakePollPeriod)
	t.Logf("Wait for the number of replicas be scaled to 7 when there are 2000 cores and 400 node\n")
	if err := waitForReplicasNumberSatisfy(t, &mockK8s, 7); err != nil {
		t.Fatalf("Timeout waiting for condition: %v", err)
	}

	t.Logf("Scenario: Switch control mode on the fly\n")
	delete(mockK8s.ConfigMap.Data, laddercontroller.ControllerType)
	mockK8s.ConfigMap.Data[linearcontroller.ControllerType] = `{
			"coresPerReplica": 100,
			"nodesPerReplica": 10,
			"min": 1,
			"max": 100
		}`
	mockK8s.ConfigMap.ObjectMeta.ResourceVersion = `4`

	fakeClock.Step(fakePollPeriod)
	t.Logf("Wait for the number of replicas be scaled to 40 with new configuration)\n")
	if err := waitForReplicasNumberSatisfy(t, &mockK8s, 40); err != nil {
		t.Fatalf("Timeout waiting for condition: %v", err)
	}

	mockK8s.NumOfCores = 1600
	mockK8s.NumOfNodes = 100
	fakeClock.Step(fakePollPeriod)
	t.Logf("Wait for the number of replicas be scaled to 16 when there are 1600 cores and 100 node\n")
	if err := waitForReplicasNumberSatisfy(t, &mockK8s, 16); err != nil {
		t.Fatalf("Timeout waiting for condition: %v", err)
	}

	mockK8s.NumOfCores = 100000
	mockK8s.NumOfNodes = 20000
	fakeClock.Step(fakePollPeriod)
	t.Logf("Wait for the number of replicas be scaled to 100 when there are 100000 cores and 20000 node\n")
	if err := waitForReplicasNumberSatisfy(t, &mockK8s, 100); err != nil {
		t.Fatalf("Timeout waiting for condition: %v", err)
	}
}

func TestRun_MaxRetries(t *testing.T) {
	const maxRetries = 3
	mockK8s := k8sclient.MockK8sClient{
		NumOfNodes:    0,
		NumOfCores:    0,
		NumOfReplicas: 0,
		FetchConfigMapFn: func(namespace, configmap string) (*v1.ConfigMap, error) {
			return nil, errors.New("mocked error")
		},
	}
	var exitFnCalled bool
	fakeClock := testingclock.NewFakeClock(time.Now())
	fakePollPeriod := 5 * time.Second
	fakeConfigMapName := "fake-cluster-proportional-autoscaler-params"
	readyCh := make(chan struct{})
	stopCh := make(chan struct{})
	autoScaler := &AutoScaler{
		k8sClient:           &mockK8s,
		controller:          laddercontroller.NewLadderController(),
		clock:               fakeClock,
		pollPeriod:          fakePollPeriod,
		configMapName:       fakeConfigMapName,
		stopCh:              stopCh,
		readyCh:             readyCh,
		lastPollCycleHealth: newHealthInfo(),
		maxSyncFailures:     maxRetries,
		exitFn: func() {
			exitFnCalled = true
			// shutdown autoScaler via stopCh to stop
			close(stopCh)
		},
		healthServer: mockHealthServer{},
	}
	go autoScaler.Run()
	<-readyCh
	for i := 0; i < maxRetries; i++ {
		fakeClock.Step(fakePollPeriod)
		_ = wait.PollUntilContextTimeout(context.TODO(), 50*time.Millisecond, 3*time.Second, false, func(ctx context.Context) (done bool, err error) {
			return true, nil
		})
	}

	if !exitFnCalled {
		t.Fatalf("Did not exit")
	}
	if autoScaler.lastPollCycleHealth.failedCount > autoScaler.maxSyncFailures {
		t.Fatalf("scaler ran more times than allowed")
	}
}

// No t.Parallel(): modifies global state
func TestBuidKubeConfig(t *testing.T) {
	tmpDir := t.TempDir()
	path := writeKubeConfig(t, tmpDir, validKubeconfig)
	isolateKubeEnv(t, filepath.Join(t.TempDir(), "does-not-exist"))

	cfg, err := buildKubeconfig(path)
	if err != nil {
		t.Fatalf("failed to build kubeconfig %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil rest.Config")
	}
	if cfg.Host != "https://127.0.0.1:6443" {
		t.Errorf("unexpected host: got %q, expected %q", cfg.Host, "https://127.0.0.1:6443")
	}
	if cfg.BearerToken != "test-token" {
		t.Errorf("unexpected bearer token: got %q, expected %q", cfg.BearerToken, "test-token")
	}
}

// isolateKubeEnv prevents tests from accidently picking in-cluster service account
// or the developers real kubeconfig
func isolateKubeEnv(t *testing.T, kubeConfigEnv string) {
	t.Helper()
	t.Setenv("KUBECONFIG", kubeConfigEnv)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
}

// writeKubeonfig writes the provided kubeconfig to provided path
func writeKubeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write kubeconfig: %v", err)
	}
	return path
}

func waitForReplicasNumberSatisfy(t *testing.T, mockK8s *k8sclient.MockK8sClient, replicas int) error {
	return wait.PollUntilContextTimeout(context.TODO(), 50*time.Millisecond, 3*time.Second, false, func(ctx context.Context) (done bool, err error) {
		if mockK8s.NumOfReplicas != replicas {
			t.Logf("Error number of replicas, expected: %d, got %d\n", replicas, mockK8s.NumOfReplicas)
			return false, nil
		}
		return true, nil
	})
}

type mockHealthServer struct{}

func (s mockHealthServer) Start() {
}

func TestPollAPIServer_ConfigMapUpdateModeAndUseDefaults(t *testing.T) {
	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "my-autoscaler", ResourceVersion: "1"},
		Data: map[string]string{
			linearcontroller.ControllerType: `{"coresPerReplica": 0, "nodesPerReplica": 10, "min": 1}`,
		},
	}
	mockK8s := &k8sclient.MockK8sClient{
		NumOfNodes: 100,
		NumOfCores: 400,
		ConfigMap:  cm,
	}
	s := &AutoScaler{
		k8sClient:           mockK8s,
		defaultParams:       map[string]string{linearcontroller.ControllerType: `{"coresPerReplica": 0, "nodesPerReplica": 25, "min": 1}`},
		clock:               testingclock.NewFakeClock(time.Now()),
		pollPeriod:          10 * time.Second,
		configMapName:       "my-autoscaler",
		stopCh:              make(chan struct{}),
		readyCh:             make(chan struct{}, 1),
		lastPollCycleHealth: newHealthInfo(),
		healthServer:        mockHealthServer{},
	}

	// Auto mode reconciles to 100 / 10 = 10 replicas.
	if err := s.pollAPIServer(); err != nil {
		t.Fatalf("Auto poll failed: %v", err)
	}
	if mockK8s.NumOfReplicas != 10 {
		t.Fatalf("Auto replicas = %d, want 10", mockK8s.NumOfReplicas)
	}

	// Off mode preserves manual replica edits across cluster size changes and extra keys.
	cm.ResourceVersion = "2"
	cm.Data["updateMode"] = "Off"
	cm.Data["extra"] = "ignored-while-paused"
	mockK8s.NumOfReplicas = 42
	mockK8s.NumOfNodes = 300
	if err := s.pollAPIServer(); err != nil {
		t.Fatalf("Off poll failed: %v", err)
	}
	if mockK8s.NumOfReplicas != 42 || !s.inUpdateModeOff {
		t.Fatalf("Off mode: replicas=%d inUpdateModeOff=%v, want 42, true", mockK8s.NumOfReplicas, s.inUpdateModeOff)
	}

	// Returning to Auto with useDefaults=true overrides differing linear with --default-params (300 / 25 = 12) and records warning version.
	cm.ResourceVersion = "3"
	delete(cm.Data, "extra")
	cm.Data["updateMode"] = "Auto"
	cm.Data["useDefaults"] = "true"
	if err := s.pollAPIServer(); err != nil {
		t.Fatalf("Auto+useDefaults poll failed: %v", err)
	}
	if mockK8s.NumOfReplicas != 12 || s.inUpdateModeOff || s.lastUseDefaultsWarnVer != "3" {
		t.Fatalf("Auto+useDefaults: replicas=%d inUpdateModeOff=%v warnVer=%q, want 12, false, %q",
			mockK8s.NumOfReplicas, s.inUpdateModeOff, s.lastUseDefaultsWarnVer, "3")
	}

	// Setting useDefaults=false switches back to ConfigMap linear (300 / 10 = 30) and clears warning state.
	cm.ResourceVersion = "4"
	cm.Data["useDefaults"] = "false"
	if err := s.pollAPIServer(); err != nil {
		t.Fatalf("Auto+useDefaults=false poll failed: %v", err)
	}
	if mockK8s.NumOfReplicas != 30 || s.lastUseDefaultsWarnVer != "" {
		t.Fatalf("Auto+useDefaults=false: replicas=%d warnVer=%q, want 30, empty",
			mockK8s.NumOfReplicas, s.lastUseDefaultsWarnVer)
	}

	// Invalid updateMode returns an error without modifying replicas.
	cm.ResourceVersion = "5"
	cm.Data["updateMode"] = "Invalid"
	if err := s.pollAPIServer(); err == nil {
		t.Fatal("expected error for invalid updateMode")
	}
	if mockK8s.NumOfReplicas != 30 {
		t.Fatalf("replicas changed on invalid updateMode: got %d, want 30", mockK8s.NumOfReplicas)
	}
}

type spyHealthServer struct {
	once      sync.Once
	startedCh chan struct{}
}

func (s *spyHealthServer) Start() {
	s.once.Do(func() { close(s.startedCh) })
}

func startLeaderAutoScaler(
	t *testing.T,
	clientset *fake.Clientset,
	id, leaseName string,
	initFn func() (k8sclient.K8sClient, error),
	exitCh chan struct{},
	health HealthServer,
) (readyCh chan struct{}, stopFn func()) {
	t.Helper()
	stopCh := make(chan struct{})
	readyCh = make(chan struct{}, 1)
	if health == nil {
		health = mockHealthServer{}
	}
	var exitOnce sync.Once
	s := &AutoScaler{
		k8sClientInitFn: initFn,
		leaderElection: &leaderelection.LeaderElectionConfig{
			Lock: &resourcelock.LeaseLock{
				LeaseMeta:  metav1.ObjectMeta{Name: leaseName, Namespace: "kube-system"},
				Client:     clientset.CoordinationV1(),
				LockConfig: resourcelock.ResourceLockConfig{Identity: id},
			},
			ReleaseOnCancel: true,
			LeaseDuration:   2 * time.Second,
			RenewDeadline:   600 * time.Millisecond,
			RetryPeriod:     50 * time.Millisecond,
		},
		clock:               testingclock.NewFakeClock(time.Now()),
		pollPeriod:          10 * time.Second,
		configMapName:       leaseName,
		stopCh:              stopCh,
		readyCh:             readyCh,
		lastPollCycleHealth: newHealthInfo(),
		healthServer:        health,
		exitFn: func() {
			if exitCh == nil {
				t.Errorf("unexpected exitFn call for replica %s", id)
				return
			}
			exitOnce.Do(func() { close(exitCh) })
		},
	}

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		s.Run()
	}()

	stopFn = func() {
		s.Stop()
		select {
		case <-doneCh:
		case <-time.After(5 * time.Second):
			t.Errorf("timed out waiting for replica %s to stop", id)
		}
	}
	t.Cleanup(stopFn)
	return readyCh, stopFn
}

func newFakeMockK8sClient() *k8sclient.MockK8sClient {
	return &k8sclient.MockK8sClient{
		NumOfNodes: 10,
		NumOfCores: 40,
		ConfigMap: &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{ResourceVersion: "1"},
			Data:       map[string]string{linearcontroller.ControllerType: `{"coresPerReplica": 10, "nodesPerReplica": 10, "min": 1}`},
		},
	}
}

func TestRun_LeaderElectionStandbyIdleAndFailover(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	var standbyStarted atomic.Bool
	var standbyPolls atomic.Int32
	standbyPolledCh := make(chan struct{})
	var polledOnce sync.Once
	clientset.PrependReactor("get", "leases", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if standbyStarted.Load() && standbyPolls.Add(1) >= 2 {
			polledOnce.Do(func() { close(standbyPolledCh) })
		}
		return false, nil, nil
	})

	var init1, init2 atomic.Int32
	ready1, stop1 := startLeaderAutoScaler(t, clientset, "replica-1", "test-lease", func() (k8sclient.K8sClient, error) {
		init1.Add(1)
		return newFakeMockK8sClient(), nil
	}, nil, nil)

	select {
	case <-ready1:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for replica-1 to acquire leadership")
	}
	if got := init1.Load(); got != 1 {
		t.Fatalf("replica-1 init count = %d, want 1", got)
	}

	standbyHealth := &spyHealthServer{startedCh: make(chan struct{})}
	standbyStarted.Store(true)
	ready2, _ := startLeaderAutoScaler(t, clientset, "replica-2", "test-lease", func() (k8sclient.K8sClient, error) {
		init2.Add(1)
		return newFakeMockK8sClient(), nil
	}, nil, standbyHealth)

	select {
	case <-standbyHealth.startedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for standby health server to start")
	}
	select {
	case <-standbyPolledCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for standby replica to poll held lease")
	}
	if got := init2.Load(); got != 0 {
		t.Fatalf("standby replica initialized k8sClient %d times while not leader", got)
	}
	select {
	case <-ready2:
		t.Fatal("standby replica started poll loop while replica-1 held lease")
	default:
	}

	stop1()
	select {
	case <-ready2:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for replica-2 to take over after replica-1 stopped")
	}
	if got := init2.Load(); got != 1 {
		t.Fatalf("replica-2 init count after failover = %d, want 1", got)
	}
}

func TestRun_LeaderElectionExitPaths(t *testing.T) {
	t.Run("init failure releases lease and exits", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		exitCh := make(chan struct{})
		_, stop := startLeaderAutoScaler(t, clientset, "replica-fail", "init-fail-lease", func() (k8sclient.K8sClient, error) {
			return nil, errors.New("apiserver unreachable")
		}, exitCh, nil)

		select {
		case <-exitCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for exitFn after k8sClient init failure")
		}
		stop()

		lease, err := clientset.CoordinationV1().Leases("kube-system").Get(context.Background(), "init-fail-lease", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("getting lease: %v", err)
		}
		if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" {
			t.Errorf("expected lease holder to be released after init failure, got %q", *lease.Spec.HolderIdentity)
		}
	})

	t.Run("lease renewal loss exits", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		var failRenewals atomic.Bool
		clientset.PrependReactor("update", "leases", func(action k8stesting.Action) (bool, runtime.Object, error) {
			if failRenewals.Load() {
				return true, nil, errors.New("lease update rejected")
			}
			return false, nil, nil
		})

		exitCh := make(chan struct{})
		readyCh, _ := startLeaderAutoScaler(t, clientset, "replica-loss", "loss-lease", func() (k8sclient.K8sClient, error) {
			return newFakeMockK8sClient(), nil
		}, exitCh, nil)

		select {
		case <-readyCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for replica-loss to acquire lease")
		}
		failRenewals.Store(true)
		select {
		case <-exitCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for exitFn after lease renewal loss")
		}
	})

	t.Run("Stop releases lease cleanly without calling exitFn", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		stopCh := make(chan struct{})
		readyCh := make(chan struct{}, 1)
		var exitCalled atomic.Bool
		s := &AutoScaler{
			k8sClientInitFn: func() (k8sclient.K8sClient, error) {
				return newFakeMockK8sClient(), nil
			},
			leaderElection: &leaderelection.LeaderElectionConfig{
				Lock: &resourcelock.LeaseLock{
					LeaseMeta:  metav1.ObjectMeta{Name: "graceful-stop-lease", Namespace: "kube-system"},
					Client:     clientset.CoordinationV1(),
					LockConfig: resourcelock.ResourceLockConfig{Identity: "replica-stop"},
				},
				ReleaseOnCancel: true,
				LeaseDuration:   15 * time.Second,
				RenewDeadline:   10 * time.Second,
				RetryPeriod:     50 * time.Millisecond,
			},
			clock:               testingclock.NewFakeClock(time.Now()),
			pollPeriod:          10 * time.Second,
			configMapName:       "graceful-stop-lease",
			stopCh:              stopCh,
			readyCh:             readyCh,
			lastPollCycleHealth: newHealthInfo(),
			healthServer:        mockHealthServer{},
			exitFn:              func() { exitCalled.Store(true) },
		}

		doneCh := make(chan struct{})
		go func() {
			defer close(doneCh)
			s.Run()
		}()

		select {
		case <-readyCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for replica-stop to acquire lease")
		}

		// Stop must be idempotent and release the lease without invoking exitFn.
		s.Stop()
		s.Stop()

		select {
		case <-doneCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for Run() to return after Stop()")
		}
		if exitCalled.Load() {
			t.Fatal("exitFn should not be called on graceful Stop()")
		}
		lease, err := clientset.CoordinationV1().Leases("kube-system").Get(context.Background(), "graceful-stop-lease", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("getting lease: %v", err)
		}
		if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" {
			t.Errorf("expected lease holder to be released after Stop(), got %q", *lease.Spec.HolderIdentity)
		}
	})

	t.Run("Stop waits for in-flight poll before releasing lease", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		inPollCh := make(chan struct{})
		releasePollCh := make(chan struct{})
		var pollOnce sync.Once
		client := &blockingPollK8sClient{
			MockK8sClient: newFakeMockK8sClient(),
			onPoll: func() {
				pollOnce.Do(func() { close(inPollCh) })
				<-releasePollCh
			},
		}

		stopCh := make(chan struct{})
		var exitCalled atomic.Bool
		s := &AutoScaler{
			k8sClientInitFn: func() (k8sclient.K8sClient, error) {
				return client, nil
			},
			leaderElection: &leaderelection.LeaderElectionConfig{
				Name: "inflight-stop-lease",
				Lock: &resourcelock.LeaseLock{
					LeaseMeta:  metav1.ObjectMeta{Name: "inflight-stop-lease", Namespace: "kube-system"},
					Client:     clientset.CoordinationV1(),
					LockConfig: resourcelock.ResourceLockConfig{Identity: "replica-inflight"},
				},
				ReleaseOnCancel: true,
				LeaseDuration:   15 * time.Second,
				RenewDeadline:   10 * time.Second,
				RetryPeriod:     50 * time.Millisecond,
			},
			clock:               testingclock.NewFakeClock(time.Now()),
			pollPeriod:          10 * time.Second,
			configMapName:       "inflight-stop-lease",
			stopCh:              stopCh,
			lastPollCycleHealth: newHealthInfo(),
			healthServer:        mockHealthServer{},
			exitFn:              func() { exitCalled.Store(true) },
		}

		doneCh := make(chan struct{})
		go func() {
			defer close(doneCh)
			s.Run()
		}()

		select {
		case <-inPollCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for in-flight poll to start")
		}

		s.Stop()
		// Give the shutdown goroutine multiple retry periods to verify the Lease
		// stays held while the in-flight poll has not returned.
		time.Sleep(150 * time.Millisecond)
		lease, err := clientset.CoordinationV1().Leases("kube-system").Get(context.Background(), "inflight-stop-lease", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("getting lease during in-flight poll: %v", err)
		}
		if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != "replica-inflight" {
			t.Fatalf("lease released prematurely while poll was in flight: holder = %v", lease.Spec.HolderIdentity)
		}

		close(releasePollCh)
		select {
		case <-doneCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for Run() to return after poll completed")
		}
		if exitCalled.Load() {
			t.Fatal("exitFn should not be called on graceful Stop()")
		}
		lease, err = clientset.CoordinationV1().Leases("kube-system").Get(context.Background(), "inflight-stop-lease", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("getting lease after Stop(): %v", err)
		}
		if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" {
			t.Errorf("expected lease holder to be released after poll completed, got %q", *lease.Spec.HolderIdentity)
		}
	})
}

type blockingPollK8sClient struct {
	*k8sclient.MockK8sClient
	onPoll func()
}

func (b *blockingPollK8sClient) UpdateReplicas(expReplicas int32) error {
	b.onPoll()
	return b.MockK8sClient.UpdateReplicas(expReplicas)
}
