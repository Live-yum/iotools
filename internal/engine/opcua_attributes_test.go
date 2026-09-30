package engine

import (
	"context"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua/ua"
	"strings"
	"testing"
)

func TestOPCUAAttributeSelectionAndTypedNames(t *testing.T) {
	for _, raw := range []any{"DisplayName", "displayname", 4, "4"} {
		id, e := opcuaAttribute(raw)
		if e != nil || id != ua.AttributeIDDisplayName {
			t.Fatalf("attribute %v: %v %v", raw, id, e)
		}
	}
	for _, raw := range []any{0, 28, 1.5, "wrong"} {
		if _, e := opcuaAttribute(raw); e == nil {
			t.Fatalf("invalid attribute %v accepted", raw)
		}
	}
	for _, kind := range []string{"LocalizedText", "QualifiedName"} {
		raw := map[string]any{"text": "温度", "locale": "zh-CN"}
		if kind == "QualifiedName" {
			raw = map[string]any{"name": "温度", "namespace": 2}
		}
		if _, e := opcuaVariant(kind, raw); e != nil {
			t.Fatal(e)
		}
		if _, e := opcuaVariant(kind+"[]", []any{raw}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := opcuaVariant("QualifiedName", map[string]any{"name": "x", "namespace": 1.5}); e == nil {
		t.Fatal("fractional namespace accepted")
	}
	r := config.Request{Protocol: "opcua", Action: "write", Endpoint: "opc.tcp://127.0.0.1:1", Params: map[string]any{"node_id": "i=85", "attribute": "DisplayName", "value_type": "String", "value": "x"}}
	if e := Run(context.Background(), r, true, nil); e == nil || !strings.Contains(e.Error(), "LocalizedText") {
		t.Fatalf("write type wasn't rejected before network: %v", e)
	}
	r.Params["attribute"] = "NodeID"
	if e := validateOPCUAOperation(r); e == nil {
		t.Fatal("read-only attribute accepted for write")
	}
}
func TestOPCUALoopbackAllAttributesAndStructuredValues(t *testing.T) {
	endpoint, ns, params := localUAServer(t, false)
	params["node_id"] = ua.NewStringNodeID(ns.ID(), "Value").String()
	r := config.Request{Protocol: "opcua", Action: "attributes", Endpoint: endpoint, Timeout: "10s", Params: params}
	count := 0
	display := false
	if e := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "attribute" {
			count++
			m := e.Data.(map[string]any)
			if m["attribute"] == "DisplayName" {
				display = true
			}
		}
	}); e != nil {
		t.Fatal(e)
	}
	if count != 27 || !display {
		t.Fatalf("read %d attributes, DisplayName %v", count, display)
	}
	r.Action = "read"
	params["attribute"] = "DisplayName"
	if e := Run(context.Background(), r, false, nil); e != nil {
		t.Fatal(e)
	}
	delete(params, "attribute")
	r.Action = "write"
	params["value_type"] = "LocalizedText"
	params["value"] = map[string]any{"text": "设备温度", "locale": "zh-CN"}
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	text, ok := ns.GetValue("Value").(*ua.LocalizedText)
	if !ok || text.Text != "设备温度" || text.Locale != "zh-CN" {
		t.Fatal("LocalizedText did not cross wire")
	}
	params["value_type"] = "QualifiedName"
	params["value"] = map[string]any{"name": "Motor", "namespace": 2}
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	name, ok := ns.GetValue("Value").(*ua.QualifiedName)
	if !ok || name.Name != "Motor" || name.NamespaceIndex != 2 {
		t.Fatal("QualifiedName did not cross wire")
	}
}

type directionUABrowser struct {
	direction ua.BrowseDirection
	typ       *ua.NodeID
	subtypes  bool
}

func (f *directionUABrowser) Browse(_ context.Context, r *ua.BrowseRequest) (*ua.BrowseResponse, error) {
	f.direction = r.NodesToBrowse[0].BrowseDirection
	f.typ = r.NodesToBrowse[0].ReferenceTypeID
	f.subtypes = r.NodesToBrowse[0].IncludeSubtypes
	return &ua.BrowseResponse{Results: []*ua.BrowseResult{{StatusCode: ua.StatusOK}}}, nil
}
func (f *directionUABrowser) BrowseNext(context.Context, *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
	panic("not reached")
}
func TestOPCUAReferenceDirections(t *testing.T) {
	for _, direction := range []string{"forward", "inverse", "both"} {
		f := new(directionUABrowser)
		r := config.Request{Action: "references", Params: map[string]any{"direction": direction, "reference_type": "i=47", "include_subtypes": false}}
		if e := opcuaBrowse(context.Background(), f, r, nil); e != nil {
			t.Fatal(e)
		}
		want, _ := opcuaBrowseDirection(r)
		if f.direction != want || f.typ.String() != "i=47" || f.subtypes {
			t.Fatal("reference query lost filters")
		}
	}
}
