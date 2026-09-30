package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Live-yum/iotools/internal/config"
)

// ModbusWriteScope is one startup-authorized device/unit/type/address range.
// It is never expanded by an API request. Nil means read-only.
type ModbusWriteScope struct {
	Unit, Address, Count int
	Type                 string
}
type modbusAPIRequest struct {
	Type    string `json:"type"`
	Address *int   `json:"address"`
	Count   *int   `json:"count,omitempty"`
	Values  []int  `json:"values,omitempty"`
	Unit    *int   `json:"unit_id,omitempty"`
}

func validateModbusAPIScope(base config.Request, scope *ModbusWriteScope) error {
	if base.Protocol != "modbus" {
		return fmt.Errorf("API requires a Modbus request")
	}
	if err := validateParams(base); err != nil {
		return err
	}
	if _, err := parseRegisterAnnotations(base); err != nil {
		return err
	}
	v, ok := base.Params["unit"]
	if !ok {
		return fmt.Errorf("API requires explicit unit")
	}
	unit, err := exactInt(v)
	if err != nil || unit < 1 || unit > 247 {
		return fmt.Errorf("API unit must be 1..247")
	}
	if scope != nil {
		if scope.Unit != int(unit) || scope.Address < 0 || scope.Address > 65535 || scope.Count < 1 || scope.Count > 1968 || scope.Address+scope.Count > 65536 || (scope.Type != "holding" && scope.Type != "coil") {
			return fmt.Errorf("invalid explicit API write scope")
		}
	}
	return nil
}
func newModbusAPIHandler(base config.Request, scope *ModbusWriteScope) (http.Handler, error) {
	if err := validateModbusAPIScope(base, scope); err != nil {
		return nil, err
	}
	// Snapshot configuration so callers cannot change the authorization while serving.
	raw, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &base); err != nil {
		return nil, err
	}
	if scope != nil {
		copyScope := *scope
		scope = &copyScope
	}
	gate := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeAPIJSON(w, http.StatusOK, map[string]any{"status": "configured", "device_present": false, "read_only": scope == nil, "simulated": base.Endpoint == "mock://local"})
	})
	handle := func(write bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if write && scope == nil {
				http.Error(w, "read-only API", http.StatusForbidden)
				return
			}
			contentType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mediaErr != nil || contentType != "application/json" {
				http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
				return
			}
			var req modbusAPIRequest
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&req); err != nil {
				http.Error(w, "invalid JSON request", 400)
				return
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				http.Error(w, "expected one JSON object", 400)
				return
			}
			if req.Address == nil || *req.Address < 0 || *req.Address > 65535 {
				http.Error(w, "explicit address 0..65535 required", 400)
				return
			}
			unit := base.Int("unit", 0)
			if req.Unit != nil && *req.Unit != unit {
				http.Error(w, "unit override outside configured scope", 403)
				return
			}
			action := map[string]string{"holding": "read-holding", "input": "read-input", "coil": "read-coils", "discrete": "read-discrete"}[req.Type]
			if action == "" {
				http.Error(w, "unknown register type", 400)
				return
			}
			p := map[string]any{}
			for _, k := range []string{"baud", "data_bits", "parity", "stop_bits", "word_order"} {
				if v, ok := base.Params[k]; ok {
					p[k] = v
				}
			}
			p["unit"] = unit
			p["address"] = *req.Address
			p["samples"] = 1
			if write {
				if req.Count != nil || len(req.Values) < 1 || len(req.Values) > 123 || *req.Address+len(req.Values) > 65536 {
					http.Error(w, "write requires 1..123 values and no count", 400)
					return
				}
				if req.Type != scope.Type || unit != scope.Unit || *req.Address < scope.Address || *req.Address+len(req.Values) > scope.Address+scope.Count {
					http.Error(w, "write outside authorized scope", 403)
					return
				}
				values := make([]any, len(req.Values))
				for i, v := range req.Values {
					if v < 0 || v > 65535 || (req.Type == "coil" && v > 1) {
						http.Error(w, "registers require 0..65535; coils require 0 or 1", 400)
						return
					}
					if req.Type == "coil" {
						values[i] = v == 1
					} else {
						values[i] = v
					}
				}
				action = "write-registers"
				if req.Type == "coil" {
					action = "write-coils"
				}
				p["values"] = values
			} else {
				if len(req.Values) != 0 || req.Count == nil || *req.Count < 1 || *req.Count > 125 || *req.Address+*req.Count > 65536 {
					http.Error(w, "read requires count 1..125 and no values", 400)
					return
				}
				p["count"] = *req.Count
			}
			request := base
			request.Action = action
			request.Params = p
			request.Timeout = "5s"
			values := []any{}
			select {
			case gate <- struct{}{}:
			default:
				http.Error(w, "device busy", http.StatusTooManyRequests)
				return
			}
			err := Run(r.Context(), request, write, func(event Event) {
				switch event.Kind {
				case "registers":
					for _, row := range event.Data.([]map[string]any) {
						values = append(values, row["u16"])
					}
				case "bits":
					for _, bit := range event.Data.(map[string]any)["values"].([]bool) {
						v := 0
						if bit {
							v = 1
						}
						values = append(values, v)
					}
				}
			})
			<-gate
			if err != nil {
				http.Error(w, "Modbus operation failed: "+err.Error(), http.StatusBadGateway)
				return
			}
			if write {
				w.WriteHeader(http.StatusNoContent)
			} else {
				writeAPIJSON(w, 200, map[string]any{"values": values, "simulated": base.Endpoint == "mock://local"})
			}
		}
	}
	mux.HandleFunc("POST /read", handle(false))
	mux.HandleFunc("POST /write", handle(true))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() || r.Header.Get("Origin") != "" {
			http.Error(w, "loopback non-browser requests only", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if base.Endpoint == "mock://local" {
			w.Header().Set("X-Iotools-Simulated", "true")
		}
		mux.ServeHTTP(w, r)
	}), nil
}
func writeAPIJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ServeModbusAPI starts only on an explicitly supplied numeric loopback address.
// No credentials are generated and no device endpoint can be changed over HTTP.
func ServeModbusAPI(ctx context.Context, listen string, base config.Request, scope *ModbusWriteScope) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("listen must be numeric loopback IP:port")
	}
	ip := net.ParseIP(host)
	n, e := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || e != nil || n < 0 || n > 65535 {
		return fmt.Errorf("API bind must be numeric loopback IP:port")
	}
	handler, err := newModbusAPIHandler(base, scope)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = server.Close()
		case <-done:
		}
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
