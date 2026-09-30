package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Live-yum/iotools/internal/config"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func waitMQTT(ctx context.Context, t mqtt.Token) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.Done():
		return t.Error()
	}
}
func runMQTT(ctx context.Context, r config.Request, emit Emit) error {
	switch r.Action {
	case "publish", "subscribe", "read-one", "preview-retained", "clean-retained":
	default:
		return unsupported(r, "publish", "subscribe", "read-one", "preview-retained", "clean-retained")
	}
	settings, e := mqttSettingsFor(r)
	if e != nil {
		return e
	}
	if r.Action == "subscribe" || r.Action == "read-one" {
		return mqttSubscribe(ctx, r, settings, emit)
	}
	var payload []byte
	if r.Action == "publish" {
		payload, e = mqttPayload(r)
		if e != nil {
			return e
		}
	}
	client, lost, e := mqttConnect(ctx, r, settings)
	if e != nil {
		return e
	}
	defer client.Disconnect(100)
	if r.Action == "preview-retained" || settings.snapshotCleanup {
		return mqttRetained(ctx, r, settings, client, lost, emit)
	}
	retain := r.Bool("retain")
	if r.Action == "clean-retained" {
		payload = nil
		retain = true
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if e = waitMQTT(ctx, client.Publish(settings.topics[0], settings.qos, retain, payload)); e != nil {
		return e
	}
	send(emit, "published", map[string]any{"topic": settings.topics[0], "bytes": len(payload), "qos": settings.qos, "retained": retain})
	return nil
}

type mqttSettings struct {
	topics            []string
	qos               byte
	clientID          string
	autoReconnect     bool
	reconnectInterval time.Duration
	scanDuration      time.Duration
	maxTopics         int
	snapshotCleanup   bool
	confirmTopics     []string
	confirmToken      string
}

func mqttSettingsFor(r config.Request) (mqttSettings, error) {
	s := mqttSettings{qos: byte(r.Int("qos", 1)), autoReconnect: true, reconnectInterval: 500 * time.Millisecond, scanDuration: 500 * time.Millisecond, maxTopics: 1000}
	if q := r.Int("qos", 1); q < 0 || q > 2 {
		return s, fmt.Errorf("MQTT qos 需要 0、1 或 2")
	}
	if v, ok := r.Params["topics"]; ok {
		var err error
		s.topics, err = mqttStringList(v, "topics")
		if err != nil {
			return s, err
		}
	} else {
		topic, ok := r.Params["topic"].(string)
		if !ok {
			return s, fmt.Errorf("MQTT 需要非空 topic 或 topics")
		}
		s.topics = []string{topic}
	}
	if len(s.topics) == 0 || len(s.topics) > 1000 {
		return s, fmt.Errorf("MQTT 需要 1..1000 个主题过滤器")
	}
	for _, topic := range s.topics {
		if !mqttValidTopic(topic, r.Action != "publish") {
			return s, fmt.Errorf("MQTT 主题或过滤器无效：%q", topic)
		}
	}
	if r.Action == "publish" && len(s.topics) != 1 {
		return s, fmt.Errorf("MQTT publish 需要一个精确主题")
	}
	if v, ok := r.Params["auto_reconnect"]; ok {
		var valid bool
		s.autoReconnect, valid = v.(bool)
		if !valid {
			return s, fmt.Errorf("auto_reconnect 需要 YAML 布尔值")
		}
	}
	for name, target := range map[string]*time.Duration{"reconnect_interval_ms": &s.reconnectInterval, "scan_duration_ms": &s.scanDuration} {
		if v, ok := r.Params[name]; ok {
			n, err := exactInt(v)
			if err != nil || n < 100 || n > 30000 {
				return s, fmt.Errorf("%s 需要 100..30000 的整数", name)
			}
			*target = time.Duration(n) * time.Millisecond
		}
	}
	if v, ok := r.Params["max_topics"]; ok {
		n, err := exactInt(v)
		if err != nil || n < 1 || n > 10000 {
			return s, fmt.Errorf("max_topics 需要 1..10000 的整数")
		}
		s.maxTopics = int(n)
	}
	s.snapshotCleanup = r.Action == "clean-retained" && (len(s.topics) != 1 || strings.ContainsAny(s.topics[0], "+#"))
	if v, ok := r.Params["confirm_token"]; ok {
		var valid bool
		s.confirmToken, valid = v.(string)
		decoded, err := hex.DecodeString(s.confirmToken)
		if !valid || err != nil || len(decoded) != 32 {
			return s, fmt.Errorf("confirm_token 需要 preview-retained 输出的 SHA-256 确认值")
		}
		if r.Action == "clean-retained" {
			s.snapshotCleanup = true
		}
	}
	if v, ok := r.Params["confirm_topics"]; ok {
		var err error
		s.confirmTopics, err = mqttStringList(v, "confirm_topics")
		if err != nil {
			return s, err
		}
		if len(s.confirmTopics) > s.maxTopics {
			return s, fmt.Errorf("确认主题数超过 max_topics")
		}
		seen := map[string]bool{}
		for _, topic := range s.confirmTopics {
			if !mqttValidTopic(topic, false) || seen[topic] {
				return s, fmt.Errorf("confirm_topics 必须是不重复的精确主题")
			}
			seen[topic] = true
		}
		if r.Action == "clean-retained" {
			s.snapshotCleanup = true
		}
	}
	if s.snapshotCleanup {
		if _, ok := r.Params["confirm_topics"]; !ok || s.confirmToken == "" {
			return s, fmt.Errorf("递归保留清理需要先执行 preview-retained，核对后提供 confirm_topics、confirm_token，并明确允许写入")
		}
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return s, err
	}
	s.clientID = r.String("client_id", "iotools-"+hex.EncodeToString(b))
	return s, nil
}

func mqttStringList(v any, name string) ([]string, error) {
	switch values := v.(type) {
	case string:
		return []string{values}, nil
	case []string:
		return append([]string(nil), values...), nil
	case []any:
		result := make([]string, len(values))
		for i, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("%s 需要字符串列表", name)
			}
			result[i] = text
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s 需要字符串列表", name)
	}
}

