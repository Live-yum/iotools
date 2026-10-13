package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"
)

type scanTestClient struct {
	rootClass  ua.NodeClass
	namespaces []string
	browse     func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error)
	next       func(context.Context, *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error)
	release    int
	browsed    []string
}

func (f *scanTestClient) Read(_ context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
	if len(req.NodesToRead) == 1 && req.NodesToRead[0].NodeID.IntID() == id.Server_NamespaceArray {
		names := f.namespaces
		if names == nil {
			names = []string{"http://opcfoundation.org/UA/", "urn:local", "urn:test"}
		}
		return &ua.ReadResponse{Results: []*ua.DataValue{{Value: ua.MustVariant(names)}}}, nil
	}
	class := f.rootClass
	if class == 0 {
		class = ua.NodeClassObject
	}
	return &ua.ReadResponse{Results: []*ua.DataValue{{Value: ua.MustVariant(int32(class))}, {Value: ua.MustVariant(&ua.LocalizedText{Text: "Root"})}, {Value: ua.MustVariant(&ua.QualifiedName{Name: "Root"})}}}, nil
}
func (f *scanTestClient) Browse(ctx context.Context, r *ua.BrowseRequest) (*ua.BrowseResponse, error) {
	f.browsed = append(f.browsed, r.NodesToBrowse[0].NodeID.String())
	if f.browse != nil {
		return f.browse(ctx, r)
	}
	return &ua.BrowseResponse{Results: []*ua.BrowseResult{{}}}, nil
}
func (f *scanTestClient) BrowseNext(ctx context.Context, r *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
	if r.ReleaseContinuationPoints {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		f.release++
		return &ua.BrowseNextResponse{}, nil
	}
	if f.next != nil {
		return f.next(ctx, r)
	}
	return nil, errors.New("unexpected BrowseNext")
}
func scanRef(id string, class ua.NodeClass) *ua.ReferenceDescription {
	node, _ := ua.ParseNodeID(id)
	return &ua.ReferenceDescription{NodeID: ua.NewExpandedNodeID(node, "", 0), ReferenceTypeID: ua.NewNumericNodeID(0, 47), IsForward: true, NodeClass: class, DisplayName: &ua.LocalizedText{Text: id}, BrowseName: &ua.QualifiedName{Name: id}}
}
func scanEvents(f *scanTestClient, params map[string]any) ([]map[string]any, map[string]any, error) {
	var nodes []map[string]any
	var summary map[string]any
	err := opcuaScan(context.Background(), f, config.Request{Action: "scan", Params: params}, func(e Event) {
		if e.Kind == "scan-node" {
			nodes = append(nodes, e.Data.(map[string]any))
		}
		if e.Kind == "scan-summary" {
			summary = e.Data.(map[string]any)
		}
	})
	return nodes, summary, err
}
func TestOPCUAScanPaginationCycleAndScope(t *testing.T) {
	f := &scanTestClient{}
	f.browse = func(_ context.Context, r *ua.BrowseRequest) (*ua.BrowseResponse, error) {
		q := r.NodesToBrowse[0]
		if q.BrowseDirection != ua.BrowseDirectionForward || q.ReferenceTypeID.String() != "i=33" || !q.IncludeSubtypes {
			t.Fatal("scan lost hierarchical forward filters")
		}
		if q.NodeID.String() == "i=85" {
			return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{scanRef("ns=2;s=Folder", ua.NodeClassObject)}, ContinuationPoint: []byte{1}}}}, nil
		}
		if q.NodeID.String() == "ns=2;s=Folder" {
			return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{scanRef("i=85", ua.NodeClassObject), scanRef("ns=2;s=Value", ua.NodeClassVariable)}}}}, nil
		}
		return &ua.BrowseResponse{Results: []*ua.BrowseResult{{}}}, nil
	}
	f.next = func(context.Context, *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
		foreign := scanRef("ns=2;s=Foreign", ua.NodeClassVariable)
		foreign.NodeID.ServerIndex = 1
		uri := scanRef("i=100", ua.NodeClassVariable)
		uri.NodeID.NamespaceURI = "urn:test"
		inverse := scanRef("ns=2;s=Parent", ua.NodeClassObject)
		inverse.IsForward = false
		return &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{scanRef("ns=2;s=Value", ua.NodeClassVariable), foreign, uri, inverse}}}}, nil
	}
	nodes, summary, err := scanEvents(f, nil)
	if err != nil || len(nodes) != 4 || summary["variables"] != 2 || summary["complete"] != true || summary["duplicate_references"] != 2 || summary["skipped_external"] != 1 {
		t.Fatalf("nodes=%v summary=%v err=%v", nodes, summary, err)
	}
	if nodes[0]["parent_id"] != "" || nodes[2]["node_class"] != "Variable" || nodes[2]["depth"] != 1 {
		t.Fatal(nodes)
	}
	if f.release != 0 || len(f.browsed) != 4 {
		t.Fatal(f)
	}
}
func TestOPCUAScanLimitsCancellationAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
		cancel bool
	}{
		{"nodes", map[string]any{"max_nodes": 1}, false},
		{"references", map[string]any{"max_references": 1}, false},
		{"cancel", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &scanTestClient{}
			f.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
				if tc.cancel {
					cancel()
				}
				return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{scanRef("i=100", ua.NodeClassVariable), scanRef("i=101", ua.NodeClassVariable)}, ContinuationPoint: []byte{1}}}}, nil
			}
			f.next = func(context.Context, *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
				return &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{}}}, nil
			}
			var summary map[string]any
			err := opcuaScan(ctx, f, config.Request{Action: "scan", Params: tc.params}, func(e Event) {
				if e.Kind == "scan-summary" {
					summary = e.Data.(map[string]any)
				}
			})
			if err == nil || summary["complete"] != false {
				t.Fatalf("%v %v", err, summary)
			}
			if f.release == 0 {
				t.Fatalf("continuation not released: %d", f.release)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestOPCUAScanDeepChainOptionalDepthLimitAndCycle(t *testing.T) {
	const terminalDepth = 600
	for _, tc := range []struct {
		name       string
		params     map[string]any
		wantNodes  int
		wantReason string
	}{
		{"default unlimited", nil, terminalDepth + 1, ""},
		{"explicit unlimited", map[string]any{"max_depth": 0}, terminalDepth + 1, ""},
		{"depth one", map[string]any{"max_depth": 1}, 2, "max_depth=1"},
		{"depth thirty two", map[string]any{"max_depth": 32}, 33, "max_depth=32"},
		{"depth two hundred fifty six", map[string]any{"max_depth": 256}, 257, "max_depth=256"},
		{"unlimited retains node limit", map[string]any{"max_depth": 0, "max_nodes": 512}, 512, "max_nodes=512"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &scanTestClient{}
			f.browse = func(_ context.Context, r *ua.BrowseRequest) (*ua.BrowseResponse, error) {
				parent := r.NodesToBrowse[0].NodeID
				depth := 0
				if parent.Namespace() == 2 {
					depth = int(parent.IntID())
				}
				if depth == terminalDepth {
					// A back-edge from the deepest node must still terminate when
					// there is no depth cap.
					return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{scanRef("i=85", ua.NodeClassObject)}}}}, nil
				}
				class := ua.NodeClassObject
				if depth+1 == terminalDepth {
					class = ua.NodeClassVariable
				}
				child := ua.NewNumericNodeID(2, uint32(depth+1)).String()
				return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{scanRef(child, class)}}}}, nil
			}
			nodes, summary, err := scanEvents(f, tc.params)
			if len(nodes) != tc.wantNodes || summary["nodes"] != tc.wantNodes || len(f.browsed) != tc.wantNodes {
				t.Fatalf("nodes=%d browsed=%d summary=%v err=%v", len(nodes), len(f.browsed), summary, err)
			}
			last := nodes[len(nodes)-1]
			if last["depth"] != tc.wantNodes-1 {
				t.Fatalf("last depth=%v", last["depth"])
			}
			if tc.wantReason != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantReason) || summary["complete"] != false || !strings.Contains(summary["reason"].(string), tc.wantReason) || summary["variables"] != 0 {
					t.Fatalf("limit not reported as partial: summary=%v err=%v", summary, err)
				}
				return
			}
			if err != nil || summary["complete"] != true || summary["reason"] != "" || summary["variables"] != 1 || summary["duplicate_references"] != 1 {
				t.Fatalf("deep scan did not complete: summary=%v err=%v", summary, err)
			}
			if last["node_id"] != "ns=2;i=600" || last["node_class"] != "Variable" {
				t.Fatalf("terminal variable missing: %v", last)
			}
		})
	}
}
func TestOPCUAScanRootVariableAndEmptyPaging(t *testing.T) {
	f := &scanTestClient{rootClass: ua.NodeClassVariable}
	nodes, summary, err := scanEvents(f, map[string]any{"node_id": "ns=2;s=CaseSensitive"})
	if err != nil || len(nodes) != 1 || nodes[0]["node_class"] != "Variable" || summary["variables"] != 1 {
		t.Fatalf("%v %v %v", nodes, summary, err)
	}
	f.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
		return &ua.BrowseResponse{Results: []*ua.BrowseResult{{ContinuationPoint: []byte{1}}}}, nil
	}
	f.next = func(context.Context, *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
		return &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{ContinuationPoint: []byte{1}}}}, nil
	}
	_, summary, err = scanEvents(f, nil)
	if err == nil || !strings.Contains(err.Error(), "empty page") || summary["complete"] != false || f.release != 1 {
		t.Fatalf("%v %v releases=%d", err, summary, f.release)
	}
}

