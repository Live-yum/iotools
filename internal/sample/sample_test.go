package sample

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
)

func TestCollectionDefaultResolvesEveryExample(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", "examples", "local.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(Collection, example) {
		t.Fatal("embedded init sample differs from examples/local.yaml")
	}
	c, err := config.Parse(Collection)
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultProfile != "local" {
		t.Fatalf("default profile = %q, want local", c.DefaultProfile)
	}
	bases := map[string]string{
		"http": "http://127.0.0.1:8080", "kafka": "127.0.0.1:9092",
		"mqtt": "mqtt://127.0.0.1:1883", "modbus": "tcp://127.0.0.1:1502",
		"opcua": "opc.tcp://127.0.0.1:4840",
	}
	seen := map[string]bool{}
	for _, r := range c.Requests {
		t.Run(r.ID, func(t *testing.T) {
			base, ok := bases[r.Protocol]
			if !ok {
				t.Fatalf("unexpected protocol %q", r.Protocol)
			}
			seen[r.Protocol] = true
			before, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := c.Resolve(r, c.DefaultProfile)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.ReplaceAll(r.Endpoint, "${"+r.Protocol+"}", base)
			if resolved.Endpoint != want {
				t.Errorf("endpoint = %q, want %q", resolved.Endpoint, want)
			}
			encoded, err := json.Marshal(resolved)
			if err != nil || bytes.Contains(encoded, []byte("${")) {
				t.Errorf("unresolved sample: %s, error: %v", encoded, err)
			}
			after, _ := json.Marshal(r)
			if !bytes.Equal(before, after) {
				t.Error("resolving modified source request")
			}
		})
	}
	if len(seen) != len(bases) {
		t.Errorf("covered %d protocols, want %d", len(seen), len(bases))
	}
}

// The documented CLI invocation does not supply --profile. Resolve exactly as
// startup does, without contacting the example endpoint.
func TestOPCUAScanExampleResolvesWithDefaultProfile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "opcua-scan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultProfile != "local" {
		t.Fatalf("default profile = %q, want local", c.DefaultProfile)
	}
	if len(c.Requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(c.Requests))
	}
	resolved, err := c.Resolve(c.Requests[0], c.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Protocol != "opcua" || resolved.Action != "scan" || resolved.Endpoint != "opc.tcp://127.0.0.1:4840" {
		t.Fatalf("unexpected resolved scan: %+v", resolved)
	}
	encoded, err := json.Marshal(resolved)
	if err != nil || bytes.Contains(encoded, []byte("${")) {
		t.Fatalf("unresolved scan: %s, error: %v", encoded, err)
	}
}
