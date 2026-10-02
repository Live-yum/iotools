package engine

import (
	"context"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua/ua"
	"testing"
)

func TestUABrowsePathParsing(t *testing.T) {
	parts, e := parseUABrowsePath("/Root/Objects/ns=2:A&/B/3:C&:D/Name&&Value")
	if e != nil {
		t.Fatal(e)
	}
	if len(parts) != 4 || parts[1].Name != "A/B" || parts[1].NamespaceIndex != 2 || parts[2].Name != "C:D" || parts[3].Name != "Name&Value" {
		t.Fatalf("%+v", parts)
	}
	for _, bad := range []string{"/name&", "/ns=65536:x", "/ns=-1:x"} {
		if _, e := parseUABrowsePath(bad); e == nil {
			t.Fatal("invalid path accepted", bad)
		}
	}
}
func TestUABrowsePathLoopback(t *testing.T) {
	endpoint, _, params := localUAServer(t, false)
	params["browse_path"] = "/Root/Objects"
	found := false
	e := Run(context.Background(), config.Request{Protocol: "opcua", Action: "browse-path", Endpoint: endpoint, Params: params}, false, func(e Event) {
		if e.Kind == "path-resolved" {
			found = e.Data.(map[string]any)["node_id"] == ua.NewNumericNodeID(0, 85).String()
		}
	})
	if e != nil || !found {
		t.Fatalf("found=%v error=%v", found, e)
	}
}

func TestUANodePathRoundtripOnLoopback(t *testing.T) {
	endpoint, _, params := localUAServer(t, false)
	params["node_id"] = "i=85"
	path := ""
	if e := Run(context.Background(), config.Request{Protocol: "opcua", Action: "node-path", Endpoint: endpoint, Params: params}, false, func(e Event) {
		if e.Kind == "browse-path" {
			path = e.Data.(map[string]any)["path"].(string)
		}
	}); e != nil {
		t.Fatal(e)
	}
	if path != "/Objects" {
		t.Fatal(path)
	}
	if escapeUABrowseName(&ua.QualifiedName{NamespaceIndex: 2, Name: "A/B:C"}) != "2:A&/B&:C" {
		t.Fatal("path escaping")
	}
}