type batchTestReader struct {
	calls   []int
	max     int
	failure string
	stamp   time.Time
}

func (f *batchTestReader) Read(_ context.Context, r *ua.ReadRequest) (*ua.ReadResponse, error) {
	f.calls = append(f.calls, len(r.NodesToRead))
	if f.max > 0 && len(r.NodesToRead) > f.max {
		return nil, ua.StatusBadTooManyOperations
	}
	result := &ua.ReadResponse{}
	for _, n := range r.NodesToRead {
		v := &ua.DataValue{Value: ua.MustVariant(int32(7)), SourceTimestamp: f.stamp, ServerTimestamp: f.stamp}
		if n.NodeID.String() == f.failure {
			v.Status = ua.StatusBadNodeIDUnknown
		}
		result.Results = append(result.Results, v)
	}
	return result, nil
}
func TestOPCUAReadBatchesAdaptiveAndPartial(t *testing.T) {
	f := &batchTestReader{max: 2, failure: "ns=2;s=Item2", stamp: time.Now().UTC()}
	ids := []string{}
	for i := 0; i < 7; i++ {
		ids = append(ids, fmt.Sprintf("ns=2;s=Item%d", i))
	}
	var events []Event
	err := opcuaReadAttributes(context.Background(), f, config.Request{Action: "read", Params: map[string]any{"node_ids": ids, "read_batch_size": 5}}, func(e Event) { events = append(events, e) })
	if err == nil || len(events) != 7 || !reflect.DeepEqual(f.calls, []int{5, 2, 2, 2, 1}) {
		t.Fatalf("%v %d %v", err, len(events), f.calls)
	}
	for i, event := range events {
		d := event.Data.(map[string]any)
		if d["node_id"] != ids[i] || d["source_timestamp"] != f.stamp || d["server_timestamp"] != f.stamp {
			t.Fatal(d)
		}
	}
	if events[2].Data.(map[string]any)["status"] == ua.StatusOK.Error() {
		t.Fatal("bad per-node status was lost")
	}
}

