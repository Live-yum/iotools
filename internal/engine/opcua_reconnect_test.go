package engine

import (
	"context"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua/ua"
	"io"
	"testing"
)

func TestOPCUARestoreSubscriptionBoundsAndNoWriteReplay(t *testing.T) {
	calls := 0
	r := config.Request{Action: "subscribe", Params: map[string]any{"max_events": 2, "reconnect_interval_ms": 1}}
	err := runOPCUARestoring(context.Background(), r, nil, func(_ context.Context, r config.Request, emit Emit) error {
		calls++
		if r.Int("max_events", 0) != 3-calls {
			t.Fatal("event count reset across reconnect")
		}
		emit(Event{Kind: "notification"})
		if calls == 1 {
			return io.EOF
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("%d %v", calls, err)
	}
	for _, action := range []string{"write", "call"} {
		calls = 0
		r.Action = action
		err = runOPCUARestoring(context.Background(), r, nil, func(context.Context, config.Request, Emit) error { calls++; return io.EOF })
		if calls != 1 || err == nil {
			t.Fatal("write replayed")
		}
	}
	if retryOPCUAError(ua.StatusBadCertificateInvalid) || retryOPCUAError(ua.StatusBadNodeIDUnknown) {
		t.Fatal("non-transient rejection retried")
	}
}
