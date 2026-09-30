package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

func runKafka(ctx context.Context, r config.Request, emit Emit) error {
	// Schema Registry and Kafka Connect are HTTP protocols; use the same verified
	// TLS/auth/response engine rather than starting an external helper.
	switch r.Action {
	case "schemas", "schema", "register-schema", "connectors", "connector", "update-connector":
		h := r
		h.Protocol = "http"
		h.Action = "GET"
		switch r.Action {
		case "schemas":
			h.Endpoint = strings.TrimRight(r.Endpoint, "/") + "/subjects"
		case "schema":
			h.Endpoint = strings.TrimRight(r.Endpoint, "/") + "/subjects/" + url.PathEscape(r.String("subject", "")) + "/versions/" + r.String("version", "latest")
		case "register-schema":
			h.Action = "POST"
			h.Endpoint = strings.TrimRight(r.Endpoint, "/") + "/subjects/" + url.PathEscape(r.String("subject", "")) + "/versions"
		case "connectors":
			h.Endpoint = strings.TrimRight(r.Endpoint, "/") + "/connectors?expand=status&expand=info"
		case "connector":
			h.Endpoint = strings.TrimRight(r.Endpoint, "/") + "/connectors/" + url.PathEscape(r.String("connector", "")) + "/status"
		case "update-connector":
			h.Action = "PUT"
			h.Endpoint = strings.TrimRight(r.Endpoint, "/") + "/connectors/" + url.PathEscape(r.String("connector", "")) + "/config"
		}
		return runHTTP(ctx, h, emit)
	}
	seeds := strings.Split(strings.TrimPrefix(r.Endpoint, "kafka://"), ",")
	for _, s := range seeds {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("Kafka brokers are required")
		}
	}
	opts := []kgo.Opt{kgo.SeedBrokers(seeds...), kgo.ClientID("iotools"), kgo.FetchMaxBytes(maxBody), kgo.RecordDeliveryTimeout(0)}
	if r.Bool("tls") {
		t, e := tlsConfig(r)
		if e != nil {
			return e
		}
		opts = append(opts, kgo.DialTLSConfig(t))
	}
	user, password := r.String("username", ""), r.String("password", "")
	switch r.String("sasl", "") {
	case "":
	case "plain":
		opts = append(opts, kgo.SASL(plain.Auth{User: user, Pass: password}.AsMechanism()))
	case "scram-sha-256":
		opts = append(opts, kgo.SASL(scram.Auth{User: user, Pass: password}.AsSha256Mechanism()))
	case "scram-sha-512":
		opts = append(opts, kgo.SASL(scram.Auth{User: user, Pass: password}.AsSha512Mechanism()))
	default:
		return fmt.Errorf("unsupported SASL mechanism")
	}
	topic := r.String("topic", "")
	if r.Action == "consume" {
		if topic == "" {
			return fmt.Errorf("topic required")
		}
		offset := kgo.NewOffset().AtStart()
		if r.String("offset", "earliest") == "latest" {
			offset = kgo.NewOffset().AtEnd()
		}
		opts = append(opts, kgo.ConsumeTopics(topic), kgo.ConsumeResetOffset(offset))
	}
	cl, e := kgo.NewClient(opts...)
	if e != nil {
		return e
	}
	defer cl.Close()
	admin := kadm.NewClient(cl)
	switch r.Action {
	case "topics":
		v, e := admin.ListTopics(ctx)
		if e != nil {
			return e
		}
		send(emit, "topics", v)
		return v.Error()
	case "brokers":
		v, e := admin.ListBrokers(ctx)
		if e != nil {
			return e
		}
		send(emit, "brokers", v)
		return nil
	case "groups":
		v, e := admin.ListGroups(ctx)
		if e != nil {
			return e
		}
		send(emit, "groups", v)
		return nil
	case "group":
		v, e := admin.DescribeGroups(ctx, r.String("group", ""))
		if e != nil {
			return e
		}
		send(emit, "group", v)
		return v.Error()
	case "lag":
		v, e := admin.Lag(ctx, r.Strings("groups")...)
		if e != nil {
			return e
		}
		send(emit, "lag", v)
		return v.Error()
	case "offsets":
		v, e := admin.ListEndOffsets(ctx, topic)
		if e != nil {
			return e
		}
		send(emit, "offsets", v)
		return v.Error()
	case "create-topic":
		if topic == "" {
			return fmt.Errorf("topic required")
		}
		v, e := admin.CreateTopic(ctx, int32(r.Int("partitions", 1)), int16(r.Int("replication_factor", 1)), nil, topic)
		if e != nil {
			return e
		}
		send(emit, "created", v)
		return v.Err
	case "delete-topic":
		if topic == "" {
			return fmt.Errorf("topic required")
		}
		v, e := admin.DeleteTopic(ctx, topic)
		if e != nil {
			return e
		}
		send(emit, "deleted", v)
		return v.Err
	case "alter-topic":
		if topic == "" {
			return fmt.Errorf("topic required")
		}
		params, ok := r.Params["configs"].(map[string]any)
		if !ok || len(params) == 0 {
			return fmt.Errorf("configs mapping required")
		}
		var configs []kadm.AlterConfig
		for name, value := range params {
			v := fmt.Sprint(value)
			configs = append(configs, kadm.AlterConfig{Op: kadm.SetConfig, Name: name, Value: &v})
		}
		v, e := admin.AlterTopicConfigs(ctx, configs, topic)
		if e != nil {
			return e
		}
		send(emit, "altered", v)
		for _, response := range v {
			if response.Err != nil {
				return response.Err
			}
		}
		return nil
	case "produce":
		if topic == "" {
			return fmt.Errorf("topic required")
		}
		record := &kgo.Record{Topic: topic, Key: []byte(r.String("key", "")), Value: []byte(r.String("value", ""))}
		if len(record.Value) > maxBody {
			return fmt.Errorf("record exceeds 4 MiB")
		}
		if e := cl.ProduceSync(ctx, record).FirstErr(); e != nil {
			return e
		}
		send(emit, "produced", map[string]any{"topic": topic, "partition": record.Partition, "offset": record.Offset})
		return nil
	case "consume":
		limit := r.Int("limit", 100)
		if limit < 1 || limit > 100000 {
			return fmt.Errorf("limit must be 1..100000")
		}
		filter := r.String("filter", "")
		for n := 0; n < limit; {
			fetches := cl.PollFetches(ctx)
			if e := fetches.Err(); e != nil {
				return e
			}
			it := fetches.RecordIter()
			for !it.Done() && n < limit {
				v := it.Next()
				if filter != "" && !strings.Contains(string(v.Value), filter) {
					continue
				}
				var value any
				if json.Unmarshal(v.Value, &value) != nil {
					value = string(v.Value)
				}
				send(emit, "record", map[string]any{"topic": v.Topic, "partition": v.Partition, "offset": v.Offset, "key": string(v.Key), "value": value, "timestamp": v.Timestamp, "headers": v.Headers})
				n++
			}
		}
		return nil
	default:
		return unsupported(r, "topics", "brokers", "groups", "group", "lag", "offsets", "create-topic", "delete-topic", "alter-topic", "produce", "consume", "schemas", "schema", "register-schema", "connectors", "connector", "update-connector")
	}
}
