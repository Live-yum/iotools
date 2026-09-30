package engine

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/twmb/franz-go/pkg/kfake"
)

const testAvroSchema = `{"type":"record","name":"Reading","fields":[{"name":"temperature","type":"long"}]}`

func registryHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "registry-user" || pass != "registry-secret" {
			http.Error(w, "secret-echo", 401)
			return
		}
		if r.URL.Path != "/subjects/readings-value/versions/2" && r.URL.Path != "/schemas/ids/42" {
			http.Error(w, "unknown-secret", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": 42, "schema": testAvroSchema})
	}
}
func avroRequest(endpoint, registry string) config.Request {
	return config.Request{Protocol: "kafka", Action: "produce", Endpoint: endpoint, Timeout: "5s", Params: map[string]any{"topic": "avro-readings", "value_format": "avro", "value_subject": "readings-value", "value_version": 2, "value": `{"temperature":9007199254740993}`, "schema_registry_url": registry, "schema_registry_username": "registry-user", "schema_registry_password": "registry-secret"}}
}
func TestKafkaAvroRegistryWireRoundTrip(t *testing.T) {
	srv := httptest.NewServer(registryHandler(t))
	defer srv.Close()
	broker, err := kfake.NewCluster(kfake.NumBrokers(1))
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	r := avroRequest(broker.ListenAddrs()[0], srv.URL)
	r.Action = "create-topic"
	if err := Run(context.Background(), r, true, nil); err != nil {
		t.Fatal(err)
	}
	r.Action = "produce"
	if err := Run(context.Background(), r, true, nil); err != nil {
		t.Fatal(err)
	}
	r.Action = "consume"
	r.Params["limit"] = 1
	r.Params["value_format"] = "auto"
	seen := false
	if err := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "record" {
			m := e.Data.(map[string]any)
			seen = m["value"].(map[string]any)["temperature"] == json.Number("9007199254740993")
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("did not preserve Avro long through broker and registry")
	}
}
func TestKafkaAvroFailsClosed(t *testing.T) {
	srv := httptest.NewServer(registryHandler(t))
	defer srv.Close()
	r := avroRequest("unused", srv.URL)
	s, err := newKafkaRegistry(r)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	b, err := kafkaEncode(context.Background(), r, s, "value")
	if err != nil {
		t.Fatal(err)
	}
	r.Action = "consume"
	cases := []struct {
		name     string
		data     []byte
		registry *kafkaRegistry
	}{{"missing registry", b, nil}, {"truncated", []byte{0, 1}, s}, {"unknown ID", append([]byte{0, 0, 0, 0, 99}, b[5:]...), s}, {"trailing bytes", append(append([]byte{}, b...), 1), s}, {"malformed datum", []byte{0, 0, 0, 0, 42, 255}, s}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := kafkaDecode(context.Background(), r, tc.registry, "value", tc.data)
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("error exposed server body or credentials")
			}
		})
	}
	if v, err := kafkaDecode(context.Background(), r, s, "value", nil); err != nil || v != nil {
		t.Fatal("tombstone not preserved")
	}
	r.Params["value_format"] = "text"
	v, err := kafkaDecode(context.Background(), r, s, "value", []byte{255})
	if err != nil || v.(map[string]any)["encoding"] != "base64" {
		t.Fatal("binary corrupted")
	}
}
func TestKafkaRegistryTLSAndRedirect(t *testing.T) {
	srv := httptest.NewTLSServer(registryHandler(t))
	defer srv.Close()
	r := avroRequest("unused", srv.URL)
	s, err := newKafkaRegistry(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kafkaEncode(context.Background(), r, s, "value"); err == nil {
		t.Fatal("untrusted TLS accepted")
	}
	s.close()
	file := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	r.Params["schema_registry_ca_file"] = file
	s, err = newKafkaRegistry(r)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if _, err := kafkaEncode(context.Background(), r, s, "value"); err != nil {
		t.Fatal(err)
	}
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	r.Params["schema_registry_url"] = redirect.URL
	s2, err := newKafkaRegistry(r)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.close()
	if _, err := kafkaEncode(context.Background(), r, s2, "value"); err == nil || hit {
		t.Fatal("registry redirect followed")
	}
}
func TestKafkaAvroPrimitiveBytesAndKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": 7, "schema": `"bytes"`})
	}))
	defer srv.Close()
	r := avroRequest("unused", srv.URL)
	r.Params["key_format"] = "avro"
	r.Params["key_subject"] = "keys"
	r.Params["key"] = `"abc"`
	s, err := newKafkaRegistry(r)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	b, err := kafkaEncode(context.Background(), r, s, "key")
	if err != nil {
		t.Fatal(err)
	}
	if string(b[5:]) != "abc" || binary.BigEndian.Uint32(b[1:5]) != 7 {
		t.Fatalf("wrong Confluent bytes framing: %v", b)
	}
	r.Action = "consume"
	v, err := kafkaDecode(context.Background(), r, s, "key", b)
	if err != nil || v != "abc" {
		t.Fatalf("decode: %v %v", v, err)
	}
}
func TestKafkaRegistryRejectsUnsupportedSchemas(t *testing.T) {
	for _, response := range []string{`{"id":4,"schemaType":"PROTOBUF","schema":"secret"}`, `{"id":4,"schema":"secret"}`, `{"id":4,"schema":"\"string\"","references":[{"name":"external"}]}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(response)) }))
		r := avroRequest("unused", srv.URL)
		s, err := newKafkaRegistry(r)
		if err != nil {
			t.Fatal(err)
		}
		_, err = kafkaEncode(context.Background(), r, s, "value")
		s.close()
		srv.Close()
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe schema response: %v", err)
		}
	}
}

func TestKafkaAvroProfileMapping(t *testing.T) {
	t.Setenv("IOTOOLS_TEST_REGISTRY_PASSWORD", "test-password")
	c, err := config.Parse([]byte(`version: 1
profiles:
  test:
    registry: https://registry.invalid
requests:
  - id: avro
    protocol: kafka
    action: consume
    endpoint: localhost:9092
    params:
      schema_registry_url: ${registry}
      schema_registry_password: ${env:IOTOOLS_TEST_REGISTRY_PASSWORD}
`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Resolve(c.Requests[0], "test")
	if err != nil {
		t.Fatal(err)
	}
	if r.String("schema_registry_url", "") != "https://registry.invalid" || r.String("schema_registry_password", "") != "test-password" {
		t.Fatal("registry fields were not resolved")
	}
	if c.Requests[0].String("schema_registry_password", "") != "${env:IOTOOLS_TEST_REGISTRY_PASSWORD}" {
		t.Fatal("resolved credential mutated source")
	}
	for _, bad := range []string{"https://u:p@registry.invalid", "https://registry.invalid?token=secret", "file:///tmp/schema", "https://registry.invalid#secret"} {
		r.Params["schema_registry_url"] = bad
		if _, err := newKafkaRegistry(r); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("bad URL accepted or leaked: %v", err)
		}
	}
}
