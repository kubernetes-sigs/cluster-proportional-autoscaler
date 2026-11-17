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

package k8sclient

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/streaming"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	restclientwatch "k8s.io/client-go/rest/watch"
)

func protobufInfo(tb testing.TB) runtime.SerializerInfo {
	tb.Helper()
	info, ok := runtime.SerializerInfoForMediaType(scheme.Codecs.SupportedMediaTypes(), runtime.ContentTypeProtobuf)
	if !ok {
		tb.Fatal("protobuf serializer not registered")
	}
	return info
}

func encodeList(tb testing.TB, list *v1.NodeList) []byte {
	tb.Helper()
	enc := scheme.Codecs.EncoderForVersion(protobufInfo(tb).Serializer, v1.SchemeGroupVersion)
	var buf bytes.Buffer
	if err := enc.Encode(list, &buf); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

func encodeEvents(tb testing.TB, events ...watch.Event) []byte {
	tb.Helper()
	info := protobufInfo(tb)
	var buf bytes.Buffer
	fw := info.StreamSerializer.Framer.NewFrameWriter(&buf)
	enc := restclientwatch.NewEncoder(streaming.NewEncoder(fw, info.StreamSerializer.Serializer),
		scheme.Codecs.EncoderForVersion(info.Serializer, v1.SchemeGroupVersion))
	for i := range events {
		if err := enc.Encode(&events[i]); err != nil {
			tb.Fatal(err)
		}
	}
	return buf.Bytes()
}

// heavyNode builds a Node with many fields the scanner must skip.
func heavyNode(name, cpu string, unschedulable bool, ready *v1.ConditionStatus, rv string) *v1.Node {
	n := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, ResourceVersion: rv,
			Labels:      map[string]string{"node-pool": "pool-1", "kubernetes.io/hostname": name},
			Annotations: map[string]string{"node.alpha.kubernetes.io/ttl": "0", "big": string(bytes.Repeat([]byte("x"), 2048))},
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubelet", Operation: metav1.ManagedFieldsOperationUpdate,
				FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:status":{}}`)}}},
		},
		Spec: v1.NodeSpec{PodCIDR: "10.0.0.0/24", ProviderID: "provider://zone/" + name, Unschedulable: unschedulable,
			Taints: []v1.Taint{{Key: "k", Value: "v", Effect: v1.TaintEffectNoSchedule}}},
		Status: v1.NodeStatus{
			Capacity:    v1.ResourceList{v1.ResourceCPU: resource.MustParse("64"), v1.ResourceMemory: resource.MustParse("256Gi")},
			Allocatable: v1.ResourceList{v1.ResourceMemory: resource.MustParse("250Gi"), v1.ResourcePods: resource.MustParse("110")},
			Addresses:   []v1.NodeAddress{{Type: v1.NodeInternalIP, Address: "10.1.2.3"}},
			NodeInfo:    v1.NodeSystemInfo{KubeletVersion: "v1.36.1", OSImage: "Linux"},
			Images:      []v1.ContainerImage{{Names: []string{"registry.k8s.io/pause:3.9"}, SizeBytes: 123}},
			Conditions: []v1.NodeCondition{
				{Type: v1.NodeMemoryPressure, Status: v1.ConditionFalse, Reason: "KubeletHasSufficientMemory", Message: "ok"},
				{Type: v1.NodeDiskPressure, Status: v1.ConditionFalse},
			},
		},
	}
	if cpu != "" {
		n.Status.Allocatable[v1.ResourceCPU] = resource.MustParse(cpu)
	}
	if ready != nil {
		n.Status.Conditions = append(n.Status.Conditions, v1.NodeCondition{Type: v1.NodeReady, Status: *ready,
			LastHeartbeatTime: metav1.Now(), Reason: "KubeletReady", Message: "kubelet is posting ready status"})
	}
	return n
}

func cs(s v1.ConditionStatus) *v1.ConditionStatus { return &s }

