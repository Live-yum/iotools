package engine

import (
	"context"
	"encoding/json"
	"github.com/Live-yum/iotools/internal/config"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testWorkflow() *httpWorkflow {
	return &httpWorkflow{ctx: context.Background(), collection: &config.Collection{Version: 1}, vars: map[string]string{}, varsCache: map[string]any{}, varsActive: map[string]bool{}, rootDir: "."}
}
func TestSlumberTemplateFunctions(t *testing.T) {
	cases := []struct {
		source   string
		expected any
	}{
		{`{{ 'hello' | upper() }}`, "HELLO"}, {`{{ ' A ' | trim() | lower() }}`, "a"},
		{`{{ [1, true, 'x'] | join(':') }}`, "1:true:x"}, {`{{ concat(['a', 'b']) }}`, "ab"},
		{`{{ {'x': [1, 2]} | jq('.x | add') }}`, 3},
		{`{{ {'x': [1, 2]} | jsonpath('$.x[1]') }}`, json.Number("2")},
		{`{{ '{"x":false}' | json_parse() | jq('.x') }}`, false},
		{`{{ 'abc' | base64() }}`, "YWJj"}, {`{{ 'YWJj' | base64(decode=true) }}`, []byte("abc")},
		{`{{ '你好世界' | index(-1) }}`, "界"}, {`{{ [1,2,3] | slice(1, -1) }}`, []any{json.Number("2")}},
		{`{{ 'abc' | slice(1, null) }}`, "bc"}, {`{{ 'a,b,c' | split(',', n=1) }}`, []any{"a", "b,c"}},
		{`{{ 'banana' | replace('na', 'ma', n=1) }}`, "bamana"}, {`{{ 'banana' | replace('[ab]', 'x', regex=true) }}`, "xxnxnx"},
		{`{{ integer('12') }}`, json.Number("12")}, {`{{ float('1.5') }}`, 1.5}, {`{{ boolean([]) }}`, false},
		{`{{ sensitive('demo') }}`, "demo"}, {`{{ b'\xff\x00' }}`, []byte{255, 0}}, {`{{ string({'x':1}) }}`, `{"x":1}`},
		{`{{ [1,2] | index(3) }}`, nil}, {`{{ jsonpath('$[*]', [1,2], mode='array') }}`, []any{json.Number("1"), json.Number("2")}},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			w := testWorkflow()
			got, e := w.render(tc.source, true)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("got %#v (%T), want %#v (%T)", got, got, tc.expected, tc.expected)
			}
		})
	}
}
func TestSlumberTemplateParsingSafety(t *testing.T) {
	for _, s := range []string{`{{ env( }}`, `{{ 'x }}`, `{{ f(a=1,a=2) }}`, `{{ f(a=1,2) }}`, `{{ [] | foo }}`, `{{ {'x':1 }`, `{{ base64() }}`, `{{ env('X', nonsense=true) }}`, `{{ missing }}`, `{{ command(['echo','x']) }}`} {
		t.Run(s, func(t *testing.T) {
			w := testWorkflow()
			if _, e := w.render(s, true); e == nil {
				t.Fatalf("accepted invalid or disabled template %s", s)
			}
		})
	}
	w := testWorkflow()
	w.vars["a"] = `{{ b }}`
	w.vars["b"] = `{{ a }}`
	if _, e := w.render(`{{ a }}`, true); e == nil {
		t.Fatal("cyclic profile accepted")
	}
	w = testWorkflow()
	w.vars["host"] = "127.0.0.1"
	got, e := w.render(`http://{{ host }}/{{ {'x': '}}'} | jq('.x') }}`, false)
	if e != nil || got != "http://127.0.0.1/}}" {
		t.Fatalf("quote/delimiter parsing: %v %v", got, e)
	}
	for s, want := range map[string]string{`{_{literal}}`: `{{literal}}`, `{__{literal}}`: `{_{literal}}`, `plain`: "plain", `{_{{host}}`: "{127.0.0.1", `{__{{host}}`: "{_127.0.0.1"} {
		got, e := w.render(s, false)
		if e != nil || got != want {
			t.Fatalf("escape %q: %v %v", s, got, e)
		}
	}
}
func TestSlumberFilesAndPrompt(t *testing.T) {
	root := t.TempDir()
	data := []byte{0, 255, 42}
	if e := os.WriteFile(filepath.Join(root, "sample.bin"), data, 0600); e != nil {
		t.Fatal(e)
	}
	w := testWorkflow()
	w.rootDir = root
	got, e := w.render(`{{ file('sample.bin') }}`, true)
	if e != nil || !reflect.DeepEqual(got, data) {
		t.Fatalf("binary file: %v %v", got, e)
	}
	if _, e = w.render(`file: {{ file('sample.bin') }}`, false); e == nil {
		t.Fatal("invalid UTF-8 string accepted")
	}
	if _, e = w.render(`{{ file('.') }}`, true); e == nil {
		t.Fatal("directory accepted")
	}
	t.Setenv("IOTOOLS_TEMPLATE_TEST", "local")
	got, e = w.render(`{{ env('IOTOOLS_TEMPLATE_TEST') }}`, true)
	if e != nil || got != "local" {
		t.Fatal(got, e)
	}
	got, e = w.render(`{{ env('IOTOOLS_ABSENT_5D313', default='fallback') }}`, true)
	if e != nil || got != "fallback" {
		t.Fatal(got, e)
	}
	w.options.Prompt = func(_ context.Context, message, def string, sensitive bool) (string, error) {
		if message != "Code" || def != "demo" || !sensitive {
			t.Fatal(message, def, sensitive)
		}
		return "selected", nil
	}
	got, e = w.render(`{{ prompt(message='Code', default='demo', sensitive=true) }}`, true)
	if e != nil || got != "selected" {
		t.Fatal(got, e)
	}
	w.options.Select = func(_ context.Context, _ string, options []any) (any, error) { return options[1], nil }
	got, e = w.render(`{{ ['a', 'b'] | select() }}`, true)
	if e != nil || got != "b" {
		t.Fatal(got, e)
	}
}
func TestJQPrecisionLimitsAndCancellation(t *testing.T) {
	values, e := FilterJSON(context.Background(), `.id + 1`, []byte(`{"id":9007199254740993}`))
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(values)
	if string(b) != `[9007199254740994]` {
		t.Fatal(string(b))
	}
	if _, e = FilterJSON(context.Background(), `empty`, []byte(`{}`)); e != nil {
		t.Fatal(e)
	}
	if _, e = FilterJSON(context.Background(), `.`, []byte(`{} {}`)); e == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, e = FilterJSON(context.Background(), `range(0;10001)`, []byte(`{}`)); e == nil {
		t.Fatal("unbounded result")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, e = FilterJSON(ctx, `def forever: forever; forever`, []byte(`{}`))
	if e == nil || !strings.Contains(e.Error(), "deadline") {
		t.Fatalf("query not canceled: %v", e)
	}
}
