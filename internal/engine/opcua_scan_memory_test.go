package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"
)

func TestOPCUAScanMemoryCumulativeNamesStopFrontierAndRelease(t *testing.T) {
	// Every reference shares one small fixture allocation. The old source can
	// fail this regression without allocating a genuinely enormous address space.
	name := strings.Repeat("x", 128<<10)
	f := &scanTestClient{}
	f.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
		refs := make([]*ua.ReferenceDescription, 64)
		for i := range refs {
			refs[i] = scanRef(fmt.Sprintf("ns=2;i=%d", i+1), ua.NodeClassVariable)
			refs[i].DisplayName.Text, refs[i].BrowseName.Name = name, name
		}
		return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: refs, ContinuationPoint: []byte{7}}}}, nil
	}
	f.next = func(context.Context, *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
		return &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{}}}, nil
	}
	nodes, summary, err := scanEvents(f, nil)
	if !errors.Is(err, errOPCUAScanMemoryLimit) || len(nodes) <= 1 || len(nodes) >= 65 || summary["complete"] != false || !strings.Contains(summary["reason"].(string), "memory budget") || len(f.browsed) != 1 || f.release != 1 {
		t.Fatalf("nodes=%d browsed=%d releases=%d complete=%v err=%v", len(nodes), len(f.browsed), f.release, summary["complete"], err)
	}
	if got := nodes[1]["display_name"].(string); got != name || unsafe.StringData(got) == unsafe.StringData(name) {
		t.Fatal("admitted metadata not losslessly detached")
	}
}

func TestOPCUAScanMemoryRejectsOversizedMetadataAndIDs(t *testing.T) {
	huge := strings.Repeat("x", int(opcuaScanMemoryLimit/32)+1)
	for _, which := range []string{"display", "browse", "string-id", "opaque-id", "namespace-uri", "error"} {
		t.Run(which, func(t *testing.T) {
			f := &scanTestClient{}
			f.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
				if which == "error" {
					return nil, errors.New(huge)
				}
				ref := scanRef("ns=2;i=1", ua.NodeClassVariable)
				switch which {
				case "display":
					ref.DisplayName.Text = huge
				case "browse":
					ref.BrowseName.Name = huge
				case "string-id":
					ref.NodeID = ua.NewExpandedNodeID(ua.NewStringNodeID(2, huge), "", 0)
				case "opaque-id":
					ref.NodeID = ua.NewExpandedNodeID(ua.NewByteStringNodeID(2, []byte(huge)), "", 0)
				case "namespace-uri":
					ref.NodeID.NamespaceURI = huge
				}
				return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{ref}, ContinuationPoint: []byte{1}}}}, nil
			}
			f.next = func(context.Context, *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
				return &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{}}}, nil
			}
			nodes, summary, err := scanEvents(f, map[string]any{"max_nodes": 2})
			if !errors.Is(err, errOPCUAScanMemoryLimit) || len(nodes) != 1 || summary["complete"] != false || len(f.browsed) != 1 {
				t.Fatalf("nodes=%d browsed=%d complete=%v memory error=%v", len(nodes), len(f.browsed), summary["complete"], errors.Is(err, errOPCUAScanMemoryLimit))
			}
			if which != "error" && f.release != 1 {
				t.Fatalf("release=%d", f.release)
			}
			if len(summary["reason"].(string)) > 1024 {
				t.Fatal("unbounded error retained")
			}
		})
	}
}

type scanMetadataTestClient struct {
	*scanTestClient
	display, browse string
}

