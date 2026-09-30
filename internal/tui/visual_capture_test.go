package tui

import (
	"encoding/json"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"os"
	"path/filepath"
	"testing"
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
}