type batchTestMonitor struct {
	calls   []int
	handles []uint32
}

func (f *batchTestMonitor) Monitor(_ context.Context, _ ua.TimestampsToReturn, items ...*ua.MonitoredItemCreateRequest) (*ua.CreateMonitoredItemsResponse, error) {
	f.calls = append(f.calls, len(items))
	if len(items) > 40 {
		return nil, ua.StatusBadTooManyOperations
	}
	r := &ua.CreateMonitoredItemsResponse{}
	for _, item := range items {
		f.handles = append(f.handles, item.RequestedParameters.ClientHandle)
		r.Results = append(r.Results, &ua.MonitoredItemCreateResult{})
	}
	return r, nil
}
func TestOPCUAMonitorBatchesKeepGlobalHandles(t *testing.T) {
	f := &batchTestMonitor{}
	items := []*ua.MonitoredItemCreateRequest{}
	for i := 1; i <= 1000; i++ {
		items = append(items, opcua.NewMonitoredItemCreateRequestWithDefaults(ua.NewNumericNodeID(2, uint32(i)), ua.AttributeIDValue, uint32(i)))
	}
	if err := opcuaMonitorBatches(context.Background(), f, items); err != nil {
		t.Fatal(err)
	}
	if len(f.handles) != 1000 || f.calls[0] != 100 || f.calls[1] != 50 || f.calls[2] != 25 {
		t.Fatal(f.calls)
	}
	for i, h := range f.handles {
		if h != uint32(i+1) {
			t.Fatal("handle changed across monitor batches")
		}
	}
}
func TestOPCUAScanAndBatchValidationBeforeConnection(t *testing.T) {
	for _, p := range []map[string]any{{"max_depth": -1}, {"max_depth": 257}, {"max_nodes": 0}, {"max_nodes": 100001}, {"read_batch_size": 0}, {"read_batch_size": 1001}, {"max_depth": 1.5}, {"expected_namespace_fingerprint": ""}, {"expected_namespace_fingerprint": strings.Repeat("z", 64)}, {"expected_namespace_fingerprint": 123}} {
		err := Run(context.Background(), config.Request{Protocol: "opcua", Action: "scan", Endpoint: "opc.tcp://127.0.0.1:1", Params: p}, false, nil)
		if err == nil || strings.Contains(err.Error(), "discovery") {
			t.Fatalf("invalid config reached network: %v", err)
		}
	}
	for _, a := range []struct {
		action string
		limit  int
	}{{"read", 10000}, {"subscribe", 1000}, {"attributes", 256}} {
		names := make([]string, a.limit+1)
		for i := range names {
			names[i] = "i=85"
		}
		if _, err := opcuaNodes(config.Request{Action: a.action, Params: map[string]any{"node_ids": names}}); err == nil {
			t.Fatal(a.action)
		}
		if _, err := opcuaNodes(config.Request{Action: a.action, Params: map[string]any{"node_ids": names[:a.limit]}}); err != nil {
			t.Fatal(err)
		}
	}
}