func (f *scanMetadataTestClient) Read(ctx context.Context, r *ua.ReadRequest) (*ua.ReadResponse, error) {
	if len(r.NodesToRead) == 1 && r.NodesToRead[0].NodeID.IntID() == id.Server_NamespaceArray {
		return f.scanTestClient.Read(ctx, r)
	}
	return &ua.ReadResponse{Results: []*ua.DataValue{{Value: ua.MustVariant(int32(ua.NodeClassObject))}, {Value: ua.MustVariant(&ua.LocalizedText{Text: f.display})}, {Value: ua.MustVariant(&ua.QualifiedName{Name: f.browse})}}}, nil
}
func TestOPCUAScanMemoryRootMetadataAndRootID(t *testing.T) {
	huge := strings.Repeat("x", int(opcuaScanMemoryLimit/32)+1)
	for _, which := range []string{"display", "browse", "root-id"} {
		t.Run(which, func(t *testing.T) {
			f := &scanMetadataTestClient{scanTestClient: &scanTestClient{}, display: "root", browse: "root"}
			params := map[string]any{}
			switch which {
			case "display":
				f.display = huge
			case "browse":
				f.browse = huge
			case "root-id":
				params["node_id"] = "ns=2;s=" + huge
			}
			nodes := 0
			var summary map[string]any
			err := opcuaScan(context.Background(), f, config.Request{Params: params}, func(e Event) {
				if e.Kind == "scan-node" {
					nodes++
				}
				if e.Kind == "scan-summary" {
					summary = e.Data.(map[string]any)
				}
			})
			if !errors.Is(err, errOPCUAScanMemoryLimit) || nodes != 0 || len(f.browsed) != 0 || summary["complete"] != false {
				t.Fatalf("nodes=%d browsed=%d complete=%v memory error=%v", nodes, len(f.browsed), summary["complete"], errors.Is(err, errOPCUAScanMemoryLimit))
			}
		})
	}
}

func TestOPCUAScanMemoryNamespaceAdmissionOwnershipAndFingerprint(t *testing.T) {
	backing := strings.Repeat("x", 1<<20) + "urn:<a>\n\"/雪"
	uri := backing[len(backing)-len("urn:<a>\n\"/雪"):]
	names := []string{"http://opcfoundation.org/UA/", uri}
	f := &scanTestClient{namespaces: names}
	got, fingerprint, err := opcuaScanNamespaces(context.Background(), f)
	encoded, _ := json.Marshal(names)
	sum := sha256.Sum256(encoded)
	if err != nil || fingerprint != hex.EncodeToString(sum[:]) || got[uri] != 1 {
		t.Fatalf("namespace/fingerprint changed: %v", err)
	}
	for key := range got {
		if key == uri && unsafe.StringData(key) == unsafe.StringData(uri) {
			t.Error("namespace key aliases response backing")
		}
	}
	if err := opcuaVerifyNamespace(context.Background(), f, fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := opcuaVerifyNamespace(context.Background(), f, strings.Repeat("0", 64)); err == nil {
		t.Fatal("identity mismatch accepted")
	}
	for _, which := range []string{"single", "aggregate"} {
		t.Run(which, func(t *testing.T) {
			names := []string{strings.Repeat("x", int(opcuaScanMemoryLimit/32)+1)}
			if which == "aggregate" {
				base := strings.Repeat("x", 128<<10)
				names = nil
				for i := 0; i < 40; i++ {
					names = append(names, fmt.Sprintf("%d:%s", i, base))
				}
			}
			result, _, err := opcuaScanNamespaces(context.Background(), &scanTestClient{namespaces: names})
			if !errors.Is(err, errOPCUAScanMemoryLimit) || result != nil {
				t.Fatalf("rejected=%v map=%d", errors.Is(err, errOPCUAScanMemoryLimit), len(result))
			}
		})
	}
}

func TestOPCUAScanMemoryContinuationAdmissionAndCancellation(t *testing.T) {
	for _, which := range []string{"single", "aggregate", "cancel"} {
		t.Run(which, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			size := 256 << 10
			if which == "single" {
				size = int(opcuaScanMemoryLimit/32) + 1
			}
			token := bytes.Repeat([]byte{1}, size)
			f := &scanTestClient{}
			pages := 0
			f.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
				if which == "cancel" {
					cancel()
				}
				return &ua.BrowseResponse{Results: []*ua.BrowseResult{{ContinuationPoint: token}}}, nil
			}
			f.next = func(_ context.Context, r *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
				if len(r.ContinuationPoints[0]) > 0 && &r.ContinuationPoints[0][0] == &token[0] {
					t.Fatal("pagination token aliases response")
				}
				pages++
				if pages == 64 {
					return &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{}}}, nil
				}
				token[0] = byte(pages + 1)
				return &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{ContinuationPoint: token}}}, nil
			}
			err := opcuaScanReferences(ctx, f, ua.NewNumericNodeID(0, 85), 100, func(*ua.ReferenceDescription) error { return nil })
			if which == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, errOPCUAScanMemoryLimit) {
				t.Fatalf("memory rejection=%v pages=%d", errors.Is(err, errOPCUAScanMemoryLimit), pages)
			}
			wantReleases := 1
			if which == "single" {
				wantReleases = 0
				if !errors.Is(err, errOPCUAScanReleaseLimit) || !strings.Contains(err.Error(), "not released") {
					t.Fatal("oversized token release omission was not explicit")
				}
			}
			if f.release != wantReleases || pages >= 64 {
				t.Fatalf("release=%d pages=%d", f.release, pages)
			}
		})
	}
}

