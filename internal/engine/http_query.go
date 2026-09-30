package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/itchyny/gojq"
	"github.com/theory/jsonpath"
	"io"
)

func decodeJSON(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one JSON value")
	}
	return v, nil
}

// FilterJSON evaluates jq in-process without a shell, external modules, or environment access.
// Results preserve large integer precision. Cancellation and an output bound apply.
func FilterJSON(ctx context.Context, query string, data []byte) ([]any, error) {
	v, err := decodeJSON(data)
	if err != nil {
		return nil, fmt.Errorf("query JSON: %w", err)
	}
	return queryValues(ctx, "jq", query, v)
}

func queryValues(ctx context.Context, language, query string, value any) ([]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(query) > 65536 {
		return nil, fmt.Errorf("query exceeds 64 KiB")
	}
	switch v := value.(type) {
	case []byte:
		parsed, e := decodeJSON(v)
		if e != nil {
			return nil, e
		}
		value = parsed
	case string:
		parsed, e := decodeJSON([]byte(v))
		if e != nil {
			return nil, e
		}
		value = parsed
	}
	if language == "jsonpath" {
		p, err := jsonpath.Parse(query)
		if err != nil {
			return nil, err
		}
		out := []any(p.Select(value))
		if len(out) > 10000 {
			return nil, fmt.Errorf("query exceeds 10000 results")
		}
		size := 0
		for _, item := range out {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			size += len(encoded)
			if size > maxBody {
				return nil, fmt.Errorf("query result exceeds limit")
			}
		}
		return out, ctx.Err()
	}
	q, err := gojq.Parse(query)
	if err != nil {
		return nil, err
	}
	code, err := gojq.Compile(q)
	if err != nil {
		return nil, err
	}
	it := code.RunWithContext(ctx, value)
	out := []any{}
	size := 0
	for {
		v, ok := it.Next()
		if !ok {
			break
		}
		if e, ok := v.(error); ok {
			return nil, e
		}
		b, e := json.Marshal(v)
		if e != nil {
			return nil, e
		}
		size += len(b)
		if len(out) >= 10000 || size > maxBody {
			return nil, fmt.Errorf("query result exceeds limit")
		}
		out = append(out, v)
	}
	return out, ctx.Err()
}
func queryMode(values []any, mode string) (any, error) {
	switch mode {
	case "", "auto":
		if len(values) == 1 {
			return values[0], nil
		}
		if len(values) > 1 {
			return values, nil
		}
	case "single":
		if len(values) == 1 {
			return values[0], nil
		}
	case "array":
		return values, nil
	default:
		return nil, fmt.Errorf("query mode must be auto, single or array")
	}
	return nil, fmt.Errorf("query returned %d results for mode %q", len(values), mode)
}
