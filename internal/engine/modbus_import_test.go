package engine

import (
	"github.com/Live-yum/iotools/internal/config"
	"testing"
)

func TestMTUIRegistersNativeRoundtrip(t *testing.T) {
	payload := []byte(`{"version":1,"device":{"interface":"mock"},"registers":{"holdings":[{"address":10,"pinned":true,"label":"电压","custom":{"repr":"f32","word_order":"cdab","ops":["/10"],"next":[12],"enum":{"0":"停止"},"bits":{"1":"运行"}}}],"inputs":[{"address":10,"label":"不同空间"}]}}`)
	p, e := ImportMTUIRegisters(payload, "read-holding")
	if e != nil {
		t.Fatal(e)
	}
	r := config.Request{Action: "read-holding", Params: p}
	a, e := parseRegisterAnnotations(r)
	if e != nil || a.Labels[10] != "电压" || a.Rules[0].WordOrder != "CDAB" {
		t.Fatal(a, e)
	}
	out, e := ExportMTUIRegisters(r)
	if e != nil {
		t.Fatal(e)
	}
	back, e := ImportMTUIRegisters(out, "read-holding")
	if e != nil {
		t.Fatal(string(out), e)
	}
	b, e := parseRegisterAnnotations(config.Request{Params: back})
	if e != nil || b.Rules[0].Next[0] != 12 || b.Rules[0].Enum["0"] != "停止" {
		t.Fatal(b, e)
	}
	p, e = ImportMTUIRegisters(payload, "read-input")
	if e != nil {
		t.Fatal(e)
	}
	b, e = parseRegisterAnnotations(config.Request{Params: p})
	if e != nil || b.Labels[10] != "不同空间" || len(b.Pins) != 0 {
		t.Fatal(b, e)
	}
	for _, bad := range []string{`{"holdings":[{"address":1,"pinned":"false"}]}`, `{"holdings":[{"address":1.5}]}`, `{"holdings":[{"address":1},{"address":1}]}`, `{"holdings":[{"address":65535,"custom":{"repr":"f64"}}]}`, `{"holdings":[{"address":1,"custom":{"repr":"u16","typo":1}}]}`, `{"version":2,"registers":{}}`} {
		if _, e := ImportMTUIRegisters([]byte(bad), "read-holding"); e == nil {
			t.Fatal("bad native import accepted", bad)
		}
	}
}
