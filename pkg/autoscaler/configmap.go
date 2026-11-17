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
	"fmt"
	"sort"
	"strings"

	v1 "k8s.io/api/core/v1"
)

const (
	keyUpdateMode  = "updateMode"
	keyUseDefaults = "useDefaults"
	keyLinear      = "linear"
	keyLadder      = "ladder"
)

// resolveConfigMap parses optional "updateMode" ("Auto" | "Off") and "useDefaults" ("true" | "false")
// keys from cm. When updateMode is "Off", it returns paused=true without evaluating defaultParams.
// Otherwise it returns a shallow ConfigMap view with metadata keys stripped and the single active
// control mode ("linear" or "ladder") resolved from defaultParams (when useDefaults=true) or cm.Data.
func resolveConfigMap(cm *v1.ConfigMap, defaultParams map[string]string) (paused bool, controllerCM *v1.ConfigMap, err error) {
	if cm == nil {
		return false, nil, fmt.Errorf("configMap is nil")
	}

	switch raw := strings.TrimSpace(cm.Data[keyUpdateMode]); strings.ToLower(raw) {
	case "", "auto":
	case "off":
		paused = true
	default:
		return false, nil, fmt.Errorf("configmap %s/%s: unsupported %s %q (expected \"Auto\" or \"Off\")",
			cm.Namespace, cm.Name, keyUpdateMode, raw)
	}

	var useDefaults bool
	switch raw := strings.TrimSpace(cm.Data[keyUseDefaults]); strings.ToLower(raw) {
	case "", "false":
	case "true":
		useDefaults = true
	default:
		return false, nil, fmt.Errorf("configmap %s/%s: unsupported %s %q (expected \"true\" or \"false\")",
			cm.Namespace, cm.Name, keyUseDefaults, raw)
	}

	mode, payload, err := extractControlMode(cm.Data, cm.Namespace, cm.Name, true)
	if err != nil {
		return false, nil, err
	}
	if paused {
		return true, nil, nil
	}

	if useDefaults {
		if len(defaultParams) == 0 {
			return false, nil, fmt.Errorf("configmap %s/%s: %s is true but --default-params is not configured",
				cm.Namespace, cm.Name, keyUseDefaults)
		}
		mode, payload, err = extractControlMode(defaultParams, cm.Namespace, cm.Name, false)
		if err != nil {
			return false, nil, fmt.Errorf("configmap %s/%s: invalid --default-params: %w", cm.Namespace, cm.Name, err)
		}
	}
	if mode == "" {
		return false, nil, fmt.Errorf("configmap %s/%s: expected one control mode key (%q or %q)",
			cm.Namespace, cm.Name, keyLinear, keyLadder)
	}

	return false, &v1.ConfigMap{
		ObjectMeta: cm.ObjectMeta,
		Data:       map[string]string{mode: payload},
	}, nil
}

func extractControlMode(data map[string]string, namespace, name string, skipMetadata bool) (string, string, error) {
	keys := make([]string, 0, len(data))
	for k := range data {
		if skipMetadata && (k == keyUpdateMode || k == keyUseDefaults) {
			continue
		}
		if k != keyLinear && k != keyLadder {
			return "", "", fmt.Errorf("configmap %s/%s: unexpected key %q (expected %q or %q)",
				namespace, name, k, keyLinear, keyLadder)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 1 {
		return "", "", fmt.Errorf("configmap %s/%s: expected at most one control mode key, got %v",
			namespace, name, keys)
	}
	if len(keys) == 0 {
		return "", "", nil
	}
	return keys[0], data[keys[0]], nil
}
