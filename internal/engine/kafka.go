package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

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
	case "pause-connector", "resume-connector", "delete-connector", "delete-subject", "purge-subject", "delete-schema", "schema-versions", "schemas", "schema", "register-schema", "connectors", "connector", "update-connector":
		if (r.Action == "schema" || r.Action == "schema-versions" || r.Action == "register-schema" || r.Action == "delete-subject" || r.Action == "purge-subject" || r.Action == "delete-schema") && r.String("subject", "") == "" {
			return fmt.Errorf("subject required")
		}
		if (r.Action == "connector" || r.Action == "update-connector" || r.Action == "pause-connector" || r.Action == "resume-connector" || r.Action == "delete-connector") && r.String("connector", "") == "" {
			return fmt.Errorf("connector required")
		}
		if (r.Action == "schema" || r.Action == "delete-schema") && r.String("version", "latest") != "latest" {
			n, e := exactInt(r.Params["version"])
			if e != nil || n < 1 {
				return fmt.Errorf("schema version requires positive integer or latest")
			}
		}
		if r.Action == "purge-subject" && r.String("confirm_subject", "") != r.String("subject", "") {
			return fmt.Errorf("永久删除必须明确提供匹配的 confirm_subject")
		}
		h := r
		h.Protocol = "http"
		h.Action = "GET"
		switch r.Action {
		case "pause-connector", "resume-connector":
			h.Action = "PUT"
			operation := strings.TrimSuffix(r.Action, "-connector")
			h.Endpoint = strings.TrimRight(r.Endpoint, "/") + "/connectors/" + url.PathEscape(r.String("connector", "")) + "/" + operation
		case "delete-connector":
			h.Action = "DELETE"
			h.Endpoint = strings.TrimRight(r.Endpoint, "/") + "/connectors/" + url.PathEscape(r.String("connector", ""))
		case "delete-subject", "purge-subject":
			h.Action = "DELETE"
			h.Endpoint = strings.TrimRight(r.Endpoint, "/") + "/subjects/" + url.PathEscape(r.String("subject", ""))
			if r.Action == "purge-subject" {
				h.Endpoint += "?permanent=true"
			}
		case "delete-schema":
			h.Action = "DELETE"
			h.Endpoint = strings.TrimRight(r.Endpoint, "/") + "/subjects/" + url.PathEscape(r.String("subject", "")) + "/versions/" + url.PathEscape(r.String("version", "latest"))
		case "schema-versions":
			h.Endpoint = strings.TrimRight(r.Endpoint, "/") + "/subjects/" + url.PathEscape(r.String("subject", "")) + "/versions"
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
	registry, err := newKafkaRegistry(r)
	if err != nil {
		return err
	}
	defer registry.close()
	if r.Action == "produce" || r.Action == "consume" {
		for _, part := range []string{"key", "value"} {
			if _, err := kafkaFormat(r, part); err != nil {
				return err
			}
		}
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
		if value := r.String("offset", "earliest"); value != "earliest" && value != "latest" && value != "most-recent" {
			return fmt.Errorf("offset requires earliest/latest/most-recent")
		}
		if start := r.String("start_time", ""); start != "" {
			timestamp, e := kafkaStartTime(start, time.Now())
			if e != nil {
				return e
			}
			offset = kgo.NewOffset().AfterMilli(timestamp.UnixMilli())
		}
		if r.String("offset", "earliest") == "most-recent" {
			// Explicit finite assignments are installed after querying current end offsets.
		} else if selections, exists := r.Params["consume_partitions"]; exists {
			ids, e := kafkaPartitions(selections)
			if e != nil {
				return e
			}
			assigned := map[int32]kgo.Offset{}
			for _, id := range ids {
				assigned[id] = offset
			}
			opts = append(opts, kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{topic: assigned}))
		} else if _, exists := r.Params["partition"]; exists {
			opts = append(opts, kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{topic: {int32(r.Int("partition", 0)): offset}}))
		} else {
			opts = append(opts, kgo.ConsumeTopics(topic), kgo.ConsumeResetOffset(offset))
		}
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
	case "delete-group":
		group := r.String("group", "")
		if group == "" {
			return fmt.Errorf("group required")
		}
		v, e := admin.DeleteGroup(ctx, group)
		if e != nil {
			return e
		}
		send(emit, "group-deleted", v)
		return v.Err
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
	case "topic-config":
		if topic == "" {
			return fmt.Errorf("topic required")
		}
		v, e := admin.DescribeTopicConfigs(ctx, topic)
		if e != nil {
			return e
		}
		send(emit, "topic-config", v)
		for _, item := range v {
			if item.Err != nil {
				return item.Err
			}
		}
		return nil
	case "expand-partitions":
		if topic == "" {
			return fmt.Errorf("topic required")
		}
		if _, ok := r.Params["partitions"]; !ok {
			return fmt.Errorf("explicit final partitions required")
		}
		v, e := admin.UpdatePartitions(ctx, r.Int("partitions", 0), topic)
		if e != nil {
			return e
		}
		send(emit, "partitions-expanded", v)
		for _, item := range v {
			if item.Err != nil {
				return item.Err
			}
		}
		return nil
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
		key, err := kafkaEncode(ctx, r, registry, "key")
		if err != nil {
			return err
		}
		value, err := kafkaEncode(ctx, r, registry, "value")
		if err != nil {
			return err
		}
		record := &kgo.Record{Topic: topic, Key: key, Value: value}
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
		var recentEnds map[int32]int64
		if r.String("offset", "earliest") == "most-recent" {
			var err error
			recentEnds, err = assignKafkaRecent(ctx, r, cl, admin, limit)
			if err != nil {
				return err
			}
			if len(recentEnds) == 0 {
				send(emit, "consume-complete", map[string]any{"records": 0, "reason": "当前快照为空"})
				return nil
			}
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
				if recentEnds != nil {
					end, exists := recentEnds[v.Partition]
					if !exists {
						continue
					}
					if v.Offset >= end-1 {
						delete(recentEnds, v.Partition)
						cl.PauseFetchPartitions(map[string][]int32{topic: {v.Partition}})
					}
					if v.Offset >= end {
						continue
					}
				}
				value, err := kafkaDecode(ctx, r, registry, "value", v.Value)
				if err != nil {
					return err
				}
				key, err := kafkaDecode(ctx, r, registry, "key", v.Key)
				if err != nil {
					return err
				}
				if !kafkaMatchFilter(key, r.String("key_filter", ""), r.String("key_prefix", "")) || !kafkaMatchFilter(value, "", r.String("value_prefix", "")) {
					continue
				}
				rendered, _ := json.Marshal(value)
				if filter != "" && !strings.Contains(string(rendered), filter) && !strings.Contains(fmt.Sprint(value), filter) {
					continue
				}
				send(emit, "record", map[string]any{"topic": v.Topic, "partition": v.Partition, "offset": v.Offset, "key": key, "value": value, "timestamp": v.Timestamp, "headers": v.Headers, "raw_key_base64": base64.StdEncoding.EncodeToString(v.Key), "raw_value_base64": base64.StdEncoding.EncodeToString(v.Value), "key_is_null": v.Key == nil, "value_is_null": v.Value == nil})
				n++
			}
			if recentEnds != nil && len(recentEnds) == 0 {
				return nil
			}
		}
		return nil
	default:
		return unsupported(r, "topics", "brokers", "groups", "group", "lag", "offsets", "create-topic", "delete-topic", "alter-topic", "produce", "consume", "schemas", "schema", "register-schema", "connectors", "connector", "update-connector")
	}
}

