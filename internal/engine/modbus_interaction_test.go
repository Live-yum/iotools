package engine

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
)

func TestModbusInteractionExactPreviewRoundtrip(t *testing.T) {
	vectors := []struct{ kind, value string }{{"u16", "65535"}, {"i16", "-32768"}, {"f16", "1.5"}, {"u32", "4294967295"}, {"i32", "-2147483648"}, {"f32", "12.5"}, {"u64", "18446744073709551615"}, {"i64", "-9223372036854775808"}, {"f64", "-3.25"}}
	for _, order := range []string{"ABCD", "BADC", "CDAB", "DCBA"} {
		for _, v := range vectors {
			words, err := EncodeModbusValue(v.kind, v.value, order, 65000)
			if err != nil {
				t.Fatal(err)
			}
			r := config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Params: map[string]any{"unit": 241, "address": 65000, "count": len(words), "word_order": order}}
			decoder, err := NewModbusInterpreter(r)
			if err != nil {
				t.Fatal(err)
			}
			raw := map[int]uint16{}
			values := []any{}
			for i, w := range words {
				raw[65000+i] = w
				values = append(values, int(w))
			}
			rows, err := decoder.Interpret(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmtValue(rows[0][v.kind]); got != v.value {
				t.Fatalf("%s %s got %s", v.kind, order, got)
			}
			write := r
			write.Action = "write-registers"
			write.Params = map[string]any{"unit": 241, "address": 65000, "count": len(words), "values": values}
			if err = Run(context.Background(), write, false, nil); err == nil {
				t.Fatal("ungated write")
			}
			if err = Run(context.Background(), write, true, nil); err != nil {
				t.Fatal(err)
			}
			seen := false
			if err = Run(context.Background(), r, false, func(e Event) {
				if e.Kind == "registers" {
					seen = reflect.DeepEqual(e.Data.([]map[string]any)[0][v.kind], rows[0][v.kind])
				}
			}); err != nil || !seen {
				t.Fatalf("mock roundtrip %s: %v", v.kind, err)
			}
		}
	}
	for _, v := range []struct {
		k, s, o string
		a       int
	}{{"u64", "18446744073709551616", "ABCD", 0}, {"i16", "32768", "ABCD", 0}, {"u16", "-1", "ABCD", 0}, {"u32", "1", "ABCD", 65535}, {"f16", "70000", "ABCD", 0}, {"f32", "1e100", "ABCD", 0}, {"f64", "NaN", "ABCD", 0}, {"f64", "Inf", "ABCD", 0}, {"u16", "1", "bad", 0}} {
		if _, err := EncodeModbusValue(v.k, v.s, v.o, v.a); err == nil {
			t.Fatalf("accepted overflow %+v", v)
		}
	}
}
func fmtValue(v any) string {
	switch n := v.(type) {
	case uint16:
		return fmt.Sprint(n)
	case int16:
		return fmt.Sprint(n)
	case uint32:
		return fmt.Sprint(n)
	case int32:
		return fmt.Sprint(n)
	default:
		return fmt.Sprint(v)
	}
}
func TestModbusInteractionSparseRulesAndNoStaleWords(t *testing.T) {
	r := config.Request{Params: map[string]any{"rules": []any{map[string]any{"address": 10, "repr": "u32", "next": []any{20}, "ops": []any{"*2"}}}}}
	m, err := NewModbusInterpreter(r)
	if err != nil {
		t.Fatal(err)
	}
	row, err := m.Field(map[int]uint16{10: 1, 20: 2}, 10)
	if err != nil || row["custom_numeric"] != float64(131076) {
		t.Fatalf("sparse rule %v %v", row, err)
	}
	if _, ok := row["u32"]; ok {
		t.Fatal("gap silently filled")
	}
	incomplete, _ := m.Field(map[int]uint16{10: 9}, 10)
	if _, ok := incomplete["custom_numeric"]; ok {
		t.Fatal("old word reused")
	}
	if _, err = m.Interpret(map[int]uint16{-1: 1}); err == nil {
		t.Fatal("bad address")
	}
	if _, err = NewModbusInterpreter(config.Request{Params: map[string]any{"word_order": "INVALID"}}); err == nil {
		t.Fatal("bad order")
	}
	many := map[int]uint16{}
	for i := 0; i < 2001; i++ {
		many[i] = 1
	}
	if _, err = m.Interpret(many); err == nil {
		t.Fatal("unbounded batch")
	}
}