func TestOPCUAScanMemoryExactBoundaryAndComplexIDs(t *testing.T) {
	for _, id := range []*ua.NodeID{ua.NewTwoByteNodeID(7), ua.NewFourByteNodeID(2, 8), ua.NewNumericNodeID(300, 99999), ua.NewStringNodeID(2, "a;雪"), ua.NewGUIDNodeID(2, "00112233-4455-6677-8899-aabbccddeeff"), ua.NewByteStringNodeID(2, []byte{0, 1, 255})} {
		b := opcuaScanMemory{remaining: opcuaScanMemoryLimit}
		owned, key, err := b.ownNodeID(id)
		if err != nil || key != id.String() || owned.String() != key {
			t.Fatalf("NodeID changed: %v", err)
		}
		needed := opcuaScanMemoryLimit - b.remaining
		for _, delta := range []int64{-1, 0, 1} {
			b := opcuaScanMemory{remaining: needed + delta}
			_, _, err := b.ownNodeID(id)
			if (err == nil) != (delta >= 0) {
				t.Fatalf("boundary %d err=%v", delta, err)
			}
			if delta < 0 && b.remaining != needed+delta {
				t.Fatal("failed admission charged budget")
			}
		}
	}
	backing := bytes.Repeat([]byte{8}, 4096)
	id := ua.NewByteStringNodeID(2, backing[2000:2002])
	b := opcuaScanMemory{remaining: opcuaScanMemoryLimit}
	owned, key, err := b.ownNodeID(id)
	backing[2000] = 9
	if err != nil || owned.String() != key || owned.String() == id.String() {
		t.Fatal("opaque NodeID backing not detached")
	}
	names := strings.Repeat("x", 4096)
	input := uaScanNode{display: names[2000:2002], browse: names[2001:2003], parent: names[2002:2004]}
	out, err := b.ownNames(input)
	if err != nil || out.display != input.display || unsafe.StringData(out.display) == unsafe.StringData(input.display) || unsafe.StringData(out.browse) == unsafe.StringData(input.browse) || unsafe.StringData(out.parent) == unsafe.StringData(input.parent) {
		t.Fatal("metadata backing not detached")
	}
}

func TestOPCUAScanMemoryTerminalReasonSurvivesEarlierErrors(t *testing.T) {
	f := &scanTestClient{}
	f.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
		refs := []*ua.ReferenceDescription{}
		for i := 0; i < 10; i++ {
			r := scanRef(fmt.Sprintf("i=%d", i+1), ua.NodeClassVariable)
			r.NodeID.NamespaceURI = "urn:missing"
			refs = append(refs, r)
		}
		r := scanRef("i=20", ua.NodeClassVariable)
		r.DisplayName.Text = strings.Repeat("x", int(opcuaScanMemoryLimit/32)+1)
		refs = append(refs, r)
		return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: refs}}}, nil
	}
	_, summary, err := scanEvents(f, nil)
	if !errors.Is(err, errOPCUAScanMemoryLimit) || !strings.Contains(summary["reason"].(string), "memory budget") || !strings.Contains(summary["reason"].(string), "namespace URI") {
		t.Fatal("earlier or terminal error lost")
	}
}

// Cleanup failures must not erase the original cancellation/partial cause, or
// claim that server resources were released merely because cleanup was tried.
type scanCleanupErrorClient struct {
	*scanTestClient
	releaseCalls int
}

