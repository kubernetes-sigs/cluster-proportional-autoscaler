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
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"

	"github.com/golang/glog"
)

// The compact node source keeps the same apiserver contract as the trimmed Node informer
// (one LIST with resourceVersion=0 served from the watch cache, then one WATCH), but it never
// materialises v1.Node objects. The protobuf LIST response is read as a stream and each Node is
// scanned for only the four fields CPA needs (name, spec.unschedulable, the Ready condition and
// status.allocatable["cpu"]); everything else is skipped by its length prefix. Each node is stored
// as a small record and the cluster totals are kept up to date incrementally from WATCH events.

const (
	protobufMediaType       = "application/vnd.kubernetes.protobuf"
	protobufWatchMediaType  = "application/vnd.kubernetes.protobuf;stream=watch"
	compactListTimeout      = 2 * time.Minute
	compactMinWatchTimeout  = 5 * time.Minute
	compactWatchGracePeriod = 30 * time.Second
	compactMaxFrameBytes    = 16 << 20
	compactRetryBackoffBase = time.Second
	compactRetryBackoffMax  = 30 * time.Second
)

// Protobuf wire field numbers from k8s.io/api/core/v1 and k8s.io/apimachinery
// generated.proto (stable under Kubernetes API compatibility rules).
const (
	unknownRawField                protowire.Number = 2 // runtime.Unknown.raw
	watchEventTypeField            protowire.Number = 1 // metav1.WatchEvent.type
	watchEventObjectField          protowire.Number = 2 // metav1.WatchEvent.object
	rawExtensionRawField           protowire.Number = 1 // runtime.RawExtension.raw
	nodeListMetadataField          protowire.Number = 1 // v1.NodeList.metadata
	nodeListItemsField             protowire.Number = 2 // v1.NodeList.items
	listMetaResourceVersionField   protowire.Number = 2 // metav1.ListMeta.resourceVersion
	nodeMetadataField              protowire.Number = 1 // v1.Node.metadata
	nodeSpecField                  protowire.Number = 2 // v1.Node.spec
	nodeStatusField                protowire.Number = 3 // v1.Node.status
	objectMetaNameField            protowire.Number = 1 // metav1.ObjectMeta.name
	objectMetaResourceVersionField protowire.Number = 6 // metav1.ObjectMeta.resourceVersion
	nodeSpecUnschedulableField     protowire.Number = 4 // v1.NodeSpec.unschedulable
	nodeStatusAllocatableField     protowire.Number = 2 // v1.NodeStatus.allocatable
	nodeStatusConditionsField      protowire.Number = 4 // v1.NodeStatus.conditions
	allocatableEntryKeyField       protowire.Number = 1 // map<string, Quantity>.key
	allocatableEntryValueField     protowire.Number = 2 // map<string, Quantity>.value
	quantityStringField            protowire.Number = 1 // resource.Quantity.string
	nodeConditionTypeField         protowire.Number = 1 // v1.NodeCondition.type
	nodeConditionStatusField       protowire.Number = 2 // v1.NodeCondition.status
)

var protobufMagic = []byte{0x6b, 0x38, 0x73, 0x00}

// errCompactRelist signals that the watch cannot be resumed and a fresh LIST is required.
var errCompactRelist = errors.New("watch expired, relist required")

type compactNode struct {
	milliCPU      int64
	unschedulable bool
	ready         bool
}

func (n compactNode) schedulable() bool { return !n.unschedulable && n.ready }

type compactTotals struct {
	nodes            int64
	schedulableNodes int64
	milliCPU         int64
	schedulableMilli int64
}

func (t *compactTotals) apply(n compactNode, sign int64) {
	t.nodes += sign
	t.milliCPU += sign * n.milliCPU
	if n.schedulable() {
		t.schedulableNodes += sign
		t.schedulableMilli += sign * n.milliCPU
	}
}

