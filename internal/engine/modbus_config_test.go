package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
)

func TestMTUIFullConfigOfflineReadOnlyConversion(t *testing.T) {
	input := []byte(`{"version":1,"name":"工厂","device":{"interface":{"tcp":{"ip":"127.0.0.1","port":1502}},"unit_id":7,"word_order":"cdab","request_timeout_ms":3000},"startup":{"address":200,"type":"coil","panel":"matrix"},"batch":{"size":11,"anchor":"middle"},"refresh_interval_ms":500,"columns":{"visible":["address","time","u64","bits"],"time_mode":"ago","address_mode":"hex","label_width":12,"custom_width":25},"registers":{"holdings":[{"address":1,"label":"holding"}],"coils":[{"address":2,"pinned":true}],"inputs":[{"address":1,"label":"input"}]},"api":{"enabled":true,"port":8080},"read_only":false,"write_log":{"enabled":true,"directory":"/secret"},"next_config":"/secret/file","keybinds":{"write":"w"},"password":"secret-do-not-persist"}`)
	plan, err := ImportMTUIConfig(input, "device-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Requests) != 4 || plan.Requests[0].Action != "read-coils" {
		t.Fatal(plan)
	}
	for _, r := range plan.Requests {
		if r.Mutates() || r.Endpoint != "tcp://127.0.0.1:1502" || r.Int("address", -1) != 195 || r.Int("samples", 0) != 1 || r.Int("unit", 0) != 7 || r.Timeout != "3000ms" {
			t.Fatalf("unsafe/inaccurate request: %#v", r)
		}
		if err := validateParams(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"api", "read_only", "write_log", "next_config", "keybinds", "password"} {
		if !strings.Contains(strings.Join(plan.Warnings, "\n"), key) {
			t.Fatalf("missing warning %s", key)
		}
	}
	b, _ := json.Marshal(plan.Requests)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "enabled") || strings.Contains(string(b), "write_log") {
		t.Fatal("unsafe setting persisted")
	}
	if plan.Requests[1].Params["labels"].(map[string]any)["1"] != "holding" || plan.Requests[2].Params["labels"].(map[string]any)["1"] != "input" {
		t.Fatal("annotations crossed spaces")
	}
}
func TestMTUIFullConfigTransportAndBounds(t *testing.T) {
	for _, tc := range []struct{ raw, endpoint string }{
		{`{"interface":"mock"}`, "mock://local"},
		{`{"interface":{"rtu_over_tcp":{"ip":"::1","port":1502}}}`, "rtu+tcp://[::1]:1502"},
		{`{"interface":{"serial":{"path":"COM7","baud_rate":19200,"data_bits":7,"parity":"even","stop_bits":2}}}`, "rtu://COM7"},
	} {
		p, e := ImportMTUIConfig([]byte(`{"device":`+tc.raw+`}`), "x")
		if e != nil || p.Requests[0].Endpoint != tc.endpoint {
			t.Fatalf("%s: %v %#v", tc.raw, e, p)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"version":2}`, `{} {}`, `{"device":{"unit_id":0}}`, `{"device":null}`, `{"device":{"unit_id":null}}`, `{"columns":null}`, `{"registers":null}`, `{"device":{"interface":{"tcp":{"ip":"a@host","port":502}}}}`, `{"device":{"interface":{"tcp":{"ip":"${env:TOKEN}","port":502}}}}`, `{"device":{"interface":{"tcp":{"ip":"host","port":0}}}}`, `{"device":{"interface":{"serial":{"path":"COM1","baud_rate":0}}}}`, `{"batch":{"size":126}}`, `{"startup":{"address":65536}}`, `{"startup":{"type":"unknown"}}`, `{"columns":{"visible":["time","time"]}}`, `{"columns":{"time_mode":"never"}}`, `{"columns":{"label_width":121}}`, `{"registers":{"coils":[{"address":-1}]}}`, `{"device":{"username":"secret"}}`} {
		if _, err := ImportMTUIConfig([]byte(raw), "x"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, prefix := range []string{"", "a/b", "a${x}", strings.Repeat("x", 65)} {
		if _, err := ImportMTUIConfig([]byte(`{}`), prefix); err == nil {
			t.Fatal("unsafe prefix")
		}
	}
	auto, err := ImportMTUIConfig([]byte(`{"columns":{"label_width":0,"custom_width":0}}`), "auto")
	if err != nil || len(auto.Requests[0].Params["columns"].(map[string]any)["widths"].(map[string]any)) != 0 {
		t.Fatal("upstream automatic widths not preserved", err)
	}
	p, e := ImportMTUIConfig([]byte(`{"startup":{"address":65535},"batch":{"anchor":"start","size":10}}`), "x")
	if e != nil || p.Requests[0].Int("address", 0) != 65526 {
		t.Fatal("end range bound")
	}
}
func TestMTUIAppendPreservesSourceAndRefusesOverwrite(t *testing.T) {
	raw := []byte("# keep top\nversion: 1\nprofiles:\n  local: {host: localhost}\nrequests:\n  # keep request\n  - id: existing\n    protocol: http\n    action: GET\n    endpoint: http://localhost\n")
	plan, err := ImportMTUIConfig([]byte(`{}`), "demo")
	if err != nil {
		t.Fatal(err)
	}
	out, err := AppendMTUIRequests(raw, plan.Requests)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "# keep top") || !strings.Contains(string(out), "# keep request") || !strings.Contains(string(out), "profiles:") {
		t.Fatal("source comments/profiles lost")
	}
	c, e := config.Parse(out)
	if e != nil || len(c.Requests) != 5 {
		t.Fatal(e)
	}
	if _, err = AppendMTUIRequests(out, plan.Requests); err == nil {
		t.Fatal("duplicate IDs accepted")
	}
	plan.Requests[0].Action = "write-register"
	if _, err = AppendMTUIRequests(raw, plan.Requests); err == nil {
		t.Fatal("write plan accepted")
	}
}
func TestMTUIImportedMockRunsOnlyWhenExplicitlyCalled(t *testing.T) {
	plan, err := ImportMTUIConfig([]byte(`{"device":{"interface":"mock"},"startup":{"address":0,"type":"holding"}}`), "local")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	if err := Run(context.Background(), plan.Requests[0], false, func(e Event) {
		if e.Kind == "registers" {
			count++
		}
	}); err != nil || count != 1 {
		t.Fatalf("explicit read: %v %d", err, count)
	}
}
func TestMTUICSVCanonicalRawColumnsAndSpaces(t *testing.T) {
	for _, tc := range []struct{ col, value string }{{"u16", "38042"}, {"hex", "949A"}, {"i16", "-27494"}, {"bits", "1001 0100 1001 1010"}, {"binary", "1001010010011010"}} {
		csv := fmt.Sprintf("\ufefftype,address,time,%s,label\r\ninput,0xC8,12:13:14.150,%s,\"comma, label\"\r\n", tc.col, tc.value)
		out, e := ParseMTUICSV([]byte(csv), false)
		if e != nil {
			t.Fatal(e)
		}
		if got := out[ModbusCSVCell{"input", 200}]; got.Value != 38042 || got.Time != "12:13:14.150" {
			t.Fatal(got)
		}
	}
	out, err := ParseMTUICSV([]byte("type,address,u16,hex,time\ninput,C8,2,FFFF,3s ago\ninput,C8,3,FFFF,14:00\ncoil,C8,1,0001,now\n"), true)
	if err != nil || len(out) != 2 || out[ModbusCSVCell{"input", 200}].Value != 3 || out[ModbusCSVCell{"coil", 200}].Time != "" {
		t.Fatalf("precedence/duplicate/space: %v %#v", err, out)
	}
	rows := DiffMTUICSV(out, map[ModbusCSVCell]uint16{{"input", 200}: 4})
	if len(rows) != 2 || rows[0].After != nil || rows[1].After == nil || *rows[1].After != 4 {
		t.Fatal(rows)
	}
}
func TestMTUICSVRejectMalformedAndBounded(t *testing.T) {
	for _, input := range []string{"", "address,type,u16\n1,input,2", "type,address,value\ninput,1,2", "type,address,u16\ninput,65536,0", "type,address,u16\ncoil,1,2", "type,address,u16\ninput,1,65536", "type,address,u16\nunknown,1,0", "type,address,hex\ninput,1,F", "type,address,bits\ninput,1,0000", "type,address,u16,u16\ninput,1,2,3", "type,address,u16\ninput,1,\"2", "type,address,u16\ninput,1,2,3", strings.Repeat("a", 4<<20+1)} {
		if _, err := ParseMTUICSV([]byte(input), false); err == nil {
			t.Fatalf("accepted %q", input[:min(len(input), 100)])
		}
	}
}
func FuzzMTUICSVBounded(f *testing.F) {
	f.Add([]byte("type,address,u16\ninput,1,2\n"))
	f.Add([]byte("\ufefftype,address,hex\rcoil,0,0001\r"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65536 {
			return
		}
		out, err := ParseMTUICSV(data, false)
		if err == nil {
			for cell, value := range out {
				if cell.Address < 0 || cell.Address > 65535 || ((cell.Type == "coil" || cell.Type == "discrete") && value.Value > 1) {
					t.Fatal("invalid parsed cell")
				}
			}
		}
	})
}

func FuzzMTUIFullConfig(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"device":{"interface":{"tcp":{"ip":"127.0.0.1","port":502}}},"api":{"enabled":true}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65536 {
			return
		}
		plan, err := ImportMTUIConfig(data, "fuzz")
		if err != nil {
			return
		}
		if len(plan.Requests) != 4 {
			t.Fatal("incomplete successful plan")
		}
		for _, r := range plan.Requests {
			if r.Mutates() || r.Int("samples", 0) != 1 || r.Int("address", 0)+r.Int("count", 0) > 65536 {
				t.Fatal("unsafe generated plan")
			}
		}
	})
}
