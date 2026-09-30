package tui

import (
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Optional deterministic screen captures use the real tview drawing pipeline.
// They are simulator snapshots, not a claim of physical-terminal validation.
func TestVisualCaptures(t *testing.T) {
	directory := os.Getenv("IOTOOLS_SCREENSHOT_DIR")
	if directory == "" {
		t.Skip("optional visual capture")
	}
	if e := os.MkdirAll(directory, 0755); e != nil {
		t.Fatal(e)
	}
	u, s := newTestUI(t)
	s.SetSize(120, 40)
	capture := func(name string) {
		u.App.ForceDraw()
		cells, w, h := s.GetContents()
		type cell struct {
			Text   string
			W      int
			FG, BG int32
		}
		out := struct {
			Width, Height int
			Cells         []cell
		}{w, h, []cell{}}
		for index, c := range cells {
			_, _, _, width := s.GetContent(index%w, index/w)
			fg, bg, _ := c.Style.Decompose()
			f, b := fg.Hex(), bg.Hex()
			if f < 0 {
				f = 0xeeeeee
			}
			if b < 0 {
				b = 0
			}
			out.Cells = append(out.Cells, cell{string(c.Runes), width, f, b})
		}
		b, _ := json.Marshal(out)
		if e := os.WriteFile(filepath.Join(directory, name+".json"), b, 0644); e != nil {
			t.Fatal(e)
		}
	}
	capture("01-home")
	u.showHelp()
	capture("02-help")
	u.pages.RemovePage("help")
	for i, r := range u.collection.Requests {
		if r.Protocol == "modbus" && r.Action == "read-holding" {
			u.list.SetCurrentItem(i)
			break
		}
	}
	u.inspector.reset(config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Params: map[string]any{"matrix_columns": 4}})
	rows := []map[string]any{}
	for i := 0; i < 24; i++ {
		rows = append(rows, map[string]any{"address": i, "u16": uint16(100 + i), "i16": int16(100 + i), "hex": "0x0064", "f32": "0"})
	}
	u.inspector.add(engine.Event{Kind: "registers", Data: rows})
	u.inspector.matrix = true
	u.inspector.renderRegisters()
	capture("03-modbus-matrix")
	u.inspector.matrix = false
	for i, r := range u.collection.Requests {
		if r.Protocol == "opcua" {
			u.list.SetCurrentItem(i)
			break
		}
	}
	u.lastRequest = config.Request{Protocol: "opcua", Action: "attributes", Endpoint: "opc.tcp://127.0.0.1:4840"}
	u.inspector.reset(u.lastRequest)
	for _, a := range []string{"NodeId", "DisplayName", "Description", "Value", "DataType", "AccessLevel"} {
		u.inspector.add(engine.Event{Kind: "attribute", Data: map[string]any{"node_id": "ns=2;s=Temperature", "attribute": a, "value": "温度 21.5", "value_type": "String", "status": "Good"}})
	}
	capture("04-opcua-attributes")
	u.httpConsole()
	capture("05-http-console")
	u.pages.RemovePage("http-console")
	s.SetSize(80, 24)
	capture("06-small-terminal")
	u.editRequest()
	capture("07-edit-form-small")
	u.pages.RemovePage("request-form")
	u.edit()
	capture("08-yaml-editor-small")
	u.pages.RemovePage("editor")
	s.SetSize(120, 40)
	u.inspector.reset(config.Request{Protocol: "mqtt", Action: "subscribe", Endpoint: "mqtt://127.0.0.1:1883"})
	for i := 0; i < 24; i++ {
		value := 20 + i%8
		u.inspector.add(engine.Event{Kind: "message", Data: map[string]any{"topic": "实验室/温度", "payload": fmt.Sprint(value), "payload_json": value, "payload_format": "json", "qos": 1, "retained": false, "bytes": 2, "received_at": time.Date(2026, 9, 30, 12, 0, i, 0, time.UTC).Format(time.RFC3339)}})
	}
	u.inspector.mqttHistoryOpen("实验室/温度", false)
	capture("09-mqtt-history")
	u.pages.RemovePage("mqtt-history")
	u.inspector.mqttHistoryOpen("实验室/温度", true)
	capture("10-mqtt-graph")
	u.pages.RemovePage("mqtt-history")
	u.lastRequest = config.Request{Protocol: "opcua", Action: "browse", Endpoint: "opc.tcp://127.0.0.1:4840"}
	u.uaConnectionForm(u.lastRequest, nil)
	capture("11-opcua-connect")
	u.pages.RemovePage("ua-connect")
	u.inspector.reset(config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local"})
	u.inspector.modbusConfigImportForm()
	capture("12-modbus-config-import")
	u.pages.RemovePage("modbus-config-import")
	u.collection.Requests = []config.Request{{ID: "示例请求", Protocol: "http", Action: "GET", Endpoint: "http://127.0.0.1:8080"}}
	u.selected = 0
	s.SetSize(80, 24)
	u.httpOverrideForm()
	capture("13-http-override-small")
	u.pages.RemovePage("http-overrides")
	u.modbusOperation(engine.ModbusOperation{Time: time.Now(), Action: "read-holding", Unit: 1, Count: 8, Duration: 12 * time.Millisecond, Success: true})
	u.modbusStatsPanel()
	capture("14-modbus-stats-small")
	u.pages.RemovePage("modbus-stats")
	u.modbusRotationForm()
	capture("15-config-rotation-small")
	u.pages.RemovePage("modbus-rotation-file")
	risk := u.httpInsecurePanel(config.Request{Endpoint: "https://127.0.0.1:8443/测试"}, func(bool) {})
	u.pages.AddPage("tls-risk", risk, true, true)
	u.App.SetFocus(risk)
	capture("16-tls-risk-small")
	u.pages.RemovePage("tls-risk")
	visualModbus := config.Request{ID: "modbus-visual", Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Params: map[string]any{"unit": 1, "address": 10, "count": 2, "word_order": "ABCD"}}
	u.collection.Requests = []config.Request{visualModbus}
	u.selected = 0
	u.lastRequest = visualModbus
	u.inspector.reset(visualModbus)
	for i := 0; i < 20; i++ {
		u.inspector.add(engine.Event{Time: time.Unix(1700000000+int64(i), 0), Kind: "registers", Data: []map[string]any{{"address": 10, "u16": uint16(20 + i%7)}, {"address": 11, "u16": uint16(5)}}})
	}
	u.inspector.table.Select(1, 0)
	u.inspector.modbusReadForm(nil)
	capture("17-modbus-read-controls-small")
	u.pages.RemovePage("modbus-read-controls")
	u.readonly = false
	u.populate("")
	u.inspector.modbusWriteForm()
	capture("18-modbus-write-small")
	u.pages.RemovePage("modbus-write")
	u.inspector.modbusInspect(true)
	capture("19-modbus-field-graph-small")
}