func mqttValidTopic(topic string, filter bool) bool {
	if topic == "" || len(topic) > 65535 || !utf8.ValidString(topic) || strings.ContainsRune(topic, 0) {
		return false
	}
	if !filter {
		return !strings.ContainsAny(topic, "+#")
	}
	levels := strings.Split(topic, "/")
	for i, level := range levels {
		if strings.Contains(level, "#") && (level != "#" || i != len(levels)-1) {
			return false
		}
		if strings.Contains(level, "+") && level != "+" {
			return false
		}
	}
	return true
}

// Every retry creates a fresh Paho client; a disconnected client's worker
// goroutines may still be shutting down and that client must never be reused.
func mqttConnect(ctx context.Context, r config.Request, s mqttSettings) (mqtt.Client, <-chan error, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	endpoint := r.Endpoint
	if strings.HasPrefix(endpoint, "mqtt://") {
		endpoint = "tcp://" + strings.TrimPrefix(endpoint, "mqtt://")
	}
	if strings.HasPrefix(endpoint, "mqtts://") {
		endpoint = "ssl://" + strings.TrimPrefix(endpoint, "mqtts://")
	}
	duration := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < duration {
		duration = time.Until(deadline)
	}
	if duration <= 0 {
		return nil, nil, ctx.Err()
	}
	lost := make(chan error, 1)
	o := mqtt.NewClientOptions().AddBroker(endpoint).SetClientID(s.clientID).SetCleanSession(true).SetAutoReconnect(false).SetConnectRetry(false).SetConnectTimeout(duration).SetWriteTimeout(duration)
	o.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		select {
		case lost <- err:
		default:
		}
	})
	if user := r.String("username", ""); user != "" {
		o.SetUsername(user)
		o.SetPassword(r.String("password", ""))
	}
	tls, err := tlsConfig(r)
	if err != nil {
		return nil, nil, err
	}
	o.SetTLSConfig(tls)
	client := mqtt.NewClient(o)
	if err = waitMQTT(ctx, client.Connect()); err != nil {
		client.Disconnect(0)
		return nil, nil, err
	}
	return client, lost, nil
}

func mqttSubscribeFilters(ctx context.Context, client mqtt.Client, topics []string, qos byte, handler mqtt.MessageHandler) error {
	filters := make(map[string]byte, len(topics))
	for _, topic := range topics {
		filters[topic] = qos
	}
	token := client.SubscribeMultiple(filters, handler)
	if err := waitMQTT(ctx, token); err != nil {
		return err
	}
	if ack, ok := token.(*mqtt.SubscribeToken); ok {
		for _, topic := range topics {
			q, found := ack.Result()[topic]
			if !found || q > 2 {
				return fmt.Errorf("MQTT broker 拒绝订阅 %q", topic)
			}
		}
	}
	return nil
}