func (t *compactTotals) clusterStatus() ClusterStatus {
	return ClusterStatus{
		TotalNodes:       clampInt32(t.nodes),
		SchedulableNodes: clampInt32(t.schedulableNodes),
		TotalCores:       clampInt32(ceilMilli(t.milliCPU)),
		SchedulableCores: clampInt32(ceilMilli(t.schedulableMilli)),
	}
}

// compactNodeSource maintains per-node {unschedulable, ready, milliCPU} records from a streamed
// LIST + WATCH, without a Node informer cache.
type compactNodeSource struct {
	rc            rest.Interface
	labelSelector string

	relistMu sync.Mutex
	mu       sync.Mutex
	nodes    map[string]compactNode
	totals   compactTotals
	rv       string
	synced   bool
	started  bool
	stopped  bool

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func newCompactNodeSource(rc rest.Interface, labelSelector string) *compactNodeSource {
	return &compactNodeSource{
		rc:            rc,
		labelSelector: labelSelector,
		stopCh:        make(chan struct{}),
	}
}

func contextWithStop(stopCh <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	select {
	case <-stopCh:
		cancel()
		return ctx, cancel
	default:
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-stopCh:
			cancel()
		case <-done:
		}
	}()
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			close(done)
			cancel()
		})
	}
}

// ensureSynced performs the initial LIST if it has not succeeded yet and starts the WATCH loop once.
func (c *compactNodeSource) ensureSynced() error {
	c.mu.Lock()
	synced := c.synced
	c.mu.Unlock()
	if !synced {
		c.relistMu.Lock()
		c.mu.Lock()
		synced = c.synced
		c.mu.Unlock()
		var err error
		if !synced {
			ctx, cancel := contextWithStop(c.stopCh)
			err = c.relist(ctx)
			cancel()
		}
		c.relistMu.Unlock()
		if err != nil {
			return err
		}
	}
	c.mu.Lock()
	if !c.started && !c.stopped {
		c.started = true
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			ctx, cancel := contextWithStop(c.stopCh)
			defer cancel()
			c.watchLoop(ctx)
		}()
	}
	c.mu.Unlock()
	return nil
}

// status returns the current totals in the same shape and rounding as the informer path
// (resource.Quantity.Value() rounds the summed cores up).
func (c *compactNodeSource) status() (*ClusterStatus, error) {
	if err := c.ensureSynced(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	cs := c.totals.clusterStatus()
	c.mu.Unlock()
	return &cs, nil
}

func ceilMilli(m int64) int64 {
	if m <= 0 {
		return 0
	}
	return (m + 999) / 1000
}

func clampInt32(v int64) int32 {
	if v <= 0 {
		return 0
	}
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(v)
}

func (c *compactNodeSource) stop() {
	c.mu.Lock()
	if !c.stopped {
		c.stopped = true
		close(c.stopCh)
	}
	c.mu.Unlock()
	c.wg.Wait()
}

// relist streams one LIST (resourceVersion=0, served from the watch cache) and swaps in the result.
func (c *compactNodeSource) relist(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, compactListTimeout)
	defer cancel()
	req := c.rc.Get().Resource("nodes").
		Param("resourceVersion", "0").
		SetHeader("Accept", protobufMediaType)
	if c.labelSelector != "" {
		req = req.Param("labelSelector", c.labelSelector)
	}
	start := time.Now()
	body, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("compact node source: LIST nodes: %w", err)
	}
	defer func() { _ = body.Close() }()

	c.mu.Lock()
	prevLen := len(c.nodes)
	c.mu.Unlock()
	nodes := make(map[string]compactNode, prevLen)
	var totals compactTotals
	rv, err := decodeNodeListStream(body, func(name string, n compactNode) {
		if old, ok := nodes[name]; ok {
			totals.apply(old, -1)
		}
		nodes[name] = n
		totals.apply(n, +1)
	})
	if err != nil {
		return fmt.Errorf("compact node source: decode LIST: %w", err)
	}
	if rv == "" {
		return errors.New("compact node source: LIST response missing resourceVersion")
	}
	c.mu.Lock()
	c.nodes, c.totals, c.rv, c.synced = nodes, totals, rv, true
	c.mu.Unlock()
	glog.V(0).Infof("Compact node source: listed %d nodes (%d schedulable, %d mCPU) at rv=%s in %v",
		totals.nodes, totals.schedulableNodes, totals.milliCPU, rv, time.Since(start).Round(time.Millisecond))
	return nil
}

