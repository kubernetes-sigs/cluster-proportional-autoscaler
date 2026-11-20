/*
Copyright 2026 The Kubernetes Authors All rights reserved.

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

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestResolveConfigMap(t *testing.T) {
	defaultLinear := map[string]string{"linear": `{"nodesPerReplica":32}`}
	defaultLadder := map[string]string{"ladder": `{"nodesToReplicas":[[1,1],[32,2]]}`}

	tests := []struct {
		name          string
		cm            *v1.ConfigMap
		defaultParams map[string]string
		wantPaused    bool
		wantWarning   bool
		wantKey       string
		wantPayload   string
		wantErr       bool
	}{
		{
			name:          "legacy ConfigMap without metadata ignores defaultParams",
			cm:            &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cpa", ResourceVersion: "1"}, Data: map[string]string{"linear": `{"nodesPerReplica":16}`}},
			defaultParams: defaultLinear,
			wantKey:       "linear",
			wantPayload:   `{"nodesPerReplica":16}`,
		},
		{
			name:        "explicit Auto and useDefaults=false strips metadata keys",
			cm:          &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cpa", ResourceVersion: "2"}, Data: map[string]string{"updateMode": "Auto", "useDefaults": "false", "ladder": `{"nodesToReplicas":[[1,1]]}`}},
			wantKey:     "ladder",
			wantPayload: `{"nodesToReplicas":[[1,1]]}`,
		},
		{
			name:          "useDefaults=true without control key uses defaultParams without warning",
			cm:            &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cpa", ResourceVersion: "3"}, Data: map[string]string{"useDefaults": "true"}},
			defaultParams: defaultLadder,
			wantKey:       "ladder",
			wantPayload:   defaultLadder["ladder"],
		},
		{
			name:          "useDefaults=true with matching linear payload uses defaultParams without warning",
			cm:            &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cpa", ResourceVersion: "3"}, Data: map[string]string{"useDefaults": "true", "linear": `{"nodesPerReplica":32}`}},
			defaultParams: defaultLinear,
			wantKey:       "linear",
			wantPayload:   defaultLinear["linear"],
		},
		{
			name:          "useDefaults=true overriding differing linear warns and uses defaultParams",
			cm:            &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cpa", ResourceVersion: "3"}, Data: map[string]string{"useDefaults": "true", "linear": `{"nodesPerReplica":999}`}},
			defaultParams: defaultLadder,
			wantWarning:   true,
			wantKey:       "ladder",
			wantPayload:   defaultLadder["ladder"],
		},
		{
			name:       "Off mode alone without control key pauses cleanly",
			cm:         &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cpa", ResourceVersion: "4"}, Data: map[string]string{"updateMode": "Off"}},
			wantPaused: true,
		},
		{
			name:       "Off mode returns early ignoring extra keys and invalid useDefaults",
			cm:         &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cpa", ResourceVersion: "5"}, Data: map[string]string{"updateMode": " off ", "useDefaults": "invalid", "linear": `{}`, "ladder": `{}`, "extra": "1"}},
			wantPaused: true,
		},
		{
			name:    "nil ConfigMap fails",
			cm:      nil,
			wantErr: true,
		},
		{
			name:    "invalid updateMode fails",
			cm:      &v1.ConfigMap{Data: map[string]string{"updateMode": "Manual", "linear": `{}`}},
			wantErr: true,
		},
		{
			name:    "invalid useDefaults fails",
			cm:      &v1.ConfigMap{Data: map[string]string{"useDefaults": "yes", "linear": `{}`}},
			wantErr: true,
		},
		{
			name:    "missing control mode key in Auto mode fails",
			cm:      &v1.ConfigMap{Data: map[string]string{"updateMode": "Auto"}},
			wantErr: true,
		},
		{
			name:    "both linear and ladder keys fail",
			cm:      &v1.ConfigMap{Data: map[string]string{"linear": `{}`, "ladder": `{}`}},
			wantErr: true,
		},
		{
			name:    "unexpected key fails",
			cm:      &v1.ConfigMap{Data: map[string]string{"linear": `{}`, "extra": "1"}},
			wantErr: true,
		},
		{
			name:          "useDefaults=true with empty defaultParams fails",
			cm:            &v1.ConfigMap{Data: map[string]string{"useDefaults": "true"}},
			defaultParams: nil,
			wantErr:       true,
		},
		{
			name:          "useDefaults=true with both linear and ladder in defaultParams fails",
			cm:            &v1.ConfigMap{Data: map[string]string{"useDefaults": "true"}},
			defaultParams: map[string]string{"linear": `{}`, "ladder": `{}`},
			wantErr:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var origLen int
			if tc.cm != nil {
				origLen = len(tc.cm.Data)
			}
			got, err := resolveConfigMap(tc.cm, tc.defaultParams)
			if (err != nil) != tc.wantErr {
				t.Fatalf("resolveConfigMap() err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got.paused != tc.wantPaused {
				t.Fatalf("paused = %v, want %v", got.paused, tc.wantPaused)
			}
			if (got.warning != "") != tc.wantWarning {
				t.Errorf("warning = %q, wantWarning %v", got.warning, tc.wantWarning)
			}
			if tc.wantPaused {
				if got.controllerCM != nil {
					t.Fatalf("expected nil controllerCM when paused, got %+v", got.controllerCM)
				}
				return
			}
			if len(got.controllerCM.Data) != 1 || got.controllerCM.Data[tc.wantKey] != tc.wantPayload {
				t.Fatalf("controllerCM.Data = %v, want map[%s:%s]", got.controllerCM.Data, tc.wantKey, tc.wantPayload)
			}
			if got.controllerCM.ResourceVersion != tc.cm.ResourceVersion {
				t.Errorf("ResourceVersion = %q, want %q", got.controllerCM.ResourceVersion, tc.cm.ResourceVersion)
			}
			if len(tc.cm.Data) != origLen {
				t.Errorf("base ConfigMap Data was mutated: len %d -> %d", origLen, len(tc.cm.Data))
			}
		})
	}
}
