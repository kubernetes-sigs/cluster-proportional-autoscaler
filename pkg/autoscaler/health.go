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
	"net/http"
	"sync"

	"github.com/golang/glog"
)

type healthInfo struct {
	m           sync.Mutex
	lastError   error
	failedCount int
}

func newHealthInfo() *healthInfo {
	return &healthInfo{m: sync.Mutex{}, lastError: nil, failedCount: 0}
}

func (h *healthInfo) setLastPollError(err error) int {
	h.m.Lock()
	defer h.m.Unlock()
	h.lastError = err
	if h.lastError == nil {
		h.failedCount = 0
	} else {
		h.failedCount++
	}
	return h.failedCount
}

func (h *healthInfo) getLastPollError() error {
	h.m.Lock()
	defer h.m.Unlock()
	return h.lastError
}

type HealthServer interface {
	Start()
}

type httpHealthServer struct {
	lastPollCycleHealth *healthInfo
}

func (hs *httpHealthServer) Start() {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {})
	http.HandleFunc("/last-poll", hs.lastPollFn)
	glog.Fatal(http.ListenAndServe(":8080", nil))
}

func (hs *httpHealthServer) lastPollFn(w http.ResponseWriter, req *http.Request) {
	if err := hs.lastPollCycleHealth.getLastPollError(); err != nil {
		w.WriteHeader(500)
		w.Write([]byte(fmt.Sprintf("Encountered error at last poll cycle: %v", err)))
		return
	}
}