func (c *compactNodeSource) watchLoop(ctx context.Context) {
	backoff := compactRetryBackoffBase
	relistedSinceHealthy := false
	sleepBackoff := func() bool {
		timer := time.NewTimer(wait.Jitter(backoff, 0.2))
		defer timer.Stop()
		if backoff *= 2; backoff > compactRetryBackoffMax {
			backoff = compactRetryBackoffMax
		}
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}

	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		sawEvents, err := c.watchOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			if sawEvents || time.Since(start) >= time.Second {
				backoff = compactRetryBackoffBase
				relistedSinceHealthy = false
				continue // server closed the stream cleanly; resume from the last rv.
			}
			// Guard against a server/proxy immediately closing empty 200 OK watch streams.
		} else if errors.Is(err, errCompactRelist) {
			glog.V(0).Infof("Compact node source: %v", err)
			firstRelist := !relistedSinceHealthy
			relistedSinceHealthy = true
			if !firstRelist && !sleepBackoff() {
				return
			}
			lerr := c.relist(ctx)
			if lerr == nil {
				if firstRelist {
					backoff = compactRetryBackoffBase
				}
				continue
			}
			glog.Errorf("Compact node source: relist failed: %v", lerr)
		} else {
			glog.Warningf("Compact node source: watch error: %v", err)
		}
		if !sleepBackoff() {
			return
		}
	}
}

func (c *compactNodeSource) watchOnce(ctx context.Context) (bool, error) {
	c.mu.Lock()
	rv := c.rv
	c.mu.Unlock()
	timeout := wait.Jitter(compactMinWatchTimeout, 1.0)
	reqCtx, cancel := context.WithTimeout(ctx, timeout+compactWatchGracePeriod)
	defer cancel()
	req := c.rc.Get().Resource("nodes").
		Param("watch", "true").
		Param("resourceVersion", rv).
		Param("allowWatchBookmarks", "true").
		Param("timeoutSeconds", strconv.Itoa(int(timeout.Seconds()))).
		SetHeader("Accept", protobufWatchMediaType)
	if c.labelSelector != "" {
		req = req.Param("labelSelector", c.labelSelector)
	}
	body, err := req.Stream(reqCtx)
	if err != nil {
		if ctx.Err() != nil {
			return false, nil
		}
		if apierrors.IsGone(err) || apierrors.IsResourceExpired(err) {
			return false, fmt.Errorf("%w: %v", errCompactRelist, err)
		}
		return false, err
	}
	defer func() { _ = body.Close() }()
	var sawEvents bool
	err = decodeWatchStream(body, func(eventType, name, eventRV []byte, n compactNode) error {
		if aerr := c.applyEvent(eventType, name, eventRV, n); aerr != nil {
			return aerr
		}
		sawEvents = true
		return nil
	})
	if ctx.Err() != nil {
		return sawEvents, nil
	}
	if errors.Is(err, io.EOF) {
		return sawEvents, nil
	}
	return sawEvents, err
}

