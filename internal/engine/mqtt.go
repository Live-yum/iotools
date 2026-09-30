package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

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
	case "publish", "subscribe", "read-one", "clean-retained":
	default:
		return unsupported(r, "publish", "subscribe", "read-one", "clean-retained")
	}
	endpoint := r.Endpoint
	if strings.HasPrefix(endpoint, "mqtt://") {
		endpoint = "tcp://" + strings.TrimPrefix(endpoint, "mqtt://")
	}
	if strings.HasPrefix(endpoint, "mqtts://") {
		endpoint = "ssl://" + strings.TrimPrefix(endpoint, "mqtts://")
	}
	q := r.Int("qos", 1)
	if q < 0 || q > 2 {
		return fmt.Errorf("MQTT qos must be 0, 1 or 2")
	}
	topics := r.Strings("topics")
	if len(topics) == 0 {
		topics = []string{r.String("topic", "")}
	}
	for _, t := range topics {
		if t == "" {
			return fmt.Errorf("MQTT topic is required")
		}
	}
	if (r.Action == "publish" || r.Action == "clean-retained") && (len(topics) != 1 || strings.ContainsAny(topics[0], "+#")) {
		return fmt.Errorf("publish/clean-retained require one exact topic; wildcard deletion is prohibited")
	}
	b := make([]byte, 8)
	if _, e := rand.Read(b); e != nil {
		return e
	}
	duration, _ := r.Duration()
	o := mqtt.NewClientOptions().AddBroker(endpoint).SetClientID(r.String("client_id", "iotools-"+hex.EncodeToString(b))).SetCleanSession(true).SetAutoReconnect(false).SetConnectRetry(false).SetConnectTimeout(duration).SetWriteTimeout(duration)
	if user := r.String("username", ""); user != "" {
		o.SetUsername(user)
		o.SetPassword(r.String("password", ""))
	}
	tls, e := tlsConfig(r)
	if e != nil {
		return e
	}
	o.SetTLSConfig(tls)
	client := mqtt.NewClient(o)
	if e = waitMQTT(ctx, client.Connect()); e != nil {
		client.Disconnect(0)
		return e
	}
	defer client.Disconnect(100)
	if r.Action == "publish" || r.Action == "clean-retained" {
		payload := r.String("payload", "")
		retain := r.Bool("retain")
		if r.Action == "clean-retained" {
			payload = ""
			retain = true
		}
		if e = waitMQTT(ctx, client.Publish(topics[0], byte(q), retain, payload)); e != nil {
			return e
		}
		send(emit, "published", map[string]any{"topic": topics[0], "bytes": len(payload), "qos": q, "retained": retain})
		return nil
	}
	messages := make(chan map[string]any, 256)
	errors := make(chan error, 1)
	filters := map[string]byte{}
	for _, t := range topics {
		filters[t] = byte(q)
	}
	handler := func(_ mqtt.Client, m mqtt.Message) {
		if r.Bool("ignore_retained") && m.Retained() {
			return
		}
		if len(m.Payload()) > maxBody {
			select {
			case errors <- fmt.Errorf("MQTT payload exceeds 4 MiB"):
			default:
			}
			return
		}
		v := map[string]any{"topic": m.Topic(), "payload": string(m.Payload()), "qos": m.Qos(), "retained": m.Retained()}
		select {
		case messages <- v:
		default:
			select {
			case errors <- fmt.Errorf("MQTT consumer fell behind (256 queued messages)"):
			default:
			}
		}
	}
	if e = waitMQTT(ctx, client.SubscribeMultiple(filters, handler)); e != nil {
		return e
	}
	send(emit, "subscribed", topics)
	limit := r.Int("limit", 100)
	if r.Action == "read-one" {
		limit = 1
	}
	if limit < 1 || limit > 100000 {
		return fmt.Errorf("limit must be 1..100000")
	}
	for n := 0; n < limit; {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-errors:
			return e
		case v := <-messages:
			send(emit, "message", v)
			n++
		case <-time.After(time.Second):
			if !client.IsConnected() {
				return fmt.Errorf("MQTT disconnected")
			}
		}
	}
	return nil
}