func (f *scanCleanupErrorClient) BrowseNext(ctx context.Context, r *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
	if r.ReleaseContinuationPoints {
		f.releaseCalls++
		if ctx.Err() != nil {
			return nil, errors.New("cleanup inherited cancelled context")
		}
		return nil, errors.New("transport unavailable")
	}
	return f.scanTestClient.BrowseNext(ctx, r)
}
func TestOPCUAScanMemoryCleanupFailureAndOversizedMalformedTokens(t *testing.T) {
	t.Run("release failure preserves cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		base := &scanTestClient{}
		base.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
			cancel()
			return &ua.BrowseResponse{Results: []*ua.BrowseResult{{ContinuationPoint: []byte{1}}}}, nil
		}
		f := &scanCleanupErrorClient{scanTestClient: base}
		var summary map[string]any
		err := opcuaScan(ctx, f, config.Request{}, func(e Event) {
			if e.Kind == "scan-summary" {
				summary = e.Data.(map[string]any)
			}
		})
		if !errors.Is(err, context.Canceled) || !errors.Is(err, errOPCUAScanReleaseFailed) || f.releaseCalls != 1 || summary["cancelled"] != true || !strings.Contains(summary["reason"].(string), "unconfirmed") {
			t.Fatalf("cancellation=%v cleanupFailure=%v calls=%d summary=%v", errors.Is(err, context.Canceled), errors.Is(err, errOPCUAScanReleaseFailed), f.releaseCalls, summary)
		}
	})
	t.Run("malformed mixed tokens", func(t *testing.T) {
		f := &scanTestClient{}
		f.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
			return &ua.BrowseResponse{Results: []*ua.BrowseResult{{ContinuationPoint: []byte{1}}, {ContinuationPoint: make([]byte, opcuaScanCleanupTokenLimit+1)}, {ContinuationPoint: []byte{2}}, {ContinuationPoint: []byte{1}}}}, nil
		}
		released := [][]byte{}
		capture := &scanReleaseCapture{scanTestClient: f, points: &released}
		err := opcuaScanReferences(context.Background(), capture, ua.NewNumericNodeID(0, 85), 100, func(*ua.ReferenceDescription) error { return nil })
		if !errors.Is(err, errOPCUAScanReleaseLimit) || len(released) != 2 || !bytes.Equal(released[0], []byte{1}) || !bytes.Equal(released[1], []byte{2}) || !strings.Contains(err.Error(), "malformed") {
			t.Fatalf("limit=%v released=%d err=%v", errors.Is(err, errOPCUAScanReleaseLimit), len(released), err)
		}
	})
}

func TestOPCUAScanMemoryCleanupDiagnosticsSurviveFullReasonsAndBudget(t *testing.T) {
	t.Run("full reasons then oversized token", func(t *testing.T) {
		f := &scanTestClient{}
		f.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
			refs := []*ua.ReferenceDescription{}
			for i := 0; i < 10; i++ {
				ref := scanRef(fmt.Sprintf("i=%d", i+1), ua.NodeClassVariable)
				ref.NodeID.NamespaceURI = "urn:missing"
				refs = append(refs, ref)
			}
			return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: refs, ContinuationPoint: []byte{1}}}}, nil
		}
		f.next = func(context.Context, *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
			return &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{ContinuationPoint: make([]byte, opcuaScanCleanupTokenLimit+1)}}}, nil
		}
		_, summary, err := scanEvents(f, nil)
		if !errors.Is(err, errOPCUAScanReleaseLimit) || !strings.Contains(summary["reason"].(string), "not released") || !strings.Contains(summary["reason"].(string), "namespace URI") || f.release != 0 {
			t.Fatalf("release limit=%v releases=%d summary=%v", errors.Is(err, errOPCUAScanReleaseLimit), f.release, summary)
		}
	})
	t.Run("exhausted budget retains cleanup failure", func(t *testing.T) {
		// Compute the admitted prefix, then construct an ID which consumes all but
		// <32 bytes. The following metadata rejection leaves no diagnostic quota.
		memory := &opcuaScanMemory{remaining: opcuaScanMemoryLimit}
		if err := memory.bytes(len("i=85")); err != nil {
			t.Fatal(err)
		}
		root, _, err := memory.ownNodeID(ua.NewNumericNodeID(0, 85))
		if err != nil {
			t.Fatal(err)
		}
		if err := memory.charge(3 * 128); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"http://opcfoundation.org/UA/", "urn:local", "urn:test"} {
			if err := memory.bytes(len(name)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := memory.ownNames(uaScanNode{id: root, display: "Root", browse: "Root"}); err != nil {
			t.Fatal(err)
		}
		if _, err := memory.ownPoint([]byte{1}); err != nil {
			t.Fatal(err)
		}
		length := int((memory.remaining - 512) / 32)
		base := &scanTestClient{}
		base.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
			ref := scanRef("i=1", ua.NodeClassVariable)
			ref.NodeID = ua.NewExpandedNodeID(ua.NewStringNodeID(2, strings.Repeat("x", length)), "", 0)
			return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{ref}, ContinuationPoint: []byte{1}}}}, nil
		}
		f := &scanCleanupErrorClient{scanTestClient: base}
		var summary map[string]any
		err = opcuaScan(context.Background(), f, config.Request{}, func(e Event) {
			if e.Kind == "scan-summary" {
				summary = e.Data.(map[string]any)
			}
		})
		if !errors.Is(err, errOPCUAScanMemoryLimit) || !errors.Is(err, errOPCUAScanReleaseFailed) || f.releaseCalls != 1 || !strings.Contains(summary["reason"].(string), "unconfirmed") {
			t.Fatalf("memory=%v releaseFailure=%v calls=%d summary=%v", errors.Is(err, errOPCUAScanMemoryLimit), errors.Is(err, errOPCUAScanReleaseFailed), f.releaseCalls, summary)
		}
	})
}

