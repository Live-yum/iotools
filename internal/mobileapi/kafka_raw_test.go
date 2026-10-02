package mobileapi

import (
	"context"
	"encoding/base64"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"testing"
	"time"
)

func TestKafkaRecordPreservesRawBytesHeadersAndTimestamp(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "raw-mobile"))
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()
	client, err := kgo.NewClient(kgo.SeedBrokers(cluster.ListenAddrs()...))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key, value := []byte{0xff, 0x00}, []byte{0x80, 0xff, 0x00}
	record := &kgo.Record{Topic: "raw-mobile", Key: key, Value: value, Headers: []kgo.RecordHeader{{Key: "trace", Value: []byte{0xff, 0}}}, Timestamp: time.Now().UTC()}
	if err = client.ProduceSync(ctx, record).FirstErr(); err != nil {
		t.Fatal(err)
	}
	s := testSession(t, config.Request{ID: "read", Protocol: "kafka", Action: "consume", Endpoint: cluster.ListenAddrs()[0], Timeout: "3s", Params: map[string]any{"topic": "raw-mobile", "offset": "earliest", "limit": 1, "key_format": "text", "value_format": "text"}})
	mustOK(t, s, map[string]any{"op": "run", "token": previewToken(t, s, "read")})
	found := false
	for _, event := range awaitDone(t, s) {
		if event["kind"] != "record" {
			continue
		}
		data := event["data"].(map[string]any)
		if data["raw_key_base64"] != base64.StdEncoding.EncodeToString(key) || data["raw_value_base64"] != base64.StdEncoding.EncodeToString(value) {
			t.Fatalf("binary record changed %v", data)
		}
		if data["timestamp"] == "" || len(data["headers"].([]any)) != 1 {
			t.Fatal("metadata lost")
		}
		found = true
	}
	if !found {
		t.Fatal("no record")
	}
}
