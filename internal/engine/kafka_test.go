package engine

import (
	"context"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/twmb/franz-go/pkg/kfake"
	"testing"
)

func TestKafkaRealWireLifecycle(t *testing.T) {
	s, e := kfake.NewCluster(kfake.NumBrokers(1))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r := config.Request{Protocol: "kafka", Endpoint: s.ListenAddrs()[0], Action: "create-topic", Timeout: "5s", Params: map[string]any{"topic": "iotools-test", "partitions": 1, "replication_factor": 1}}
	for _, action := range []string{"create-topic", "topics", "brokers", "groups", "offsets"} {
		r.Action = action
		if e := Run(context.Background(), r, true, nil); e != nil {
			t.Fatalf("%s: %v", action, e)
		}
	}
	r.Action = "produce"
	r.Params["value"] = `{"temperature":21}`
	r.Params["key"] = "sensor"
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	r.Action = "consume"
	r.Params["limit"] = 1
	seen := false
	if e := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "record" {
			m := e.Data.(map[string]any)
			seen = m["key"] == "sensor" && m["value"].(map[string]any)["temperature"] == float64(21)
		}
	}); e != nil {
		t.Fatal(e)
	}
	if !seen {
		t.Fatal("record was not decoded")
	}
	r.Action = "alter-topic"
	r.Params["configs"] = map[string]any{"retention.ms": "3600000"}
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	r.Action = "delete-topic"
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
}
