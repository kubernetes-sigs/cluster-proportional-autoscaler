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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPHealthServerEndpoints(t *testing.T) {
	info := newHealthInfo()
	hs := &httpHealthServer{lastPollCycleHealth: info, port: 9154}
	ts := httptest.NewServer(hs.handler())
	defer ts.Close()

	get := func(path string) (int, string) {
		t.Helper()
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("reading %s body failed: %v", path, err)
		}
		return resp.StatusCode, string(body)
	}

	if status, _ := get("/healthz"); status != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want 200", status)
	}
	if status, _ := get("/last-poll"); status != http.StatusOK {
		t.Errorf("GET /last-poll (healthy) status = %d, want 200", status)
	}

	info.setLastPollError(errors.New("apiserver timeout"))
	if status, body := get("/last-poll"); status != http.StatusInternalServerError || !strings.Contains(body, "apiserver timeout") {
		t.Errorf("GET /last-poll (unhealthy) = (%d, %q), want (500, contains %q)", status, body, "apiserver timeout")
	}

	info.setLastPollError(nil)
	if status, _ := get("/last-poll"); status != http.StatusOK {
		t.Errorf("GET /last-poll (recovered) status = %d, want 200", status)
	}
}

func TestHTTPHealthServerDisabledPort(t *testing.T) {
	// port <= 0 should return immediately without binding or exiting.
	(&httpHealthServer{port: 0}).Start()
}
