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

package k8sclient

import (
	"context"
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGetTarget(t *testing.T) {
	testCases := []struct {
		target   string
		expKind  string
		expName  string
		expError bool
	}{
		{
			"deployment/anything",
			"deployment",
			"anything",
			false,
		},
		{
			"replicationcontroller/anotherthing",
			"replicationcontroller",
			"anotherthing",
			false,
		},
		{
			"replicationcontroller",
			"",
			"",
			true,
		},
		{
			"replicaset/anything/what",
			"",
			"",
			true,
		},
	}

	for _, tc := range testCases {
		res, err := getTarget(tc.target)
		if err != nil && !tc.expError {
			t.Errorf("Expect no error, got error for target: %v", tc.target)
			continue
		} else if err == nil && tc.expError {
			t.Errorf("Expect error, got no error for target: %v", tc.target)
			continue
		}
		if res.kind != tc.expKind || res.name != tc.expName {
			t.Errorf("Expect kind: %v, name: %v\ngot kind: %v, name: %v", tc.expKind, tc.expName, res.kind, res.name)
		}
	}
}

func TestGetScaleTargets(t *testing.T) {
	testCases := []struct {
		target          string
		expScaleTargets *scaleTargets
		expError        bool
	}{
		{
			"deployment/anything",
			&scaleTargets{
				targets: []target{
					{kind: "deployment", name: "anything"},
				},
			},
			false,
		},
		{
			"deployment/first,deployment/second",
			&scaleTargets{
				targets: []target{
					{kind: "deployment", name: "first"},
					{kind: "deployment", name: "second"},
				},
			},
			false,
		},
		{
			"deployment/first, deployment/second",
			&scaleTargets{
				targets: []target{
					{kind: "deployment", name: "first"},
					{kind: "deployment", name: "second"},
				},
			},
			false,
		},
		{
			"deployment/first deployment/second",
			&scaleTargets{
				targets: []target{
					{kind: "deployment", name: "first"},
					{kind: "deployment", name: "second"},
				},
			},
			true,
		},
	}

	for _, tc := range testCases {
		res, err := getScaleTargets(tc.target, "default")
		if err != nil && !tc.expError {
			t.Errorf("Expect no error, got error for target: %v", tc.target)
			continue
		} else if err == nil && tc.expError {
			t.Errorf("Expect error, got no error for target: %v", tc.target)
			continue
		}
		if len(res.targets) != len(tc.expScaleTargets.targets) && !tc.expError {
			t.Errorf("Expected targets vs resulted targets should be the same length: %v vs %v", len(tc.expScaleTargets.targets), len(res.targets))
			continue
		}
		for i, resTarget := range res.targets {
			if resTarget.kind != tc.expScaleTargets.targets[i].kind ||
				resTarget.name != tc.expScaleTargets.targets[i].name {
				t.Errorf("Expect kind: %v, name: %v\ngot kind: %v, name: %v", tc.expScaleTargets.targets[i].kind,
					tc.expScaleTargets.targets[i].name, resTarget.kind, resTarget.name)
			}
		}
	}
}

func TestNewK8sClient(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Create the test nodes beforehand.
	nodeLabels := "app=autoscaler"
	q1, _ := resource.ParseQuantity("1000m")
	q2, _ := resource.ParseQuantity("2000m")
	q3, _ := resource.ParseQuantity("3000m")
	q4, _ := resource.ParseQuantity("4000m")
	testNode1 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
			Labels: map[string]string{
				"app": "autoscaler",
			},
			Annotations: map[string]string{
				"eating-memory": "a-lot",
				"oom":           "on-the-way",
			},
		},
		Spec: v1.NodeSpec{
			Unschedulable: false,
			PodCIDR:       "10.0.1.0/24",
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				v1.ResourceCPU: q1,
			},
			Phase: v1.NodeRunning,
		},
	}
	testNode2 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-2",
			Labels: map[string]string{
				"app": "autoscaler",
			},
			Annotations: map[string]string{
				"eating-memory": "a-lot",
				"oom":           "on-the-way",
			},
		},
		Spec: v1.NodeSpec{
			Unschedulable: false,
			PodCIDR:       "10.0.2.0/24",
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				v1.ResourceCPU: q2,
			},
			Phase: v1.NodeRunning,
		},
	}
	// testNode3 has Unschedulable set to true.
	testNode3 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-3",
			Labels: map[string]string{
				"app": "autoscaler",
			},
			Annotations: map[string]string{
				"eating-memory": "a-lot",
				"oom":           "on-the-way",
			},
		},
		Spec: v1.NodeSpec{
			Unschedulable: true,
			PodCIDR:       "10.0.3.0/24",
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				v1.ResourceCPU: q3,
			},
			Phase: v1.NodeRunning,
		},
	}
	// testNode4 uses labels that don't match.
	testNode4 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-4",
			Labels: map[string]string{
				"app": "something-else",
			},
			Annotations: map[string]string{
				"eating-memory": "a-lot",
				"oom":           "on-the-way",
			},
		},
		Spec: v1.NodeSpec{
			Unschedulable: false,
			PodCIDR:       "10.0.4.0/24",
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				v1.ResourceCPU: q4,
			},
			Phase: v1.NodeRunning,
		},
	}
	_, err := client.CoreV1().Nodes().Create(context.Background(), testNode1, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CoreV1().Nodes().Create(context.Background(), testNode2, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CoreV1().Nodes().Create(context.Background(), testNode3, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CoreV1().Nodes().Create(context.Background(), testNode4, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	k8sClient, err := NewK8sClient(client, "test-namespace", "deployment/test-target", nodeLabels)
	if err != nil {
		t.Fatal(err)
	}
	status, err := k8sClient.GetClusterStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.TotalNodes != 3 {
		t.Errorf("status.TotalNodes=%v, want 3", status.TotalNodes)
	}
	if status.SchedulableNodes != 2 {
		t.Errorf("status.SchedulableNodes=%v, want 2", status.SchedulableNodes)
	}
	if status.TotalCores != 6 {
		t.Errorf("status.TotalCores=%v, want 6", status.TotalCores)
	}
	if status.SchedulableCores != 3 {
		t.Errorf("status.SchedulableCore=%v, want 3", status.SchedulableCores)
	}
}

func TestGetTrimmedNodeClients(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Create the test node beforehand.
	q1, _ := resource.ParseQuantity("1000m")
	testNode1 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
			Labels: map[string]string{
				"app": "autoscaler",
			},
			Annotations: map[string]string{
				"eating-memory": "a-lot",
				"oom":           "on-the-way",
			},
		},
		Spec: v1.NodeSpec{
			Unschedulable: false,
			PodCIDR:       "10.0.1.0/24",
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				v1.ResourceCPU: q1,
			},
			Phase: v1.NodeRunning,
		},
	}
	_, err := client.CoreV1().Nodes().Create(context.Background(), testNode1, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Start the informer.
	labelOptions := informers.WithTweakListOptions(func(opts *metav1.ListOptions) {})
	factory, nodelister, err := getTrimmedNodeClients(client, labelOptions)
	if err != nil {
		t.Fatal(err)
	}
	stopCh := make(chan struct{})
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)

	// Now check if all of objectMeta, spec and status have unneeded fields trimmed.
	nodes, err := nodelister.List(labels.NewSelector())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes)=%v, want 1", len(nodes))
	}
	node := nodes[0]
	if node.Annotations != nil {
		t.Errorf("node.ObjectMeta is not trimmed. Got %+v", node.ObjectMeta)
	}
	if node.Spec.PodCIDR != "" {
		t.Errorf("node.Spec is not trimmed. Got %+v", node.Spec)
	}
	if node.Status.Phase != "" {
		t.Errorf("node.Status is not trimmed. Got %+v", node.Status)
	}
}
