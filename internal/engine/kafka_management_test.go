package engine

import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestKafkaRESTManagementTargetsAndGates(t *testing.T) {
	var method, path, query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.EscapedPath()
		query = r.URL.RawQuery
		fmt.Fprint(w, `[1,2]`)
	}))
	defer server.Close()
	cases := []struct{ action, method, path string }{{"schema-versions", "GET", "/subjects/a%2Fb/versions"}, {"delete-schema", "DELETE", "/subjects/a%2Fb/versions/2"}, {"delete-subject", "DELETE", "/subjects/a%2Fb"}, {"purge-subject", "DELETE", "/subjects/a%2Fb"}, {"pause-connector", "PUT", "/connectors/a%2Fb/pause"}, {"resume-connector", "PUT", "/connectors/a%2Fb/resume"}, {"delete-connector", "DELETE", "/connectors/a%2Fb"}}
	for _, test := range cases {
		r := config.Request{Protocol: "kafka", Action: test.action, Endpoint: server.URL, Params: map[string]any{"subject": "a/b", "connector": "a/b", "version": 2, "confirm_subject": "a/b"}}
		if r.Mutates() {
			method = ""
			if e := Run(context.Background(), r, false, nil); e == nil || method != "" {
				t.Fatal("mutation bypassed gate", test.action)
			}
		}
		if e := Run(context.Background(), r, true, nil); e != nil {
			t.Fatal(test.action, e)
		}
		if method != test.method || path != test.path {
			t.Fatalf("%s %s", method, path)
		}
		if test.action == "purge-subject" && query != "permanent=true" {
			t.Fatal("hard delete flag missing")
		}
	}
	r := config.Request{Protocol: "kafka", Action: "purge-subject", Endpoint: server.URL, Params: map[string]any{"subject": "a/b"}}
	if e := Run(context.Background(), r, true, nil); e == nil {
		t.Fatal("unconfirmed hard delete accepted")
	}
}
func TestKafkaTopicConfigurationAndExpansion(t *testing.T) {
	s, e := kfake.NewCluster(kfake.NumBrokers(1))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r := config.Request{Protocol: "kafka", Action: "create-topic", Endpoint: s.ListenAddrs()[0], Timeout: "5s", Params: map[string]any{"topic": "expand-test", "partitions": 1, "replication_factor": 1}}
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	r.Action = "topic-config"
	if e := Run(context.Background(), r, false, nil); e != nil {
		t.Fatal(e)
	}
	r.Action = "expand-partitions"
	r.Params["partitions"] = 2
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("expansion bypassed gate")
	}
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	r.Action = "delete-group"
	r.Params["group"] = "unused"
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("group deletion bypassed gate")
	}
}
func TestKafkaFiltersAndTimeValidation(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, v := range []string{"today", "yesterday", "last7days", "2026-09-30T10:00:00+08:00"} {
		if _, e := kafkaStartTime(v, now); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := kafkaStartTime("2026-09-30", now); e == nil {
		t.Fatal("timezone-less date accepted")
	}
	if !kafkaMatchFilter("abc-123", "123", "abc") || kafkaMatchFilter("abc", "", "b") {
		t.Fatal("filters incorrect")
	}
	for _, v := range []any{[]any{0, 1}, []any{0, 1.5}, []any{1, 1}, []any{-1}} {
		_, e := kafkaPartitions(v)
		valid := len(v.([]any)) == 2 && fmt.Sprint(v) == "[0 1]"
		if (e == nil) != valid {
			t.Fatalf("%v %v", v, e)
		}
	}
}

func TestKafkaConsumePartitionAndTimestampOnWire(t *testing.T) {
	s, e := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(2, "timed"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	producer, e := kgo.NewClient(kgo.SeedBrokers(s.ListenAddrs()...), kgo.RecordPartitioner(kgo.ManualPartitioner()))
	if e != nil {
		t.Fatal(e)
	}
	defer producer.Close()
	now := time.Now().UTC()
	records := []*kgo.Record{{Topic: "timed", Partition: 1, Timestamp: now.Add(-2 * time.Hour), Key: []byte("wanted-old"), Value: []byte("fresh-old")}, {Topic: "timed", Partition: 0, Timestamp: now, Key: []byte("wanted-other"), Value: []byte("fresh-other")}, {Topic: "timed", Partition: 1, Timestamp: now, Key: []byte("wanted-new"), Value: []byte("fresh-new")}}
	if e := producer.ProduceSync(context.Background(), records...).FirstErr(); e != nil {
		t.Fatal(e)
	}
	r := config.Request{Protocol: "kafka", Action: "consume", Endpoint: s.ListenAddrs()[0], Timeout: "5s", Params: map[string]any{"topic": "timed", "consume_partitions": []any{1}, "start_time": now.Add(-time.Hour).Format(time.RFC3339Nano), "key_prefix": "wanted-", "value_prefix": "fresh-", "limit": 1}}
	seen := false
	if e := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "record" {
			m := e.Data.(map[string]any)
			seen = m["value"] == "fresh-new" && m["partition"] == int32(1)
		}
	}); e != nil {
		t.Fatal(e)
	}
	if !seen {
		t.Fatal("partition/time filter selected wrong record")
	}
}