// informerStatus computes the cluster status matching the informer path of GetClusterStatus.
func informerStatus(nodes []*v1.Node) ClusterStatus {
	var st ClusterStatus
	var tc, sc resource.Quantity
	st.TotalNodes = clampInt32(int64(len(nodes)))
	for _, node := range nodes {
		tc.Add(node.Status.Allocatable[v1.ResourceCPU])
		if !node.Spec.Unschedulable && isNodeReady(node) {
			st.SchedulableNodes++
			sc.Add(node.Status.Allocatable[v1.ResourceCPU])
		}
	}
	st.TotalCores = clampInt32(tc.Value())
	st.SchedulableCores = clampInt32(sc.Value())
	return st
}

func testNodes() []*v1.Node {
	return []*v1.Node{
		heavyNode("n-ready", "3920m", false, cs(v1.ConditionTrue), "11"),
		heavyNode("n-cordoned", "4", true, cs(v1.ConditionTrue), "12"),
		heavyNode("n-notready", "15890m", false, cs(v1.ConditionFalse), "13"),
		heavyNode("n-unknown", "2", false, cs(v1.ConditionUnknown), "14"),
		heavyNode("n-nocondition", "1500m", false, nil, "15"),
		heavyNode("n-nocpu", "", false, cs(v1.ConditionTrue), "16"),
		heavyNode("n-ready2", "31850m", false, cs(v1.ConditionTrue), "17"),
	}
}

func toList(nodes []*v1.Node, rv string) *v1.NodeList {
	l := &v1.NodeList{ListMeta: metav1.ListMeta{ResourceVersion: rv}}
	for _, n := range nodes {
		l.Items = append(l.Items, *n)
	}
	return l
}

func TestDecodeNodeListStreamMatchesInformer(t *testing.T) {
	nodes := testNodes()
	data := encodeList(t, toList(nodes, "4242"))
	got := map[string]compactNode{}
	var totals compactTotals
	rv, err := decodeNodeListStream(bytes.NewReader(data), func(name string, n compactNode) {
		got[name] = n
		totals.apply(n, +1)
	})
	if err != nil {
		t.Fatal(err)
	}
	if rv != "4242" {
		t.Errorf("rv = %q, want 4242", rv)
	}
	want := map[string]compactNode{
		"n-ready":       {milliCPU: 3920, ready: true},
		"n-cordoned":    {milliCPU: 4000, unschedulable: true, ready: true},
		"n-notready":    {milliCPU: 15890},
		"n-unknown":     {milliCPU: 2000},
		"n-nocondition": {milliCPU: 1500},
		"n-nocpu":       {ready: true},
		"n-ready2":      {milliCPU: 31850, ready: true},
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: got %+v, want %+v", name, got[name], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("decoded %d nodes, want %d", len(got), len(want))
	}
	if s, w := totals.clusterStatus(), informerStatus(nodes); s != w {
		t.Errorf("status = %+v, informer path = %+v", s, w)
	}
}

func TestDecodeNodeListStreamRejectsNonProtobuf(t *testing.T) {
	if _, err := decodeNodeListStream(bytes.NewReader([]byte(`{"kind":"NodeList"}`)), func(string, compactNode) {}); err == nil {
		t.Fatal("expected error for JSON body")
	}
}

func TestDecodeNodeListStreamRejectsTruncatedStream(t *testing.T) {
	data := encodeList(t, toList(testNodes(), "4242"))
	truncated := data[:len(data)/2]
	if _, err := decodeNodeListStream(bytes.NewReader(truncated), func(string, compactNode) {}); err == nil {
		t.Fatal("expected error for truncated protobuf NodeList stream")
	}
}

func TestDecodeNodeListStreamEmpty(t *testing.T) {
	n := 0
	rv, err := decodeNodeListStream(bytes.NewReader(encodeList(t, toList(nil, "7"))), func(string, compactNode) { n++ })
	if err != nil || rv != "7" || n != 0 {
		t.Fatalf("rv=%q n=%d err=%v", rv, n, err)
	}
}