// This is a real OPC UA wire/session integration on a disposable loopback
// server. It is not validation against a production or industrial device.
func TestOPCUALoopbackScanReadAndMultiSubscribe(t *testing.T) {
	endpoint, ns, security := localUAServer(t, false)
	run := func(action string, extra map[string]any) ([]Event, error) {
		p := map[string]any{}
		for k, v := range security {
			p[k] = v
		}
		for k, v := range extra {
			p[k] = v
		}
		var events []Event
		err := Run(context.Background(), config.Request{Protocol: "opcua", Action: action, Endpoint: endpoint, Timeout: "15s", Params: p}, false, func(e Event) { events = append(events, e) })
		return events, err
	}
	events, err := run("scan", map[string]any{"node_id": ua.NewNumericNodeID(ns.ID(), id.ObjectsFolder).String()})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	fingerprint := ""
	for _, e := range events {
		if e.Kind == "scan-namespace" {
			fingerprint = e.Data.(map[string]any)["fingerprint"].(string)
		}
		if e.Kind == "scan-node" {
			d := e.Data.(map[string]any)
			if d["node_class"] == "Variable" {
				names = append(names, d["node_id"].(string))
			}
		}
	}
	if len(names) != 206 {
		t.Fatalf("scan discovered %d variables", len(names))
	}
	summary := events[len(events)-1]
	if summary.Kind != "scan-summary" || summary.Data.(map[string]any)["complete"] != true {
		t.Fatal(summary)
	}
	events, err = run("read", map[string]any{"node_ids": names, "read_batch_size": 37, "expected_namespace_fingerprint": fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range events {
		if e.Kind == "value" {
			count++
		}
	}
	if count != 206 {
		t.Fatal(count)
	}
	events, err = run("scan", map[string]any{"node_id": names[0]})
	if err != nil {
		t.Fatal(err)
	}
	if events[len(events)-1].Data.(map[string]any)["variables"] != 1 {
		t.Fatal(events)
	}
	events, err = run("subscribe", map[string]any{"node_ids": names[:105], "interval_ms": 50, "max_events": 105, "expected_namespace_fingerprint": fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range events {
		if e.Kind == "notification" {
			seen[e.Data.(map[string]any)["node_id"].(string)] = true
		}
	}
	if len(seen) != 105 {
		t.Fatalf("monitored %v", seen)
	}
}

func TestOPCUAScanUnresolvedNamespaceAndBrowseFailureRemainPartial(t *testing.T) {
	f := &scanTestClient{}
	f.browse = func(_ context.Context, r *ua.BrowseRequest) (*ua.BrowseResponse, error) {
		switch r.NodesToBrowse[0].NodeID.String() {
		case "i=85":
			badURI := scanRef("ns=2;s=BadURI", ua.NodeClassVariable)
			badURI.NodeID.NamespaceURI = "urn:missing"
			return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{badURI, scanRef("ns=2;s=Denied", ua.NodeClassObject), scanRef("ns=2;s=Good", ua.NodeClassVariable)}}}}, nil
		case "ns=2;s=Denied":
			return &ua.BrowseResponse{Results: []*ua.BrowseResult{{StatusCode: ua.StatusBadUserAccessDenied}}}, nil
		default:
			return &ua.BrowseResponse{Results: []*ua.BrowseResult{{}}}, nil
		}
	}
	nodes, summary, err := scanEvents(f, nil)
	if err == nil || len(nodes) != 3 || summary["complete"] != false || summary["errors"] != 2 || !strings.Contains(summary["reason"].(string), "namespace URI") {
		t.Fatalf("%v %v %v", nodes, summary, err)
	}
	if !reflect.DeepEqual(f.browsed, []string{"i=85", "ns=2;s=Denied", "ns=2;s=Good"}) {
		t.Fatal(f.browsed)
	}
}

func TestOPCUAScanMalformedPaginationReleasesAllPoints(t *testing.T) {
	f := &scanTestClient{}
	f.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
		return &ua.BrowseResponse{Results: []*ua.BrowseResult{{ContinuationPoint: []byte{1}}, {ContinuationPoint: []byte{2}}}}, nil
	}
	released := [][]byte{}
	browser := &scanReleaseCapture{scanTestClient: f, points: &released}
	err := opcuaScanReferences(context.Background(), browser, ua.NewNumericNodeID(0, 85), 100, func(*ua.ReferenceDescription) error { return nil })
	if err == nil || !reflect.DeepEqual(released, [][]byte{{1}, {2}}) {
		t.Fatalf("%v %v", err, released)
	}
}