func (c *compactNodeSource) applyEvent(eventType, name, rv []byte, n compactNode) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch string(eventType) {
	case "ADDED", "MODIFIED":
		if len(name) == 0 {
			return errors.New("watch event missing node name")
		}
		if old, ok := c.nodes[string(name)]; ok {
			if old != n {
				c.totals.apply(old, -1)
				c.nodes[string(name)] = n
				c.totals.apply(n, +1)
			}
		} else {
			c.nodes[string(name)] = n
			c.totals.apply(n, +1)
		}
	case "DELETED":
		if len(name) == 0 {
			return errors.New("watch DELETED event missing node name")
		}
		if old, ok := c.nodes[string(name)]; ok {
			c.totals.apply(old, -1)
			delete(c.nodes, string(name))
		}
	case "BOOKMARK":
	case "ERROR":
		return fmt.Errorf("%w: watch ERROR event", errCompactRelist)
	default:
		return fmt.Errorf("unknown watch event type %q", eventType)
	}
	if len(rv) > 0 && string(rv) != c.rv {
		c.rv = string(rv)
	}
	return nil
}

// ---------------------------------------------------------------- protobuf scanning

type protoStreamReader interface {
	io.Reader
	io.ByteReader
	Discard(n int) (int, error)
}

// limitedBufReader enforces a byte budget directly over a shared *bufio.Reader without
// introducing a second buffering layer.
type limitedBufReader struct {
	br *bufio.Reader
	n  int64
}

func (l *limitedBufReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.br.Read(p)
	l.n -= int64(n)
	return n, err
}

func (l *limitedBufReader) ReadByte() (byte, error) {
	if l.n <= 0 {
		return 0, io.EOF
	}
	b, err := l.br.ReadByte()
	if err == nil {
		l.n--
	}
	return b, err
}

func (l *limitedBufReader) Discard(n int) (int, error) {
	if n <= 0 {
		return 0, nil
	}
	if l.n <= 0 {
		return 0, io.EOF
	}
	if int64(n) > l.n {
		n = int(l.n)
	}
	d, err := l.br.Discard(n)
	l.n -= int64(d)
	return d, err
}

// decodeNodeListStream reads a protobuf-encoded v1.NodeList ("k8s\x00" + runtime.Unknown) from r
// one Node at a time and returns the list's resourceVersion. Only one Node's bytes are held at once.
func decodeNodeListStream(r io.Reader, fn func(name string, n compactNode)) (string, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var magic [4]byte
	if _, err := io.ReadFull(br, magic[:]); err != nil {
		return "", fmt.Errorf("read protobuf magic: %w", err)
	}
	if !bytes.Equal(magic[:], protobufMagic) {
		return "", fmt.Errorf("response is not protobuf (prefix %q)", magic)
	}
	var rv string
	var sawList bool
	for {
		num, typ, err := readTag(br)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if num == unknownRawField && typ == protowire.BytesType {
			l, err := readFieldLength(br)
			if err != nil {
				return "", err
			}
			if l > math.MaxInt64 {
				return "", fmt.Errorf("protobuf NodeList length %d overflows int64", l)
			}
			lr := &limitedBufReader{br: br, n: int64(l)}
			if rv, err = decodeNodeListBody(lr, fn); err != nil {
				return "", err
			}
			if lr.n != 0 {
				return "", io.ErrUnexpectedEOF
			}
			sawList = true
			continue
		}
		if err := skipField(br, typ); err != nil {
			return "", err
		}
	}
	if !sawList {
		return "", errors.New("protobuf envelope has no raw NodeList")
	}
	return rv, nil
}

