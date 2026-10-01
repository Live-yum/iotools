package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Host-only loopback fixtures; this package is never linked into the APK.
func startKafkaFixture() (map[string]any, func(), error) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.Ports(48412), kfake.ListenFn(func(network, address string) (net.Listener, error) {
		_, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		return net.Listen(network, net.JoinHostPort("127.0.0.1", port))
	}), kfake.SeedTopics(1, "mobile-records"))
	if err != nil {
		return nil, nil, err
	}
	fail := func(e error) (map[string]any, func(), error) { cluster.Close(); return nil, nil, e }
	client, err := kgo.NewClient(kgo.SeedBrokers("127.0.0.1:48412"))
	if err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = client.ProduceSync(ctx, &kgo.Record{Topic: "mobile-records", Key: []byte{0xff, 0}, Value: []byte("fixture-first"), Headers: []kgo.RecordHeader{{Key: "trace", Value: []byte{0xff, 0}}}, Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, &kgo.Record{Topic: "mobile-records", Key: []byte("second"), Value: []byte("fixture-second"), Headers: []kgo.RecordHeader{{Key: "trace", Value: []byte("fixture-header")}}, Timestamp: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}).FirstErr()
	cancel()
	client.Close()
	if err != nil {
		return fail(err)
	}
	var total, produce, deletions, mutations, registrations, softDeletes, purges, updates, pauses, resumes atomic.Int64
	var stateMu sync.Mutex
	registered, softDeleted := false, false
	connectorState := "RUNNING"
	connectorConfig := map[string]string{"connector.class": "fixture", "tasks.max": "1"}
	cluster.Control(func(request kmsg.Request) (kmsg.Response, error, bool) {
		total.Add(1)
		switch request.(type) {
		case *kmsg.ProduceRequest:
			produce.Add(1)
		case *kmsg.DeleteTopicsRequest:
			deletions.Add(1)
		}
		return nil, nil, false
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/metrics" {
			_ = json.NewEncoder(w).Encode(map[string]int64{"total_requests": total.Load(), "produce_requests": produce.Load(), "delete_topic_requests": deletions.Load(), "http_mutations": mutations.Load(), "schema_registrations": registrations.Load(), "schema_soft_deletes": softDeletes.Load(), "schema_purges": purges.Load(), "connector_updates": updates.Load(), "connector_pauses": pauses.Load(), "connector_resumes": resumes.Load()})
			return
		}
		stateMu.Lock()
		defer stateMu.Unlock()
		if r.Method != "GET" {
			mutations.Add(1)
			switch {
			case r.Method == "POST" && r.URL.Path == "/subjects/fixture-schema/versions":
				var body struct {
					Schema string `json:"schema"`
				}
				decoder := json.NewDecoder(io.LimitReader(r.Body, 65537))
				decoder.DisallowUnknownFields()
				if decoder.Decode(&body) != nil || body.Schema != `"string"` {
					http.Error(w, "expected exact synthetic string schema", 400)
					return
				}
				registered = true
				softDeleted = false
				registrations.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]int{"id": 2})
				return
			case r.Method == "DELETE" && r.URL.Path == "/subjects/fixture-schema":
				if !registered {
					http.Error(w, "subject missing", 404)
					return
				}
				if r.URL.Query().Get("permanent") == "true" {
					if !softDeleted {
						http.Error(w, "soft delete required first", 409)
						return
					}
					registered = false
					purges.Add(1)
				} else {
					softDeleted = true
					softDeletes.Add(1)
				}
				_ = json.NewEncoder(w).Encode([]int{1})
				return
			case r.Method == "PUT" && r.URL.Path == "/connectors/local-demo/config":
				var body map[string]string
				decoder := json.NewDecoder(io.LimitReader(r.Body, 65537))
				decoder.DisallowUnknownFields()
				if decoder.Decode(&body) != nil || body["connector.class"] != "fixture" || body["tasks.max"] != "2" {
					http.Error(w, "unexpected connector config", 400)
					return
				}
				connectorConfig = body
				updates.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{"name": "local-demo", "config": connectorConfig})
				return
			case r.Method == "PUT" && r.URL.Path == "/connectors/local-demo/pause":
				connectorState = "PAUSED"
				pauses.Add(1)
				w.WriteHeader(202)
				return
			case r.Method == "PUT" && r.URL.Path == "/connectors/local-demo/resume":
				connectorState = "RUNNING"
				resumes.Add(1)
				w.WriteHeader(202)
				return
			default:
				http.Error(w, "unexpected fixture mutation", 409)
				return
			}
		}
		var result any
		switch {
		case r.URL.Path == "/subjects":
			result = []string{"mobile-records-value"}
			if registered && !softDeleted {
				result = []string{"mobile-records-value", "fixture-schema"}
			}
		case r.URL.Path == "/subjects/mobile-records-value/versions":
			result = []int{1}
		case strings.HasPrefix(r.URL.Path, "/subjects/mobile-records-value/versions/"):
			result = map[string]any{"subject": "mobile-records-value", "version": 1, "id": 1, "schema": `{"type":"record","name":"Mobile","fields":[{"name":"message","type":"string"}]}`}
		case r.URL.Path == "/subjects/fixture-schema/versions":
			result = []int{1}
		case strings.HasPrefix(r.URL.Path, "/subjects/fixture-schema/versions/"):
			if !registered || softDeleted {
				http.Error(w, "subject missing", 404)
				return
			}
			result = map[string]any{"subject": "fixture-schema", "version": 1, "id": 2, "schema": `"string"`}
		case r.URL.Path == "/schemas/ids/1":
			result = map[string]any{"schema": `{"type":"record","name":"Mobile","fields":[{"name":"message","type":"string"}]}`}
		case r.URL.Path == "/connectors":
			result = map[string]any{"local-demo": map[string]any{"info": map[string]any{"name": "local-demo", "config": connectorConfig}, "status": map[string]any{"name": "local-demo", "connector": map[string]string{"state": connectorState, "worker_id": "loopback"}}}}
		case r.URL.Path == "/connectors/local-demo/status":
			result = map[string]any{"name": "local-demo", "connector": map[string]string{"state": connectorState, "worker_id": "loopback"}, "tasks": []any{}, "type": "source"}
		default:
			http.Error(w, fmt.Sprintf("unknown fixture route %s", r.URL.Path), 404)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:48413")
	if err != nil {
		return fail(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	go func() { _ = server.Serve(listener) }()
	close := func() { _ = server.Close(); cluster.Close() }
	return map[string]any{"kafka_endpoint": "127.0.0.1:48412", "kafka_http_url": "http://127.0.0.1:48413", "kafka_topic": "mobile-records", "kafka_metrics_url": "http://127.0.0.1:48413/metrics"}, close, nil
}