type scanReleaseCapture struct {
	*scanTestClient
	points *[][]byte
}

func (f *scanReleaseCapture) BrowseNext(ctx context.Context, r *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
	if r.ReleaseContinuationPoints {
		*f.points = append(*f.points, r.ContinuationPoints...)
	}
	return f.scanTestClient.BrowseNext(ctx, r)
}

type functionUAReader func(context.Context, *ua.ReadRequest) (*ua.ReadResponse, error)

func (f functionUAReader) Read(ctx context.Context, r *ua.ReadRequest) (*ua.ReadResponse, error) {
	return f(ctx, r)
}
func TestOPCUAReadBatchTransportFailureAndCancellation(t *testing.T) {
	ids := []string{"i=1", "i=2", "i=3", "i=4", "i=5"}
	calls := 0
	reader := functionUAReader(func(_ context.Context, r *ua.ReadRequest) (*ua.ReadResponse, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("read service failed")
		}
		result := &ua.ReadResponse{}
		for range r.NodesToRead {
			result.Results = append(result.Results, &ua.DataValue{Value: ua.MustVariant(int32(1))})
		}
		return result, nil
	})
	var values []map[string]any
	r := config.Request{Action: "read", Params: map[string]any{"node_ids": ids, "read_batch_size": 2}}
	err := opcuaReadAttributes(context.Background(), reader, r, func(e Event) { values = append(values, e.Data.(map[string]any)) })
	if err == nil || calls != 3 || len(values) != 5 || values[2]["status"] != "Unavailable" || values[4]["value"] != int32(1) {
		t.Fatalf("%v %d %v", err, calls, values)
	}
	calls = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = opcuaReadAttributes(ctx, reader, r, func(Event) { cancel() })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancel: %v calls=%d", err, calls)
	}
}
func TestOPCUAScanRootMetadataAdaptsToSmallReadLimit(t *testing.T) {
	calls := 0
	reader := functionUAReader(func(_ context.Context, r *ua.ReadRequest) (*ua.ReadResponse, error) {
		calls++
		if len(r.NodesToRead) > 1 {
			return nil, ua.StatusBadTooManyOperations
		}
		return &ua.ReadResponse{Results: []*ua.DataValue{{Value: ua.MustVariant(int32(r.NodesToRead[0].AttributeID))}}}, nil
	})
	attrs := []*ua.ReadValueID{{AttributeID: ua.AttributeIDNodeClass}, {AttributeID: ua.AttributeIDDisplayName}, {AttributeID: ua.AttributeIDBrowseName}}
	result, err := opcuaScanMetadata(context.Background(), reader, attrs)
	if err != nil || len(result) != 3 || calls != 5 {
		t.Fatalf("%v %v %d", result, err, calls)
	}
	for i, d := range result {
		if d.Value.Value() != int32(attrs[i].AttributeID) {
			t.Fatal("attribute order lost")
		}
	}
}
func TestOPCUALoopbackScanCancelledAfterProgress(t *testing.T) {
	endpoint, ns, params := localUAServer(t, false)
	params["node_id"] = ua.NewNumericNodeID(ns.ID(), id.ObjectsFolder).String()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	var summary map[string]any
	err := Run(ctx, config.Request{Protocol: "opcua", Action: "scan", Endpoint: endpoint, Timeout: "10s", Params: params}, false, func(e Event) {
		if e.Kind == "scan-node" {
			count++
			if count == 10 {
				cancel()
			}
		}
		if e.Kind == "scan-summary" {
			summary = e.Data.(map[string]any)
		}
	})
	if !errors.Is(err, context.Canceled) || count != 10 || summary["complete"] != false || summary["cancelled"] != true {
		t.Fatalf("%v %d %v", err, count, summary)
	}
}
func TestOPCUALoopbackPinnedSecureScan(t *testing.T) {
	endpoint, ns, params := localUAServer(t, true)
	params["node_id"] = ua.NewStringNodeID(ns.ID(), "Value").String()
	r := config.Request{Protocol: "opcua", Action: "scan", Endpoint: endpoint, Timeout: "15s", Params: params}
	if err := Run(context.Background(), r, false, nil); err != nil {
		t.Fatal(err)
	}
	params["server_cert_sha256"] = strings.Repeat("00", 32)
	count := 0
	err := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "scan-node" {
			count++
		}
	})
	if err == nil || !strings.Contains(err.Error(), "pin mismatch") || count != 0 {
		t.Fatalf("scan bypassed trust: %v nodes=%d", err, count)
	}
}