func decodeNodeListBody(r protoStreamReader, fn func(name string, n compactNode)) (string, error) {
	var rv string
	var buf []byte
	for {
		num, typ, err := readTag(r)
		if errors.Is(err, io.EOF) {
			return rv, nil
		}
		if err != nil {
			return "", err
		}
		if typ != protowire.BytesType || (num != nodeListMetadataField && num != nodeListItemsField) {
			if err := skipField(r, typ); err != nil {
				return "", err
			}
			continue
		}
		l, err := readFieldLength(r)
		if err != nil {
			return "", err
		}
		if l > compactMaxFrameBytes {
			return "", fmt.Errorf("list element of %d bytes exceeds limit", l)
		}
		frameLen := int(l)
		if cap(buf) < frameLen {
			buf = make([]byte, frameLen)
		}
		buf = buf[:frameLen]
		if _, err := io.ReadFull(r, buf); err != nil {
			if errors.Is(err, io.EOF) {
				return "", io.ErrUnexpectedEOF
			}
			return "", err
		}
		if num == nodeListMetadataField {
			rvBytes, err := scanBytesField(buf, listMetaResourceVersionField)
			if err != nil {
				return "", fmt.Errorf("scan ListMeta: %w", err)
			}
			rv = string(rvBytes)
			continue
		}
		name, _, n, err := scanNode(buf)
		if err != nil {
			return "", err
		}
		if len(name) == 0 {
			return "", errors.New("NodeList item missing node name")
		}
		fn(string(name), n)
	}
}

// decodeWatchStream reads length-delimited protobuf WatchEvent frames and calls fn per event.
// Slices passed to fn alias the reusable frame buffer and are only valid during the callback.
func decodeWatchStream(r io.Reader, fn func(eventType, name, rv []byte, n compactNode) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var hdr [4]byte
	var buf []byte
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return err
		}
		l := binary.BigEndian.Uint32(hdr[:])
		if l > compactMaxFrameBytes {
			return fmt.Errorf("watch frame of %d bytes exceeds limit", l)
		}
		frameLen := int(l)
		if cap(buf) < frameLen {
			buf = make([]byte, frameLen)
		}
		buf = buf[:frameLen]
		if _, err := io.ReadFull(br, buf); err != nil {
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		event, err := unwrapUnknown(buf)
		if err != nil {
			return fmt.Errorf("unwrap WatchEvent envelope: %w", err)
		}
		eventType, err := scanBytesField(event, watchEventTypeField)
		if err != nil {
			return fmt.Errorf("scan WatchEvent.type: %w", err)
		}
		rawExt, err := scanBytesField(event, watchEventObjectField)
		if err != nil {
			return fmt.Errorf("scan WatchEvent.object: %w", err)
		}
		rawObj, err := scanBytesField(rawExt, rawExtensionRawField)
		if err != nil {
			return fmt.Errorf("scan RawExtension.raw: %w", err)
		}
		obj, err := unwrapUnknown(rawObj)
		if err != nil {
			return fmt.Errorf("unwrap WatchEvent object: %w", err)
		}
		var name, rv []byte
		var n compactNode
		if string(eventType) != "ERROR" {
			if len(obj) == 0 {
				return fmt.Errorf("watch %s event has empty object payload", eventType)
			}
			if name, rv, n, err = scanNode(obj); err != nil {
				return err
			}
		}
		if err := fn(eventType, name, rv, n); err != nil {
			return err
		}
	}
}

// unwrapUnknown strips the "k8s\x00" + runtime.Unknown envelope if present and returns Unknown.raw.
func unwrapUnknown(b []byte) ([]byte, error) {
	if len(b) >= len(protobufMagic) && bytes.Equal(b[:len(protobufMagic)], protobufMagic) {
		return scanBytesField(b[len(protobufMagic):], unknownRawField)
	}
	return b, nil
}