func TestDecodeWatchStream(t *testing.T) {
	data := encodeEvents(t,
		watch.Event{Type: watch.Added, Object: heavyNode("a", "2", false, cs(v1.ConditionTrue), "100")},
		watch.Event{Type: watch.Modified, Object: heavyNode("a", "2", true, cs(v1.ConditionTrue), "101")},
		watch.Event{Type: watch.Bookmark, Object: &v1.Node{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "150"}}},
		watch.Event{Type: watch.Deleted, Object: heavyNode("a", "2", true, cs(v1.ConditionTrue), "151")},
	)
	type ev struct {
		typ, name, rv string
		n             compactNode
	}
	var got []ev
	err := decodeWatchStream(bytes.NewReader(data), func(typ, name, rv []byte, n compactNode) error {
		got = append(got, ev{string(typ), string(name), string(rv), n})
		return nil
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF at end of stream, got %v", err)
	}
	want := []ev{
		{"ADDED", "a", "100", compactNode{milliCPU: 2000, ready: true}},
		{"MODIFIED", "a", "101", compactNode{milliCPU: 2000, unschedulable: true, ready: true}},
		{"BOOKMARK", "", "150", compactNode{}},
		{"DELETED", "a", "151", compactNode{milliCPU: 2000, unschedulable: true, ready: true}},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("events:\n got %+v\nwant %+v", got, want)
	}
}

func TestDecodeWatchStreamRejectsTruncatedAndMalformedFrames(t *testing.T) {
	valid := encodeEvents(t, watch.Event{Type: watch.Added, Object: heavyNode("a", "2", false, cs(v1.ConditionTrue), "100")})
	noop := func(_, _, _ []byte, _ compactNode) error { return nil }
	if err := decodeWatchStream(bytes.NewReader(valid[:2]), noop); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated header err = %v, want ErrUnexpectedEOF", err)
	}
	if err := decodeWatchStream(bytes.NewReader(valid[:len(valid)-5]), noop); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated body err = %v, want ErrUnexpectedEOF", err)
	}
	corruptPayload := []byte{0x0a, 0x05, 'A', 'D', 'D', 'E', 'D', 0x12, 0xff}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(corruptPayload)))
	frame := append(hdr[:], corruptPayload...)
	if err := decodeWatchStream(bytes.NewReader(frame), noop); err == nil {
		t.Fatal("expected error for malformed WatchEvent wire payload")
	}
}

// fakeAPIServer serves /api/v1/nodes LIST and WATCH with protobuf bodies.
type fakeAPIServer struct {
	t         *testing.T
	lists     [][]byte // successive LIST responses (last one repeats)
	watches   [][]byte // successive WATCH responses (then block until cancel)
	watchGate chan struct{}
	nList     int32
	nWatch    int32
}

func (f *fakeAPIServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/nodes" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("watch") == "true" {
		if f.watchGate != nil {
			select {
			case <-f.watchGate:
			case <-r.Context().Done():
				return
			}
		}
		i := int(atomic.AddInt32(&f.nWatch, 1)) - 1
		w.Header().Set("Content-Type", protobufWatchMediaType)
		if i < len(f.watches) {
			_, _ = w.Write(f.watches[i])
		} else {
			<-r.Context().Done()
		}
		return
	}
	if got := r.URL.Query().Get("resourceVersion"); got != "0" {
		f.t.Errorf("LIST resourceVersion = %q, want 0 (served from watch cache)", got)
	}
	i := int(atomic.AddInt32(&f.nList, 1)) - 1
	if i >= len(f.lists) {
		i = len(f.lists) - 1
	}
	w.Header().Set("Content-Type", protobufMediaType)
	_, _ = w.Write(f.lists[i])
}

