package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	server "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"io"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMQTTRealBrokerPublishReadAndClean(t *testing.T) {
	s := server.New(&server.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if e := s.AddHook(new(auth.AllowHook), nil); e != nil {
		t.Fatal(e)
	}
	l := listeners.NewTCP(listeners.Config{ID: "test", Address: "127.0.0.1:0"})
	if e := s.AddListener(l); e != nil {
		t.Fatal(e)
	}
	if e := s.Serve(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		// mochi v2.7.9 GetByListener recursively RLocks Clients via Len; if a
		// disconnect writer is waiting, broker.Close can deadlock. Shut down this
		// test listener with a snapshot callback (one RLock only) before CloseAll.
		l.Close(func(_ string) {
			for _, client := range s.Clients.GetAll() {
				client.Stop(nil)
			}
		})
		if e := s.Close(); e != nil {
			t.Errorf("close MQTT fixture: %v", e)
		}
	}()
	r := config.Request{Protocol: "mqtt", Endpoint: "mqtt://" + l.Address(), Action: "publish", Timeout: "3s", Params: map[string]any{"topic": "test/value", "payload": "42", "retain": true, "qos": 1}}
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	r.Action = "read-one"
	seen := false
	if e := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "message" {
			m := e.Data.(map[string]any)
			seen = m["payload"] == "42" && m["retained"] == true
		}
	}); e != nil {
		t.Fatal(e)
	}
	if !seen {
		t.Fatal("retained MQTT value not received")
	}
	r.Action = "clean-retained"
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	r.Action = "read-one"
	r.Timeout = "100ms"
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("cleaned retained value unexpectedly present")
	}
}

func TestMQTTBinaryRepresentations(t *testing.T) {
	for _, params := range []map[string]any{{"payload": "00 ff 80", "payload_encoding": "hex"}, {"payload": "AP+A", "payload_encoding": "base64"}} {
		b, e := mqttPayload(config.Request{Params: params})
		if e != nil || string(b) != string([]byte{0, 255, 128}) {
			t.Fatal("binary publish decoding failed", e)
		}
		m := mqttMessage("binary", b, 1, true)
		if m["payload_utf8"] != false || m["payload_hex"] != "00ff80" || m["payload_base64"] != "AP+A" {
			t.Fatal("binary bytes not preserved")
		}
	}
	m := mqttMessage("json", []byte(`{"n":9007199254740993}`), 0, false)
	b, _ := json.Marshal(m["payload_json"])
	if !strings.Contains(string(b), "9007199254740993") {
		t.Fatal("JSON precision lost")
	}
	for _, encoding := range []string{"base64", "hex", "wrong"} {
		if _, e := mqttPayload(config.Request{Params: map[string]any{"payload": "!", "payload_encoding": encoding}}); e == nil {
			t.Fatal("invalid MQTT encoding accepted")
		}
	}
}

// The fixture always binds a disposable loopback port and has no persistence.
func newMQTTFixture(t *testing.T) (*server.Server, string) {
	t.Helper()
	s := server.New(&server.Options{InlineClient: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := s.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatal(err)
	}
	listener := listeners.NewTCP(listeners.Config{ID: t.Name(), Address: "127.0.0.1:0"})
	if err := s.AddListener(listener); err != nil {
		t.Fatal(err)
	}
	if err := s.Serve(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		listener.Close(func(_ string) {
			for _, client := range s.Clients.GetAll() {
				client.Stop(nil)
			}
		})
		if err := s.Close(); err != nil {
			t.Errorf("close MQTT fixture: %v", err)
		}
	})
	return s, "mqtt://" + listener.Address()
}