// scanNode extracts name, resourceVersion and the compact record from a serialized v1.Node.
// Returned slices alias b.
func scanNode(b []byte) (name, rv []byte, n compactNode, err error) {
	for len(b) > 0 {
		num, typ, l := protowire.ConsumeTag(b)
		if l < 0 {
			return nil, nil, n, protowire.ParseError(l)
		}
		b = b[l:]
		if typ != protowire.BytesType || (num != nodeMetadataField && num != nodeSpecField && num != nodeStatusField) {
			if l = protowire.ConsumeFieldValue(num, typ, b); l < 0 {
				return nil, nil, n, protowire.ParseError(l)
			}
			b = b[l:]
			continue
		}
		v, l := protowire.ConsumeBytes(b)
		if l < 0 {
			return nil, nil, n, protowire.ParseError(l)
		}
		b = b[l:]
		switch num {
		case nodeMetadataField:
			name, rv, err = scanObjectMeta(v)
			if err != nil {
				return nil, nil, n, fmt.Errorf("scan ObjectMeta: %w", err)
			}
		case nodeSpecField:
			unsched, sErr := scanNodeSpec(v)
			if sErr != nil {
				return nil, nil, n, fmt.Errorf("scan NodeSpec: %w", sErr)
			}
			n.unschedulable = unsched
		case nodeStatusField:
			if err = scanNodeStatus(v, &n); err != nil {
				return nil, nil, n, err
			}
		}
	}
	return name, rv, n, nil
}

func scanNodeSpec(b []byte) (bool, error) {
	var unschedulable bool
	for len(b) > 0 {
		num, typ, l := protowire.ConsumeTag(b)
		if l < 0 {
			return false, protowire.ParseError(l)
		}
		b = b[l:]
		if num == nodeSpecUnschedulableField && typ == protowire.VarintType {
			v, l := protowire.ConsumeVarint(b)
			if l < 0 {
				return false, protowire.ParseError(l)
			}
			unschedulable = v != 0
			b = b[l:]
			continue
		}
		if l = protowire.ConsumeFieldValue(num, typ, b); l < 0 {
			return false, protowire.ParseError(l)
		}
		b = b[l:]
	}
	return unschedulable, nil
}

// parseMilliCPU parses a Kubernetes CPU Quantity string into millicores. Plain non-negative
// integer cores ("4") and millicores ("3920m") are parsed directly from b with zero allocations;
// all other formats fall back to resource.ParseQuantity.
func parseMilliCPU(b []byte) (int64, error) {
	if len(b) == 0 {
		return 0, nil
	}
	milli := b[len(b)-1] == 'm'
	digits := b
	if milli {
		digits = b[:len(b)-1]
	}
	if len(digits) > 0 {
		var v int64
		ok := true
		for _, c := range digits {
			if c < '0' || c > '9' {
				ok = false
				break
			}
			d := int64(c - '0')
			if v > (math.MaxInt64-d)/10 {
				ok = false
				break
			}
			v = v*10 + d
		}
		if ok {
			if milli {
				return v, nil
			}
			if v <= math.MaxInt64/1000 {
				return v * 1000, nil
			}
		}
	}
	q, err := resource.ParseQuantity(string(b))
	if err != nil {
		return 0, err
	}
	return q.MilliValue(), nil
}

func scanNodeStatus(b []byte, n *compactNode) error {
	for len(b) > 0 {
		num, typ, l := protowire.ConsumeTag(b)
		if l < 0 {
			return protowire.ParseError(l)
		}
		b = b[l:]
		if typ != protowire.BytesType || (num != nodeStatusAllocatableField && num != nodeStatusConditionsField) {
			if l = protowire.ConsumeFieldValue(num, typ, b); l < 0 {
				return protowire.ParseError(l)
			}
			b = b[l:]
			continue
		}
		v, l := protowire.ConsumeBytes(b)
		if l < 0 {
			return protowire.ParseError(l)
		}
		b = b[l:]
		switch num {
		case nodeStatusAllocatableField:
			k, err := scanBytesField(v, allocatableEntryKeyField)
			if err != nil {
				return fmt.Errorf("scan allocatable key: %w", err)
			}
			if string(k) == "cpu" {
				qtyMsg, err := scanBytesField(v, allocatableEntryValueField)
				if err != nil {
					return fmt.Errorf("scan allocatable cpu quantity: %w", err)
				}
				qtyStr, err := scanBytesField(qtyMsg, quantityStringField)
				if err != nil {
					return fmt.Errorf("scan allocatable cpu string: %w", err)
				}
				milli, err := parseMilliCPU(qtyStr)
				if err != nil {
					return fmt.Errorf("parse allocatable cpu: %w", err)
				}
				n.milliCPU = milli
			}
		case nodeStatusConditionsField:
			condType, err := scanBytesField(v, nodeConditionTypeField)
			if err != nil {
				return fmt.Errorf("scan NodeCondition.type: %w", err)
			}
			if string(condType) == "Ready" {
				condStatus, err := scanBytesField(v, nodeConditionStatusField)
				if err != nil {
					return fmt.Errorf("scan NodeCondition.status: %w", err)
				}
				n.ready = string(condStatus) == "True"
			}
		}
	}
	return nil
}