func TestOPCUAScanNamespaceURIPromotesCompactNumericIDs(t *testing.T) {
	for _, original := range []*ua.NodeID{ua.NewTwoByteNodeID(100), ua.NewFourByteNodeID(1, 300)} {
		t.Run(original.String(), func(t *testing.T) {
			names := make([]string, 301)
			for i := range names {
				names[i] = fmt.Sprintf("urn:unused:%d", i)
			}
			names[0] = "http://opcfoundation.org/UA/"
			names[300] = "urn:high"
			f := &scanTestClient{namespaces: names}
			f.browse = func(_ context.Context, r *ua.BrowseRequest) (*ua.BrowseResponse, error) {
				if r.NodesToBrowse[0].NodeID.String() != "i=85" {
					return &ua.BrowseResponse{Results: []*ua.BrowseResult{{}}}, nil
				}
				ref := scanRef("i=1", ua.NodeClassVariable)
				ref.NodeID = ua.NewExpandedNodeID(original, "urn:high", 0)
				return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{ref}}}}, nil
			}
			nodes, summary, err := scanEvents(f, nil)
			want := ua.NewNumericNodeID(300, original.IntID()).String()
			if err != nil || summary["complete"] != true || len(nodes) != 2 || nodes[1]["node_id"] != want {
				t.Fatalf("%v %v %v", nodes, summary, err)
			}
		})
	}
}