func TestOPCUAScanMemoryCleanupFailureAfterTenOrdinaryErrors(t *testing.T) {
	base := &scanTestClient{}
	base.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
		refs := []*ua.ReferenceDescription{}
		for i := 0; i < 10; i++ {
			ref := scanRef(fmt.Sprintf("i=%d", i+1), ua.NodeClassVariable)
			ref.NodeID.NamespaceURI = "urn:missing"
			refs = append(refs, ref)
		}
		refs = append(refs, scanRef("ns=2;s=queued", ua.NodeClassVariable))
		return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: refs, ContinuationPoint: []byte{1}}}}, nil
	}
	base.next = func(context.Context, *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
		return &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{StatusCode: ua.StatusBadUserAccessDenied, ContinuationPoint: []byte{2}}}}, nil
	}
	f := &scanCleanupErrorClient{scanTestClient: base}
	var summary map[string]any
	err := opcuaScan(context.Background(), f, config.Request{}, func(e Event) {
		if e.Kind == "scan-summary" {
			summary = e.Data.(map[string]any)
		}
	})
	if !errors.Is(err, errOPCUAScanReleaseFailed) || f.releaseCalls != 1 || len(f.browsed) != 1 || !strings.Contains(summary["reason"].(string), "unconfirmed") || !strings.Contains(summary["reason"].(string), "namespace URI") {
		t.Fatalf("cleanupFailure=%v calls=%d browsed=%d summary=%v", errors.Is(err, errOPCUAScanReleaseFailed), f.releaseCalls, len(f.browsed), summary)
	}
}

func TestOPCUAScanMemoryComplexReferenceIDsAndNamespacePromotion(t *testing.T) {
	ids := []*ua.NodeID{ua.NewStringNodeID(0, "name;雪"), ua.NewGUIDNodeID(0, "00112233-4455-6677-8899-aabbccddeeff"), ua.NewByteStringNodeID(0, []byte{0, 255, 1}), ua.NewTwoByteNodeID(42)}
	refs := []*ua.ReferenceDescription{}
	expected := map[string]bool{}
	for _, node := range ids {
		expanded := ua.NewExpandedNodeID(node, "urn:test", 0)
		refs = append(refs, &ua.ReferenceDescription{NodeID: expanded, IsForward: true, NodeClass: ua.NodeClassVariable, DisplayName: &ua.LocalizedText{Text: "雪"}, BrowseName: &ua.QualifiedName{Name: "browse"}})
		copy := ua.NewNodeIDFromExpandedNodeID(expanded)
		if node.Type() == ua.NodeIDTypeTwoByte {
			copy = ua.NewNumericNodeID(2, node.IntID())
		} else if err := copy.SetNamespace(2); err != nil {
			t.Fatal(err)
		}
		expected[copy.String()] = true
	}
	f := &scanTestClient{}
	f.browse = func(_ context.Context, r *ua.BrowseRequest) (*ua.BrowseResponse, error) {
		if r.NodesToBrowse[0].NodeID.String() == "i=85" {
			return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: refs}}}, nil
		}
		return &ua.BrowseResponse{Results: []*ua.BrowseResult{{}}}, nil
	}
	nodes, summary, err := scanEvents(f, nil)
	if err != nil || len(nodes) != 5 || summary["complete"] != true || len(f.browsed) != 5 {
		t.Fatalf("nodes=%d browsed=%d err=%v", len(nodes), len(f.browsed), err)
	}
	for _, n := range nodes[1:] {
		if !expected[n["node_id"].(string)] || n["display_name"] != "雪" || n["depth"] != 1 || n["parent_id"] != "i=85" {
			t.Fatal("complex metadata changed")
		}
	}
	for _, node := range ids {
		if node.Namespace() != 0 {
			t.Fatal("source ID mutated")
		}
	}
}