// scanObjectMeta extracts name and resourceVersion in a single pass. Returned slices alias b.
func scanObjectMeta(b []byte) (name, rv []byte, err error) {
	for len(b) > 0 {
		num, typ, l := protowire.ConsumeTag(b)
		if l < 0 {
			return nil, nil, protowire.ParseError(l)
		}
		b = b[l:]
		if typ == protowire.BytesType && (num == objectMetaNameField || num == objectMetaResourceVersionField) {
			v, l := protowire.ConsumeBytes(b)
			if l < 0 {
				return nil, nil, protowire.ParseError(l)
			}
			if num == objectMetaNameField {
				name = v
			} else {
				rv = v
			}
			b = b[l:]
			continue
		}
		if l = protowire.ConsumeFieldValue(num, typ, b); l < 0 {
			return nil, nil, protowire.ParseError(l)
		}
		b = b[l:]
	}
	return name, rv, nil
}

// scanBytesField returns the last occurrence of a length-delimited field, or nil if absent.
func scanBytesField(b []byte, want protowire.Number) ([]byte, error) {
	var out []byte
	for len(b) > 0 {
		num, typ, l := protowire.ConsumeTag(b)
		if l < 0 {
			return nil, protowire.ParseError(l)
		}
		b = b[l:]
		if num == want && typ == protowire.BytesType {
			v, l := protowire.ConsumeBytes(b)
			if l < 0 {
				return nil, protowire.ParseError(l)
			}
			out, b = v, b[l:]
			continue
		}
		if l = protowire.ConsumeFieldValue(num, typ, b); l < 0 {
			return nil, protowire.ParseError(l)
		}
		b = b[l:]
	}
	return out, nil
}

func readTag(r io.ByteReader) (protowire.Number, protowire.Type, error) {
	v, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, 0, err
	}
	num, typ := protowire.DecodeTag(v)
	if num < protowire.MinValidNumber {
		return 0, 0, fmt.Errorf("invalid protobuf field number %d", num)
	}
	return num, typ, nil
}

func readFieldLength(r io.ByteReader) (uint64, error) {
	l, err := binary.ReadUvarint(r)
	if errors.Is(err, io.EOF) {
		return 0, io.ErrUnexpectedEOF
	}
	return l, err
}

func discardExact(r protoStreamReader, n int64) error {
	for n > 0 {
		chunk := n
		if chunk > math.MaxInt32 {
			chunk = math.MaxInt32
		}
		d, err := r.Discard(int(chunk))
		n -= int64(d)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		if d == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}

func skipField(r protoStreamReader, typ protowire.Type) error {
	switch typ {
	case protowire.VarintType:
		if _, err := binary.ReadUvarint(r); err != nil {
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		return nil
	case protowire.Fixed32Type:
		return discardExact(r, 4)
	case protowire.Fixed64Type:
		return discardExact(r, 8)
	case protowire.BytesType:
		l, err := readFieldLength(r)
		if err != nil {
			return err
		}
		if l > math.MaxInt64 {
			return fmt.Errorf("protobuf field length %d overflows int64", l)
		}
		return discardExact(r, int64(l))
	default:
		return fmt.Errorf("unsupported protobuf wire type %d", typ)
	}
}
