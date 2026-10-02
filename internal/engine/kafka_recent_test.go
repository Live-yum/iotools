package engine

import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"testing"
	"time"
)

func TestKafkaMostRecentFiniteTailAndFilteredEnd(t *testing.T) {
	s, e := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(2, "recent"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	producer, e := kgo.NewClient(kgo.SeedBrokers(s.ListenAddrs()...), kgo.RecordPartitioner(kgo.ManualPartitioner()))
	if e != nil {
		t.Fatal(e)
	}
	defer producer.Close()
	records := []*kgo.Record{}
	for p := 0; p < 2; p++ {
		for n := 0; n < 5; n++ {
			records = append(records, &kgo.Record{Topic: "recent", Partition: int32(p), Value: []byte(fmt.Sprintf("p%d-%d", p, n))})
		}
	}
	if e = producer.ProduceSync(context.Background(), records...).FirstErr(); e != nil {
		t.Fatal(e)
	}
	base := config.Request{Protocol: "kafka", Action: "consume", Endpoint: s.ListenAddrs()[0], Timeout: "3s", Params: map[string]any{"topic": "recent", "offset": "most-recent", "limit": 4}}
	count := 0
	if e = Run(context.Background(), base, false, func(e Event) {
		if e.Kind == "record" {
			m := e.Data.(map[string]any)
			count++
			if m["offset"].(int64) < 3 {
				t.Error("read older than tail", m)
			}
		}
	}); e != nil {
		t.Fatal(e)
	}
	if count != 4 {
		t.Fatal("tail count", count)
	}
	base.Params["filter"] = "no-such-match"
	start := time.Now()
	if e = Run(context.Background(), base, false, func(e Event) {
		if e.Kind == "record" {
			t.Error("filter ignored")
		}
	}); e != nil {
		t.Fatal("finite snapshot waited for future message", e)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("filtered finite read did not finish")
	}
	base.Params["filter"] = ""
	base.Params["consume_partitions"] = []any{1}
	base.Params["limit"] = 1
	count = 0
	if e = Run(context.Background(), base, false, func(e Event) {
		if e.Kind == "record" {
			m := e.Data.(map[string]any)
			count++
			if m["partition"] != int32(1) || m["offset"] != int64(4) {
				t.Error(m)
			}
		}
	}); e != nil {
		t.Fatal(e)
	}
	if count != 1 {
		t.Fatal(count)
	}
}
func TestKafkaMostRecentEmptyAndInvalidPartition(t *testing.T) {
	s, e := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(2, "empty"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r := config.Request{Protocol: "kafka", Action: "consume", Endpoint: s.ListenAddrs()[0], Timeout: "2s", Params: map[string]any{"topic": "empty", "offset": "most-recent", "limit": 10}}
	if e = Run(context.Background(), r, false, nil); e != nil {
		t.Fatal(e)
	}
	r.Params["consume_partitions"] = []any{9}
	if e = Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("unknown partition accepted")
	}
}