func TestOPCUAScanMemoryDepthDiagnosticExhaustionRemainsExplicit(t *testing.T) {
	memory := &opcuaScanMemory{remaining: opcuaScanMemoryLimit}
	if err := memory.bytes(len("i=85")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := memory.ownNodeID(ua.NewNumericNodeID(0, 85)); err != nil {
		t.Fatal(err)
	}
	if err := memory.charge(3 * 128); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"http://opcfoundation.org/UA/", "urn:local", "urn:test"} {
		if err := memory.bytes(len(name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := memory.ownNames(uaScanNode{display: "Root", browse: "Root"}); err != nil {
		t.Fatal(err)
	}
	length := int((memory.remaining - 512 - 1400) / 32)
	f := &scanTestClient{}
	f.browse = func(_ context.Context, r *ua.BrowseRequest) (*ua.BrowseResponse, error) {
		node := ua.NewNumericNodeID(2, 2)
		if r.NodesToBrowse[0].NodeID.String() == "i=85" {
			node = ua.NewStringNodeID(2, strings.Repeat("x", length))
		}
		ref := &ua.ReferenceDescription{NodeID: ua.NewExpandedNodeID(node, "", 0), IsForward: true, NodeClass: ua.NodeClassVariable}
		return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{ref}}}}, nil
	}
	nodes, summary, err := scanEvents(f, map[string]any{"max_depth": 1})
	if !errors.Is(err, errOPCUAScanMemoryLimit) || len(nodes) != 2 || len(f.browsed) != 2 || !strings.Contains(summary["reason"].(string), "memory budget") {
		t.Fatalf("memory=%v nodes=%d browsed=%d complete=%v", errors.Is(err, errOPCUAScanMemoryLimit), len(nodes), len(f.browsed), summary["complete"])
	}
}

type scanCleanupReplyClient struct {
	*scanTestClient
	reply        *ua.BrowseNextResponse
	releaseCalls int
}

func (f *scanCleanupReplyClient) BrowseNext(ctx context.Context, r *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
	if r.ReleaseContinuationPoints {
		f.releaseCalls++
		return f.reply, nil
	}
	return f.scanTestClient.BrowseNext(ctx, r)
}
func TestOPCUAScanMemoryCleanupResponseStatusAndShape(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reply  *ua.BrowseNextResponse
		failed bool
	}{
		{"nil response", nil, true},
		{"bad service", &ua.BrowseNextResponse{ResponseHeader: &ua.ResponseHeader{ServiceResult: ua.StatusBadUnexpectedError}}, true},
		{"bad token", &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{StatusCode: ua.StatusBadContinuationPointInvalid}}}, true},
		{"nil token result", &ua.BrowseNextResponse{Results: []*ua.BrowseResult{nil}}, true},
		{"extra token result", &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{}, {}}}, true},
		{"empty release results", &ua.BrowseNextResponse{}, false},
		{"good token", &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{StatusCode: ua.StatusOK}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			base := &scanTestClient{}
			base.browse = func(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
				cancel()
				return &ua.BrowseResponse{Results: []*ua.BrowseResult{{ContinuationPoint: []byte{1}}}}, nil
			}
			f := &scanCleanupReplyClient{scanTestClient: base, reply: tc.reply}
			var summary map[string]any
			err := opcuaScan(ctx, f, config.Request{}, func(e Event) {
				if e.Kind == "scan-summary" {
					summary = e.Data.(map[string]any)
				}
			})
			if !errors.Is(err, context.Canceled) || errors.Is(err, errOPCUAScanReleaseFailed) != tc.failed || f.releaseCalls != 1 || summary["cancelled"] != true || strings.Contains(summary["reason"].(string), "unconfirmed") != tc.failed {
				t.Fatalf("cancelled=%v failed=%v calls=%d summary=%v", errors.Is(err, context.Canceled), errors.Is(err, errOPCUAScanReleaseFailed), f.releaseCalls, summary)
			}
		})
	}
}