func mqttPreviewForTest(t *testing.T, r config.Request) map[string]any {
	t.Helper()
	r.Action = "preview-retained"
	var preview map[string]any
	if err := Run(context.Background(), r, false, func(event Event) {
		if event.Kind == "retained-preview" {
			preview = event.Data.(map[string]any)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if preview == nil {
		t.Fatal("missing retained preview")
	}
	return preview
}

func mqttSeedForTest(t *testing.T, s *server.Server, topic, payload string, retained bool) {
	t.Helper()
	if err := s.Publish(topic, []byte(payload), retained, 1); err != nil {
		t.Fatal(err)
	}
}

func TestMQTTRetainedPreviewAndConfirmedRecursiveCleanup(t *testing.T) {
	s, endpoint := newMQTTFixture(t)
	for _, topic := range []string{"tree/a", "tree/deep/b", "outside/value"} {
		mqttSeedForTest(t, s, topic, "preserve until confirmed", true)
	}
	r := config.Request{Protocol: "mqtt", Action: "preview-retained", Endpoint: endpoint, Timeout: "3s", Params: map[string]any{"topic": "tree/#", "scan_duration_ms": 100, "qos": 1}}
	preview := mqttPreviewForTest(t, r)
	want := []string{"tree/a", "tree/deep/b"}
	if !reflect.DeepEqual(preview["topics"], want) || preview["count"] != 2 || preview["bounded_snapshot"] != true {
		t.Fatalf("incorrect preview: %#v", preview)
	}
	for _, topic := range want {
		if _, present := s.Topics.Retained.Get(topic); !present {
			t.Fatal("preview removed retained topic", topic)
		}
	}
	r.Action = "clean-retained"
	if err := Run(context.Background(), r, true, nil); err == nil || !strings.Contains(err.Error(), "preview-retained") {
		t.Fatal("wildcard cleanup did not demand snapshot confirmation", err)
	}
	r.Params["confirm_topics"] = preview["topics"]
	r.Params["confirm_token"] = preview["confirm_token"]
	if err := Run(context.Background(), r, false, nil); err == nil {
		t.Fatal("confirmation token bypassed write gate")
	}
	var cleaned []string
	if err := Run(context.Background(), r, true, func(event Event) {
		if event.Kind == "retained-cleaned" {
			cleaned = append(cleaned, event.Data.(map[string]any)["topic"].(string))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cleaned, want) {
		t.Fatalf("cleanup acknowledgements: %#v", cleaned)
	}
	for _, topic := range want {
		if _, present := s.Topics.Retained.Get(topic); present {
			t.Fatal("retained topic not removed", topic)
		}
	}
	if _, present := s.Topics.Retained.Get("outside/value"); !present {
		t.Fatal("cleanup escaped approved tree")
	}
}

func TestMQTTRetainedCleanupRejectsChangedSnapshot(t *testing.T) {
	for _, change := range []string{"payload", "added", "removed", "confirmation", "endpoint", "filter"} {
		t.Run(change, func(t *testing.T) {
			s, endpoint := newMQTTFixture(t)
			mqttSeedForTest(t, s, "tree/a", "original", true)
			mqttSeedForTest(t, s, "tree/b", "unchanged", true)
			r := config.Request{Protocol: "mqtt", Endpoint: endpoint, Timeout: "3s", Params: map[string]any{"topic": "tree/#", "scan_duration_ms": 100}}
			preview := mqttPreviewForTest(t, r)
			r.Action = "clean-retained"
			r.Params["confirm_topics"] = preview["topics"]
			r.Params["confirm_token"] = preview["confirm_token"]
			switch change {
			case "payload":
				mqttSeedForTest(t, s, "tree/a", "changed", true)
			case "added":
				mqttSeedForTest(t, s, "tree/c", "new", true)
			case "removed":
				mqttSeedForTest(t, s, "tree/a", "", true)
			case "confirmation":
				r.Params["confirm_topics"] = []string{"tree/a"}
			case "endpoint":
				r.Endpoint = strings.Replace(endpoint, "mqtt://", "tcp://", 1)
			case "filter":
				r.Params["topic"] = "tree/+"
			}
			cleaned := false
			err := Run(context.Background(), r, true, func(event Event) {
				if event.Kind == "retained-cleaned" {
					cleaned = true
				}
			})
			if err == nil || !strings.Contains(err.Error(), "未执行清理") || cleaned {
				t.Fatalf("changed snapshot accepted: %v", err)
			}
			if _, present := s.Topics.Retained.Get("tree/b"); !present {
				t.Fatal("partially deleted after confirmation mismatch")
			}
		})
	}
}

func TestMQTTRetainedPreviewBoundsAndLiveMessageExclusion(t *testing.T) {
	s, endpoint := newMQTTFixture(t)
	for i := 0; i < 45; i++ {
		mqttSeedForTest(t, s, fmt.Sprintf("tree/%03d", i), "value", true)
	}
	r := config.Request{Protocol: "mqtt", Action: "preview-retained", Endpoint: endpoint, Timeout: "3s", Params: map[string]any{"topics": []string{"tree/#", "tree/+"}, "scan_duration_ms": 100, "max_topics": 45}}
	var preview map[string]any
	if err := Run(context.Background(), r, false, func(event Event) {
		if event.Kind == "subscribed" {
			mqttSeedForTest(t, s, "tree/live-only", "transient", false)
		}
		if event.Kind == "retained-preview" {
			preview = event.Data.(map[string]any)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if preview["count"] != 45 {
		t.Fatalf("overlapping filters or transient delivery corrupted preview: %#v", preview)
	}
	r.Params["max_topics"] = 44
	if err := Run(context.Background(), r, false, nil); err == nil || !strings.Contains(err.Error(), "max_topics") {
		t.Fatal("scan silently truncated", err)
	}
	if _, present := s.Topics.Retained.Get("tree/000"); !present {
		t.Fatal("failed scan removed data")
	}
}

func TestMQTTEmptyRetainedSnapshotIsSafe(t *testing.T) {
	_, endpoint := newMQTTFixture(t)
	r := config.Request{Protocol: "mqtt", Endpoint: endpoint, Timeout: "3s", Params: map[string]any{"topic": "empty/#", "scan_duration_ms": 100}}
	preview := mqttPreviewForTest(t, r)
	if preview["count"] != 0 {
		t.Fatalf("unexpected empty preview: %#v", preview)
	}
	r.Action = "clean-retained"
	r.Params["confirm_topics"] = preview["topics"]
	r.Params["confirm_token"] = preview["confirm_token"]
	if err := Run(context.Background(), r, true, nil); err != nil {
		t.Fatal(err)
	}
}

func mqttAwaitEvent(t *testing.T, events <-chan Event, kind string) Event {
	t.Helper()
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Kind == kind {
				return event
			}
		case <-timer.C:
			t.Fatalf("waiting for MQTT %s", kind)
			return Event{}
		}
	}
}

func TestMQTTReconnectResubscribesAllFiltersAndPreservesLimit(t *testing.T) {
	s, endpoint := newMQTTFixture(t)
	mqttSeedForTest(t, s, "devices/a/value", "ignored retained value", true)
	r := config.Request{Protocol: "mqtt", Action: "subscribe", Endpoint: endpoint, Timeout: "10s", Params: map[string]any{"topics": []string{"devices/+/value", "alerts/#"}, "client_id": "iotools-reconnect-test", "limit": 3, "ignore_retained": true, "reconnect_interval_ms": 100}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 32)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, r, false, func(event Event) { events <- event }) }()
	mqttAwaitEvent(t, events, "subscribed")
	for i, topic := range []string{"devices/a/value", "alerts/system", "devices/b/value"} {
		mqttSeedForTest(t, s, topic, fmt.Sprint(i), false)
		event := mqttAwaitEvent(t, events, "message")
		if got := event.Data.(map[string]any)["payload"]; got != fmt.Sprint(i) {
			t.Fatalf("unexpected message across reconnect: %v", got)
		}
		if i < 2 {
			client, present := s.Clients.Get("iotools-reconnect-test")
			if !present {
				t.Fatal("missing subscribed client")
			}
			client.Stop(errors.New("disposable reconnect test"))
			mqttAwaitEvent(t, events, "reconnecting")
			mqttAwaitEvent(t, events, "reconnected")
			mqttAwaitEvent(t, events, "subscribed")
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("message limit was reset by reconnect")
	}
}

func TestMQTTReadOneRecoversBeforeFirstMessage(t *testing.T) {
	s, endpoint := newMQTTFixture(t)
	r := config.Request{Protocol: "mqtt", Action: "read-one", Endpoint: endpoint, Timeout: "5s", Params: map[string]any{"topic": "read/value", "client_id": "iotools-read-reconnect", "reconnect_interval_ms": 100}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 16)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, r, false, func(event Event) { events <- event }) }()
	mqttAwaitEvent(t, events, "subscribed")
	client, _ := s.Clients.Get("iotools-read-reconnect")
	client.Stop(errors.New("disposable reconnect test"))
	mqttAwaitEvent(t, events, "reconnected")
	mqttAwaitEvent(t, events, "subscribed")
	mqttSeedForTest(t, s, "read/value", "first", false)
	if event := mqttAwaitEvent(t, events, "message"); event.Data.(map[string]any)["payload"] != "first" {
		t.Fatal("wrong read-one payload")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMQTTReconnectCanBeDisabledOrCanceled(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			s, endpoint := newMQTTFixture(t)
			r := config.Request{Protocol: "mqtt", Action: "subscribe", Endpoint: endpoint, Timeout: "5s", Params: map[string]any{"topic": "test/value", "client_id": "iotools-cancel-reconnect", "auto_reconnect": !disabled, "reconnect_interval_ms": 30000}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			events := make(chan Event, 16)
			done := make(chan error, 1)
			go func() { done <- Run(ctx, r, false, func(event Event) { events <- event }) }()
			mqttAwaitEvent(t, events, "subscribed")
			client, _ := s.Clients.Get("iotools-cancel-reconnect")
			client.Stop(errors.New("disposable reconnect test"))
			if !disabled {
				mqttAwaitEvent(t, events, "reconnecting")
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("unexpected success after disconnect")
				}
				if !disabled && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("reconnect sleep did not stop promptly")
			}
		})
	}
}

func TestMQTTUnsafeCleanupRejectedBeforeConnecting(t *testing.T) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, params := range []map[string]any{
		{"topic": "tree/#"},
		{"topic": "tree/#", "confirm_token": strings.Repeat("0", 64)},
		{"topic": "tree/#", "confirm_topics": []string{"tree/a"}},
		{"topic": "tree/#", "confirm_topics": []string{"tree/a", "tree/a"}, "confirm_token": strings.Repeat("0", 64)},
		{"topic": "tree/#", "confirm_topics": []string{"tree/+"}, "confirm_token": strings.Repeat("0", 64)},
		{"topic": "tree/a#", "confirm_topics": []string{}, "confirm_token": strings.Repeat("0", 64)},
		{"topic": "tree/#", "scan_duration_ms": 0},
		{"topic": "tree/#", "auto_reconnect": "true"},
	} {
		r := config.Request{Protocol: "mqtt", Action: "clean-retained", Endpoint: "mqtt://" + listener.Addr().String(), Timeout: "100ms", Params: params}
		if err := Run(context.Background(), r, true, nil); err == nil {
			t.Fatal("unsafe cleanup accepted", params)
		}
	}
	listener.SetDeadline(time.Now().Add(30 * time.Millisecond))
	if conn, err := listener.Accept(); err == nil {
		conn.Close()
		t.Fatal("unsafe cleanup opened a connection")
	}
}

func TestMQTTRetainedPreviewAbortsOnInterruptedScan(t *testing.T) {
	for _, action := range []string{"disconnect", "cancel", "timeout"} {
		t.Run(action, func(t *testing.T) {
			s, endpoint := newMQTTFixture(t)
			mqttSeedForTest(t, s, "tree/a", "unchanged", true)
			r := config.Request{Protocol: "mqtt", Action: "preview-retained", Endpoint: endpoint, Timeout: "150ms", Params: map[string]any{"topic": "tree/#", "client_id": "iotools-preview-interrupt", "scan_duration_ms": 30000}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			previewed := false
			err := Run(ctx, r, false, func(event Event) {
				if event.Kind == "retained-preview" {
					previewed = true
				}
				if event.Kind != "subscribed" {
					return
				}
				switch action {
				case "cancel":
					cancel()
				case "disconnect":
					client, _ := s.Clients.Get("iotools-preview-interrupt")
					client.Stop(errors.New("interrupt retained snapshot"))
				}
			})
			if err == nil || previewed {
				t.Fatalf("partial scan produced actionable confirmation: %v", err)
			}
			if _, present := s.Topics.Retained.Get("tree/a"); !present {
				t.Fatal("interrupted scan changed retained data")
			}
		})
	}
}

func TestMQTTReconnectRetriesBrokerOutageWithinOriginalDeadline(t *testing.T) {
	for _, restore := range []bool{true, false} {
		t.Run(fmt.Sprint(restore), func(t *testing.T) {
			s, endpoint := newMQTTFixture(t)
			r := config.Request{Protocol: "mqtt", Action: "read-one", Endpoint: endpoint, Timeout: "700ms", Params: map[string]any{"topic": "outage/value", "client_id": "iotools-outage", "reconnect_interval_ms": 100}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			events := make(chan Event, 32)
			done := make(chan error, 1)
			go func() { done <- Run(ctx, r, false, func(event Event) { events <- event }) }()
			mqttAwaitEvent(t, events, "subscribed")
			listener, exists := s.Listeners.Get(t.Name())
			if !exists {
				t.Fatal("fixture listener missing")
			}
			closeClients := func(_ string) {
				for _, client := range s.Clients.GetAll() {
					client.Stop(nil)
				}
			}
			listener.Close(closeClients)
			mqttAwaitEvent(t, events, "reconnecting")
			failedRetry := mqttAwaitEvent(t, events, "reconnecting")
			if failedRetry.Data.(map[string]any)["attempt"].(int) < 2 {
				t.Fatal("broker outage was not retried")
			}
			if restore {
				replacement := listeners.NewTCP(listeners.Config{ID: t.Name() + "-restored", Address: strings.TrimPrefix(endpoint, "mqtt://")})
				if err := s.AddListener(replacement); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { replacement.Close(closeClients) })
				s.Listeners.Serve(replacement.ID(), s.EstablishConnection)
				mqttAwaitEvent(t, events, "reconnected")
				mqttAwaitEvent(t, events, "subscribed")
				mqttSeedForTest(t, s, "outage/value", "restored", false)
				mqttAwaitEvent(t, events, "message")
			}
			select {
			case err := <-done:
				if restore && err != nil {
					t.Fatal(err)
				}
				if !restore && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("original timeout was not retained: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("reconnect restarted the operation deadline")
			}
		})
	}
}
