package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type mqttRetainedValue struct {
	Topic       string `json:"topic"`
	PayloadHash string `json:"payload_sha256"`
	Qos         byte   `json:"qos"`
	message     map[string]any
	bytes       int
}

// MQTT 3.1.1 has no end-of-retained marker and no compare-and-delete operation.
// Scan only within a bounded window; confirmation names exact observed topics,
// never the wildcard itself. A changed second observation fails before writing.
func mqttRetained(ctx context.Context, r config.Request, settings mqttSettings, client mqtt.Client, lost <-chan error, emit Emit) error {
	var mu sync.Mutex
	values := make(map[string]mqttRetainedValue)
	totalBytes := 0
	active := true
	var scanError error
	failed := make(chan struct{}, 1)
	handler := func(_ mqtt.Client, m mqtt.Message) {
		if !m.Retained() {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if !active || scanError != nil {
			return
		}
		fail := func(err error) {
			scanError = err
			select {
			case failed <- struct{}{}:
			default:
			}
		}
		if len(m.Payload()) > maxBody {
			fail(fmt.Errorf("MQTT 保留载荷超过 4 MiB"))
			return
		}
		if !mqttValidTopic(m.Topic(), false) {
			fail(fmt.Errorf("MQTT broker 返回无效主题"))
			return
		}
		previous, exists := values[m.Topic()]
		if !exists && len(values) >= settings.maxTopics {
			fail(fmt.Errorf("保留主题超过 max_topics=%d；未执行清理，请缩小过滤器", settings.maxTopics))
			return
		}
		totalBytes += len(m.Topic()) + len(m.Payload()) - previous.bytes
		if totalBytes > 8<<20 {
			fail(fmt.Errorf("MQTT 保留预览超过 8 MiB；未执行清理，请缩小过滤器"))
			return
		}
		hash := sha256.Sum256(m.Payload())
		values[m.Topic()] = mqttRetainedValue{Topic: m.Topic(), PayloadHash: hex.EncodeToString(hash[:]), Qos: m.Qos(), message: mqttMessage(m.Topic(), m.Payload(), m.Qos(), true), bytes: len(m.Topic()) + len(m.Payload())}
	}
	if err := mqttSubscribeFilters(ctx, client, settings.topics, settings.qos, handler); err != nil {
		return err
	}
	send(emit, "subscribed", settings.topics)
	timer := time.NewTimer(settings.scanDuration)
	defer timer.Stop()
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-lost:
		if err == nil {
			err = fmt.Errorf("MQTT 保留预览期间连接断开")
		}
	case <-failed:
	case <-timer.C:
	}
	mu.Lock()
	active = false
	if scanError != nil {
		err = scanError
	}
	mu.Unlock()
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if !client.IsConnected() {
		return fmt.Errorf("MQTT 保留预览期间连接断开，未执行清理")
	}
	ordered := make([]mqttRetainedValue, 0, len(values))
	for _, value := range values {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Topic < ordered[j].Topic })
	topics := make([]string, 0, len(ordered))
	for _, value := range ordered {
		topics = append(topics, value.Topic)
		send(emit, "message", value.message)
	}
	token := mqttRetainedToken(r.Endpoint, settings.topics, ordered)
	send(emit, "retained-preview", map[string]any{
		"topics": topics, "count": len(topics), "filters": settings.topics, "confirm_token": token,
		"scan_duration_ms": settings.scanDuration.Milliseconds(), "bounded_snapshot": true,
		"warning": "MQTT 无完整快照或原子比较删除；仅清理本次确认的精确主题。请先暂停相关保留消息发布者。",
	})
	if r.Action == "preview-retained" {
		return nil
	}
	confirmed := append([]string(nil), settings.confirmTopics...)
	sort.Strings(confirmed)
	if !slices.Equal(confirmed, topics) || !strings.EqualFold(settings.confirmToken, token) {
		return fmt.Errorf("保留主题或载荷已变化，或确认值不匹配；未执行清理，请重新预览并确认")
	}
	// Acknowledge each removal before advancing. Do not silently replay any
	// destructive batch after a disconnect or retry against an expanded tree.
	qos := settings.qos
	if qos == 0 {
		qos = 1
	}
	for i, topic := range topics {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("保留清理已完成 %d/%d 个主题：%w", i, len(topics), err)
		}
		if err := waitMQTT(ctx, client.Publish(topic, qos, true, []byte{})); err != nil {
			return fmt.Errorf("保留清理已确认完成 %d/%d 个主题；当前主题结果可能未知：%w", i, len(topics), err)
		}
		send(emit, "retained-cleaned", map[string]any{"topic": topic, "qos": qos, "bytes": 0, "retained": true})
	}
	send(emit, "retained-cleanup-complete", map[string]any{"topics": topics, "count": len(topics)})
	return nil
}

func mqttRetainedToken(endpoint string, filters []string, values []mqttRetainedValue) string {
	filters = append([]string(nil), filters...)
	sort.Strings(filters)
	filters = slices.Compact(filters)
	// Only hashes, topic names and delivered QoS are marshalled; raw payloads
	// and credentials never appear inside the returned confirmation value.
	body, _ := json.Marshal(struct {
		Version  int                 `json:"version"`
		Endpoint string              `json:"endpoint"`
		Filters  []string            `json:"filters"`
		Values   []mqttRetainedValue `json:"values"`
	}{1, endpoint, filters, values})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