func TestOPCUAScanNamespaceFingerprintRejectsReordering(t *testing.T) {
	f := &scanTestClient{namespaces: []string{"http://opcfoundation.org/UA/", "urn:first", "urn:second"}}
	fingerprint := ""
	err := opcuaScan(context.Background(), f, config.Request{Action: "scan"}, func(e Event) {
		if e.Kind == "scan-namespace" {
			fingerprint = e.Data.(map[string]any)["fingerprint"].(string)
		}
	})
	if err != nil || len(fingerprint) != 64 {
		t.Fatalf("scan: %v fingerprint=%q", err, fingerprint)
	}
	if err := opcuaVerifyNamespace(context.Background(), f, fingerprint); err != nil {
		t.Fatalf("unchanged namespace rejected: %v", err)
	}
	f.namespaces[1], f.namespaces[2] = f.namespaces[2], f.namespaces[1]
	if err := opcuaVerifyNamespace(context.Background(), f, fingerprint); err == nil || !strings.Contains(err.Error(), "rescan required") {
		t.Fatalf("reordered namespace accepted: %v", err)
	}
	bad := functionUAReader(func(context.Context, *ua.ReadRequest) (*ua.ReadResponse, error) {
		return nil, ua.StatusBadUserAccessDenied
	})
	if err := opcuaVerifyNamespace(context.Background(), bad, fingerprint); err == nil {
		t.Fatal("unreadable NamespaceArray accepted")
	}
}
func TestOPCUALoopbackNamespaceMismatchBlocksReadAndSubscribe(t *testing.T) {
	endpoint, ns, params := localUAServer(t, false)
	params["node_id"] = ua.NewStringNodeID(ns.ID(), "Value").String()
	var fingerprint string
	r := config.Request{Protocol: "opcua", Action: "scan", Endpoint: endpoint, Timeout: "10s", Params: params}
	if err := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "scan-namespace" {
			fingerprint = e.Data.(map[string]any)["fingerprint"].(string)
		}
	}); err != nil {
		t.Fatal(err)
	}
	params["expected_namespace_fingerprint"] = fingerprint
	r.Action = "read"
	if err := Run(context.Background(), r, false, nil); err != nil {
		t.Fatalf("matching namespace rejected: %v", err)
	}
	params["expected_namespace_fingerprint"] = strings.Repeat("00", 32)
	params["max_events"] = 1
	for _, action := range []string{"read", "subscribe"} {
		r.Action = action
		values := 0
		err := Run(context.Background(), r, false, func(e Event) {
			if e.Kind == "value" || e.Kind == "notification" {
				values++
			}
		})
		if err == nil || !strings.Contains(err.Error(), "rescan required") || values != 0 {
			t.Fatalf("%s mismatch allowed: %v values=%d", action, err, values)
		}
	}
}

func TestOPCUANamespaceIdentityRejectsAmbiguousArrays(t *testing.T) {
	for _, names := range [][]string{{}, {""}, {"urn:same", "urn:same"}} {
		f := &scanTestClient{namespaces: names}
		if _, _, err := opcuaScanNamespaces(context.Background(), f); err == nil {
			t.Fatalf("ambiguous namespace table accepted: %v", names)
		}
	}
}