func kafkaStartTime(value string, now time.Time) (time.Time, error) {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch value {
	case "today":
		return today, nil
	case "yesterday":
		return today.AddDate(0, 0, -1), nil
	case "last7days":
		return now.AddDate(0, 0, -7), nil
	}
	t, e := time.Parse(time.RFC3339Nano, value)
	if e != nil {
		return time.Time{}, fmt.Errorf("start_time 需要 today/yesterday/last7days 或带时区 RFC3339")
	}
	return t, nil
}
func kafkaMatchFilter(value any, contains, prefix string) bool {
	text, ok := value.(string)
	if !ok {
		b, _ := json.Marshal(value)
		text = string(b)
	}
	return (contains == "" || strings.Contains(text, contains)) && (prefix == "" || strings.HasPrefix(text, prefix))
}

func kafkaPartitions(raw any) ([]int32, error) {
	values, ok := raw.([]any)
	if !ok || len(values) == 0 || len(values) > 1024 {
		return nil, fmt.Errorf("consume_partitions 必须是1..1024个分区编号")
	}
	out := []int32{}
	seen := map[int64]bool{}
	for _, value := range values {
		n, e := exactInt(value)
		if e != nil || n < 0 || n > 2147483647 || seen[n] {
			return nil, fmt.Errorf("分区编号必须唯一且为0..2147483647的整数")
		}
		seen[n] = true
		out = append(out, int32(n))
	}
	return out, nil
}