func restClientFor(t *testing.T, url string) rest.Interface {
	t.Helper()
	rc, err := rest.RESTClientFor(&rest.Config{
		Host:    url,
		APIPath: "/api",
		ContentConfig: rest.ContentConfig{
			GroupVersion:         &v1.SchemeGroupVersion,
			NegotiatedSerializer: scheme.Codecs.WithoutConversion(),
			ContentType:          runtime.ContentTypeProtobuf,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return rc
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestCompactNodeSourceListWatchEndToEnd(t *testing.T) {
	nodes := testNodes()
	newNode := heavyNode("n-new", "8", false, cs(v1.ConditionTrue), "30")
	cordoned := heavyNode("n-ready2", "31850m", true, cs(v1.ConditionTrue), "31")
	watchGate := make(chan struct{})
	fake := &fakeAPIServer{
		t:         t,
		watchGate: watchGate,
		lists:     [][]byte{encodeList(t, toList(nodes, "20"))},
		watches: [][]byte{encodeEvents(t,
			watch.Event{Type: watch.Added, Object: newNode},
			watch.Event{Type: watch.Modified, Object: cordoned},
			watch.Event{Type: watch.Deleted, Object: nodes[2]},
		)},
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	k8sCli, err := NewK8sClientWithCompactNodeSource(clientset, "default", "deployment/my-deployment", "")
	if err != nil {
		t.Fatal(err)
	}
	defer k8sCli.(interface{ Stop() }).Stop()

	st, err := k8sCli.GetClusterStatus()
	if err != nil {
		t.Fatal(err)
	}
	if want := informerStatus(nodes); *st != want {
		t.Errorf("initial status = %+v, informer path = %+v", *st, want)
	}

	close(watchGate)
	after := []*v1.Node{nodes[0], nodes[1], nodes[3], nodes[4], nodes[5], cordoned, newNode}
	want := informerStatus(after)
	waitFor(t, "watch events applied", func() bool {
		st, err := k8sCli.GetClusterStatus()
		return err == nil && st != nil && *st == want
	})
	src := k8sCli.(*k8sClient).compact
	src.mu.Lock()
	rv := src.rv
	src.mu.Unlock()
	if rv != "13" { // resourceVersion of the last event (the DELETED node object)
		t.Errorf("rv after watch = %q, want 13", rv)
	}
}

func TestCompactNodeSourceRelistsOnWatchError(t *testing.T) {
	first := testNodes()
	second := []*v1.Node{heavyNode("only", "16", false, cs(v1.ConditionTrue), "90")}
	gone := &metav1.Status{Status: metav1.StatusFailure, Code: http.StatusGone, Reason: metav1.StatusReasonExpired, Message: "too old resource version"}
	fake := &fakeAPIServer{t: t,
		lists:   [][]byte{encodeList(t, toList(first, "20")), encodeList(t, toList(second, "95"))},
		watches: [][]byte{encodeEvents(t, watch.Event{Type: watch.Error, Object: gone})},
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	src := newCompactNodeSource(restClientFor(t, srv.URL), "")
	defer src.stop()
	if _, err := src.status(); err != nil {
		t.Fatal(err)
	}
	want := informerStatus(second)
	waitFor(t, "relist after watch ERROR", func() bool {
		st, err := src.status()
		return err == nil && st != nil && *st == want
	})
	if n := atomic.LoadInt32(&fake.nList); n != 2 {
		t.Errorf("LIST calls = %d, want 2", n)
	}
}

func TestCompactNodeSourcePropagatesLabelSelectorAndHandlesHTTPGone(t *testing.T) {
	const selector = "node-pool=pool-1,kubernetes.io/os=linux"
	first := testNodes()[:2]
	second := testNodes()[2:4]
	var listCount, watchCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("labelSelector"); got != selector {
			t.Errorf("query labelSelector = %q, want %q (watch=%s)", got, selector, r.URL.Query().Get("watch"))
		}
		if r.URL.Query().Get("watch") == "true" {
			if atomic.AddInt32(&watchCount, 1) == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusGone)
				_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Expired","code":410}`))
				return
			}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", protobufMediaType)
		if atomic.AddInt32(&listCount, 1) == 1 {
			_, _ = w.Write(encodeList(t, toList(first, "10")))
			return
		}
		_, _ = w.Write(encodeList(t, toList(second, "20")))
	}))
	defer srv.Close()

	src := newCompactNodeSource(restClientFor(t, srv.URL), selector)
	defer src.stop()
	if _, err := src.status(); err != nil {
		t.Fatal(err)
	}
	want := informerStatus(second)
	waitFor(t, "relist after HTTP 410 Gone", func() bool {
		st, err := src.status()
		return err == nil && st != nil && *st == want
	})
}

func TestCompactNodeSourceListFailureIsRetried(t *testing.T) {
	var fail int32 = 1
	nodes := testNodes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			<-r.Context().Done()
			return
		}
		if atomic.CompareAndSwapInt32(&fail, 1, 0) {
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", protobufMediaType)
		_, _ = w.Write(encodeList(t, toList(nodes, "20")))
	}))
	defer srv.Close()
	src := newCompactNodeSource(restClientFor(t, srv.URL), "")
	defer src.stop()
	if _, err := src.status(); err == nil {
		t.Fatal("expected first status() to fail")
	}
	st, err := src.status()
	if err != nil {
		t.Fatal(err)
	}
	if want := informerStatus(nodes); *st != want {
		t.Errorf("status = %+v, want %+v", *st, want)
	}
}

