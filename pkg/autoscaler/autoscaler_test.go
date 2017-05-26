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
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	apiv1 "k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/util/clock"

	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/controller/laddercontroller"
	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/controller/linearcontroller"
	"github.com/kubernetes-incubator/cluster-proportional-autoscaler/pkg/autoscaler/k8sclient"
)

func TestRun(t *testing.T) {
	testConfigMap := apiv1.ConfigMap{
		Data: make(map[string]string),
	}
	testConfigMap.ObjectMeta.ResourceVersion = `1`
	testConfigMap.Data[laddercontroller.ControllerType] =
		`{
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

	fakeClock := clock.NewFakeClock(time.Now())
	fakePollPeriod := 5 * time.Second
	fakeConfigMapName := "fake-cluster-proportional-autoscaler-params"
	autoScaler := &AutoScaler{
		k8sClient:     &mockK8s,
		controller:    laddercontroller.NewLadderController(),
		clock:         fakeClock,
		pollPeriod:    fakePollPeriod,
		configMapName: fakeConfigMapName,
		stopCh:        make(chan struct{}),
		readyCh:       make(chan<- struct{}, 1),
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
	mockK8s.ConfigMap.Data[laddercontroller.ControllerType] =
		`{
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
	mockK8s.ConfigMap.Data[linearcontroller.ControllerType] =
		`{
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

func waitForReplicasNumberSatisfy(t *testing.T, mockK8s *k8sclient.MockK8sClient, replicas int) error {
	return wait.Poll(50*time.Millisecond, 3*time.Second, func() (done bool, err error) {
		if mockK8s.NumOfReplicas != replicas {
			t.Logf("Error number of replicas, expected: %d, got %d\n", replicas, mockK8s.NumOfReplicas)
			return false, nil
		}
		return true, nil
	})
}
