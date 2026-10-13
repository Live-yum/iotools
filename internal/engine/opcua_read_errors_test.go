package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua/ua"
)

func TestOPCUAReadReportedPartialFailuresContinueAndKeepValuePresence(t *testing.T) {
	for _, mode := range []string{"bad-node", "read-service", "incomplete", "nil-response"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			reader := functionUAReader(func(_ context.Context, r *ua.ReadRequest) (*ua.ReadResponse, error) {
				calls++
				if calls == 1 {
					switch mode {
					case "read-service":
						return nil, ua.StatusBadNotReadable
					case "incomplete":
						return &ua.ReadResponse{}, nil
					case "nil-response":
						return nil, nil
					}
				}
				result := &ua.ReadResponse{}
				for i := range r.NodesToRead {
					v := &ua.DataValue{Value: ua.MustVariant(int32(7))}
					if calls == 1 && i == 0 && mode == "bad-node" {
						v = &ua.DataValue{Status: ua.StatusBadNodeIDUnknown}
					}
					result.Results = append(result.Results, v)
				}
				return result, nil
			})
			var values []map[string]any
			r := config.Request{Action: "read", Params: map[string]any{"node_ids": []string{"i=1", "i=2", "i=3"}, "read_batch_size": 2}}
			err := opcuaReadAttributes(context.Background(), reader, r, func(event Event) {
				values = append(values, event.Data.(map[string]any))
			})
			if err == nil || !OPCUAReadCanContinue(err) || calls != 2 || len(values) != 3 {
				t.Fatalf("partial read stopped early or lost classification: err=%v calls=%d values=%v", err, calls, values)
			}
			if mode == "read-service" && !errors.Is(err, ua.StatusBadNotReadable) {
				t.Fatalf("partial wrapper lost original error: %v", err)
			}
			if values[0]["has_value"] != false || values[2]["has_value"] != true || values[2]["value"] != int32(7) {
				t.Fatalf("missing/bad and later readable value presence changed: %v", values)
			}
			if mode != "bad-node" && (values[0]["status"] != "Unavailable" || values[0]["error"] == "" || values[1]["has_value"] != false) {
				t.Fatalf("synthetic unavailable results omitted explicit absence/diagnostics: %v", values)
			}
		})
	}
}

func TestOPCUAReadTerminalFailuresDoNotContinueAfterGoodResults(t *testing.T) {
	unknown := errors.New("unknown read failure")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"cancelled", context.Canceled},
		{"deadline", context.DeadlineExceeded},
		{"eof", fmt.Errorf("read transport: %w", io.EOF)},
		{"network", &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}},
		{"connection", ua.StatusBadConnectionClosed},
		{"session", ua.StatusBadSessionIDInvalid},
		{"channel", ua.StatusBadSecureChannelIDInvalid},
		{"identity", ua.StatusBadSecurityChecksFailed},
		{"unknown", unknown},
		{"joined", errors.Join(ua.StatusBadNotReadable, io.EOF)},
		{"joined-too-many", errors.Join(ua.StatusBadTooManyOperations, io.EOF)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			reader := functionUAReader(func(_ context.Context, r *ua.ReadRequest) (*ua.ReadResponse, error) {
				calls++
				if calls == 2 {
					return nil, tc.err
				}
				result := &ua.ReadResponse{}
				for range r.NodesToRead {
					result.Results = append(result.Results, &ua.DataValue{Value: ua.MustVariant(int32(1))})
				}
				return result, nil
			})
			var values []map[string]any
			r := config.Request{Action: "read", Params: map[string]any{"node_ids": []string{"i=1", "i=2", "i=3", "i=4", "i=5"}, "read_batch_size": 2}}
			err := opcuaReadAttributes(context.Background(), reader, r, func(event Event) { values = append(values, event.Data.(map[string]any)) })
			if !errors.Is(err, tc.err) || OPCUAReadCanContinue(err) || calls != 2 || len(values) != 4 {
				t.Fatalf("terminal failure continued/lost original error: err=%v calls=%d values=%v", err, calls, values)
			}
			for _, value := range values[2:] {
				if value["has_value"] != false || value["status"] != "Unavailable" || value["error"] == "" {
					t.Fatalf("transport failure did not emit explicit absent value: %v", value)
				}
			}
		})
	}
}

func TestOPCUAReadPartialClassificationRejectsMixedTerminalCauses(t *testing.T) {
	partial := &OPCUAReadPartialError{Err: ua.StatusBadNotReadable}
	for _, err := range []error{partial, fmt.Errorf("read selection: %w", partial), errors.Join(partial, partial)} {
		if !OPCUAReadCanContinue(err) {
			t.Errorf("explicit partial result lost classification: %v", err)
		}
	}
	for _, err := range []error{nil, io.EOF, errors.Join(partial, io.EOF), fmt.Errorf("wrapped: %w", errors.Join(partial, context.Canceled))} {
		if OPCUAReadCanContinue(err) {
			t.Errorf("terminal/unknown cause was treated as a partial result: %v", err)
		}
	}
}