func mqttSubscribe(ctx context.Context, r config.Request, s mqttSettings, emit Emit) error {
	limit := r.Int("limit", 100)
	if r.Action == "read-one" {
		limit = 1
	}
	if limit < 1 || limit > 100000 {
		return fmt.Errorf("limit 需要 1..100000")
	}
	messages := make(chan map[string]any, 32)
	failures := make(chan error, 1)
	var queuedBytes atomic.Int64
	handler := func(_ mqtt.Client, m mqtt.Message) {
		if r.Bool("ignore_retained") && m.Retained() {
			return
		}
		fail := func(err error) {
			select {
			case failures <- err:
			default:
			}
		}
		if len(m.Payload()) > maxBody {
			fail(fmt.Errorf("MQTT 载荷超过 4 MiB"))
			return
		}
		size := int64(len(m.Payload()))
		if queuedBytes.Add(size) > 8<<20 {
			queuedBytes.Add(-size)
			fail(fmt.Errorf("MQTT 队列超过 8 MiB，请降低消息速率"))
			return
		}
		v := mqttMessage(m.Topic(), m.Payload(), m.Qos(), m.Retained())
		v["_queued_bytes"] = size
		select {
		case messages <- v:
		default:
			queuedBytes.Add(-size)
			fail(fmt.Errorf("MQTT 消费积压超过 32 条消息"))
		}
	}
	connectedBefore := false
	attempt := 0
	for n := 0; n < limit; {
		if err := ctx.Err(); err != nil {
			return err
		}
		client, lost, err := mqttConnect(ctx, r, s)
		if err != nil {
			if !connectedBefore || !s.autoReconnect {
				return err
			}
		} else {
			err = mqttSubscribeFilters(ctx, client, s.topics, s.qos, handler)
			if err == nil {
				if connectedBefore {
					send(emit, "reconnected", map[string]any{"attempt": attempt})
				}
				connectedBefore = true
				attempt = 0
				send(emit, "subscribed", s.topics)
				for err == nil && n < limit {
					select {
					case <-ctx.Done():
						err = ctx.Err()
					case err = <-failures:
						client.Disconnect(0)
						return err
					case err = <-lost:
						if err == nil {
							err = fmt.Errorf("MQTT 连接已断开")
						}
					case v := <-messages:
						queuedBytes.Add(-v["_queued_bytes"].(int64))
						delete(v, "_queued_bytes")
						send(emit, "message", v)
						n++
					}
				}
			}
			client.Disconnect(100)
			if n >= limit {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !connectedBefore || !s.autoReconnect {
				return err
			}
		}
		attempt++
		send(emit, "reconnecting", map[string]any{"attempt": attempt, "delay_ms": s.reconnectInterval.Milliseconds(), "error": err.Error()})
		timer := time.NewTimer(s.reconnectInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func mqttPayload(r config.Request) ([]byte, error) {
	text := r.String("payload", "")
	if len(text) > 2*maxBody {
		return nil, fmt.Errorf("MQTT 编码载荷过大")
	}
	var b []byte
	var e error
	switch r.String("payload_encoding", "text") {
	case "text":
		b = []byte(text)
	case "base64":
		b, e = base64.StdEncoding.Strict().DecodeString(text)
	case "hex":
		b, e = hex.DecodeString(strings.Join(strings.Fields(text), ""))
	default:
		return nil, fmt.Errorf("payload_encoding 需要 text/base64/hex")
	}
	if e != nil {
		return nil, fmt.Errorf("MQTT 载荷编码无效")
	}
	if len(b) > maxBody {
		return nil, fmt.Errorf("MQTT 载荷超过 4 MiB")
	}
	return b, nil
}
func mqttMessage(topic string, payload []byte, qos byte, retained bool) map[string]any {
	valid := utf8.Valid(payload)
	text := "（二进制载荷，请查看 HEX/Base64）"
	if valid {
		text = string(payload)
	}
	result := map[string]any{"topic": topic, "payload": text, "payload_utf8": valid, "payload_hex": hex.EncodeToString(payload), "payload_base64": base64.StdEncoding.EncodeToString(payload), "bytes": len(payload), "qos": qos, "retained": retained}
	if valid {
		var value any
		d := json.NewDecoder(bytes.NewReader(payload))
		d.UseNumber()
		if d.Decode(&value) == nil && json.Valid(payload) {
			result["payload_json"] = value
		}
	}
	return result
}