func TestNewK8sClientWithCompactNodeSourceRejectsNilClientset(t *testing.T) {
	if _, err := NewK8sClientWithCompactNodeSource(nil, "default", "deployment/app", ""); err == nil {
		t.Fatal("expected error for nil clientset")
	}
}

func TestDecodeNodeListStreamZeroCPUQuantity(t *testing.T) {
	node := heavyNode("n-zerocpu", "", false, cs(v1.ConditionTrue), "1")
	node.Status.Allocatable[v1.ResourceCPU] = resource.Quantity{}
	var got compactNode
	_, err := decodeNodeListStream(bytes.NewReader(encodeList(t, toList([]*v1.Node{node}, "1"))), func(_ string, n compactNode) {
		got = n
	})
	if err != nil {
		t.Fatalf("unexpected error for empty Quantity{}: %v", err)
	}
	if got != (compactNode{milliCPU: 0, ready: true}) {
		t.Errorf("got %+v, want {milliCPU:0 ready:true}", got)
	}
}

func TestParseMilliCPU(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "", want: 0},
		{in: "0", want: 0},
		{in: "0m", want: 0},
		{in: "4", want: 4000},
		{in: "3920m", want: 3920},
		{in: "1.5", want: 1500},
		{in: "1500u", want: 2},
		{in: "2k", want: 2000000},
		{in: "-1", want: -1000},
		{in: "m", want: 0}, // matches resource.ParseQuantity("m")
		{in: "not-a-cpu", wantErr: true},
		{in: "4x", wantErr: true},
	}
	for _, tc := range tests {
		got, err := parseMilliCPU([]byte(tc.in))
		if (err != nil) != tc.wantErr {
			t.Errorf("parseMilliCPU(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("parseMilliCPU(%q) = %d, want %d", tc.in, got, tc.want)
		}
		if tc.in != "" {
			q, qerr := resource.ParseQuantity(tc.in)
			if (qerr != nil) != (err != nil) {
				t.Errorf("parseMilliCPU(%q) err=%v disagrees with resource.ParseQuantity err=%v", tc.in, err, qerr)
			} else if err == nil && got != q.MilliValue() {
				t.Errorf("parseMilliCPU(%q) = %d disagrees with resource.ParseQuantity = %d", tc.in, got, q.MilliValue())
			}
		}
	}
}

func TestCompactNodeSourceHeartbeatZeroAllocs(t *testing.T) {
	rawNode, err := heavyNode("node-1", "100", false, cs(v1.ConditionTrue), "3920m").Marshal()
	if err != nil {
		t.Fatalf("marshal node: %v", err)
	}
	src := &compactNodeSource{
		nodes:  map[string]compactNode{"node-1": {milliCPU: 3920, ready: true}},
		totals: compactTotals{nodes: 1, schedulableNodes: 1, milliCPU: 3920, schedulableMilli: 3920},
		rv:     "100",
	}
	evType := []byte("MODIFIED")
	if allocs := testing.AllocsPerRun(100, func() {
		name, rv, node, err := scanNode(rawNode)
		if err != nil {
			t.Fatal(err)
		}
		if err := src.applyEvent(evType, name, rv, node); err != nil {
			t.Fatal(err)
		}
	}); allocs != 0 {
		t.Errorf("scanNode+applyEvent unchanged heartbeat allocs = %v, want 0", allocs)
	}
}
