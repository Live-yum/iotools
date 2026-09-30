package engine

import (
	"context"
	"github.com/Live-yum/iotools/internal/config"
	server "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"io"
	"log/slog"
	"testing"
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
