package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gopcua/opcua/ua"
)

const (
	uaScanRecordLimit = 100000
	uaScanFileLimit   = 128 << 20
)

type uaScanRow struct {
	NodeID          string    `json:"node_id"`
	ParentID        string    `json:"parent_id"`
	DisplayName     string    `json:"display_name"`
	BrowseName      string    `json:"browse_name"`
	NodeClass       string    `json:"node_class"`
	Depth           int       `json:"depth"`
	HasValue        bool      `json:"has_value"`
	Value           any       `json:"value"`
	ValueType       any       `json:"value_type"`
	ValueTypeName   string    `json:"value_type_name"`
	ValueIsArray    bool      `json:"value_is_array"`
	Status          string    `json:"status"`
	SourceTimestamp any       `json:"source_timestamp"`
	ServerTimestamp any       `json:"server_timestamp"`
	ObservedAt      time.Time `json:"observed_at"`
	Kind            string    `json:"kind"`
	Error           string    `json:"error,omitempty"`
}

type uaScanSnapshot struct {
	Endpoint             string      `json:"endpoint"`
	RootNode             string      `json:"root_node"`
	ExportedAt           time.Time   `json:"exported_at"`
	ScanComplete         bool        `json:"scan_complete"`
	ScanReason           string      `json:"scan_reason"`
	NamespaceFingerprint string      `json:"namespace_fingerprint"`
	Rows                 []uaScanRow `json:"rows"`
}

// Files contain only node observations. In particular, request parameters and
// URL credentials are never persisted alongside them.
func uaScanFileEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "[invalid endpoint]"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

func uaScanCreateFile(path string) (*os.File, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("OPC UA file path must be explicitly provided")
	}
	// O_EXCL also rejects an existing symlink, including a dangling one.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("create OPC UA file (existing files and symlinks are not overwritten): %w", err)
	}
	return f, nil
}

type uaScanFileSink interface {
	io.Writer
	Sync() error
	Close() error
}

type uaScanCountingWriter struct {
	ctx   context.Context
	sink  uaScanFileSink
	bytes int64
	limit int64
}

func (w *uaScanCountingWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > w.limit-w.bytes {
		return 0, fmt.Errorf("OPC UA file byte limit (%d bytes) exceeded", w.limit)
	}
	n, err := w.sink.Write(p)
	w.bytes += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

type uaScanFileWriter struct {
	counter *uaScanCountingWriter
	buffer  *bufio.Writer
	path    string
	err     error
	closed  bool
}

func newUAScanFileWriter(ctx context.Context, sink uaScanFileSink, path string, limit int64) *uaScanFileWriter {
	w := &uaScanFileWriter{counter: &uaScanCountingWriter{ctx: ctx, sink: sink, limit: limit}, path: path}
	w.buffer = bufio.NewWriter(w.counter)
	return w
}

func (w *uaScanFileWriter) fail(err error) error {
	if err != nil && w.err == nil {
		w.err = fmt.Errorf("OPC UA file %q may be incomplete: %w", w.path, err)
	}
	return w.err
}

// Each call is flushed before returning, so a disk/write error is reported by
// the observation that encountered it rather than by an unrelated later row.
func (w *uaScanFileWriter) write(p []byte) error {
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return errors.New("OPC UA file is closed")
	}
	if err := w.counter.ctx.Err(); err != nil {
		return w.fail(err)
	}
	if int64(len(p)) > w.counter.limit-w.counter.bytes {
		return w.fail(fmt.Errorf("OPC UA file byte limit (%d bytes) exceeded", w.counter.limit))
	}
	if _, err := w.buffer.Write(p); err != nil {
		return w.fail(err)
	}
	return w.fail(w.buffer.Flush())
}

func (w *uaScanFileWriter) close() error {
	if w.closed {
		return w.err
	}
	w.closed = true
	// Attempt every operation even after a prior failure. In particular, a
	// failed flush must not leak the file descriptor or hide a close error.
	err := errors.Join(w.buffer.Flush(), w.counter.sink.Sync(), w.counter.sink.Close(), w.counter.ctx.Err())
	if err != nil {
		if w.err != nil {
			w.err = errors.Join(w.err, fmt.Errorf("OPC UA file %q may be incomplete: %w", w.path, err))
		} else {
			w.fail(err)
		}
	}
	return w.err
}

// uaScanJSONValue avoids a marshal/unmarshal round trip, which would turn
// integers into float64 and lose precision. Non-finite IEEE values use an
// explicit tagged object, including inside typed arrays and nested maps.
type uaScanJSONValue struct{ value any }

func (v uaScanJSONValue) MarshalJSON() ([]byte, error) {
	safe, err := uaScanJSONSafeValue(reflect.ValueOf(v.value), 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(safe)
}

func uaScanJSONSafeValue(v reflect.Value, depth int) (any, error) {
	if depth > 100 {
		return nil, errors.New("OPC UA value nesting exceeds 100 levels (possibly cyclic)")
	}
	if !v.IsValid() {
		return nil, nil
	}
	if v.Kind() == reflect.Interface {
		return uaScanJSONSafeValue(v.Elem(), depth+1)
	}
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return nil, nil
	}
	if v.CanInterface() {
		switch value := v.Interface().(type) {
		case *ua.Variant:
			return uaScanJSONVariant(value, depth)
		case ua.Variant:
			return uaScanJSONVariant(&value, depth)
		case *ua.ExtensionObject:
			if err := uaScanValidateExtension(*value); err != nil {
				return nil, err
			}
		case ua.ExtensionObject:
			if err := uaScanValidateExtension(value); err != nil {
				return nil, err
			}
		case ua.NodeID:
			return &value, nil // NodeID defines MarshalJSON on its pointer.
		}
		// Keep DateTime, NodeID, ByteString and other explicitly defined JSON
		// encodings intact. Errors from a custom marshaler remain visible.
		if _, ok := v.Interface().(json.Marshaler); ok {
			return v.Interface(), nil
		}
	}
	switch v.Kind() {
	case reflect.Pointer:
		return uaScanJSONSafeValue(v.Elem(), depth+1)
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) {
			return map[string]string{"$float": "NaN"}, nil
		}
		if math.IsInf(f, 1) {
			return map[string]string{"$float": "+Inf"}, nil
		}
		if math.IsInf(f, -1) {
			return map[string]string{"$float": "-Inf"}, nil
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice {
			if v.IsNil() {
				return nil, nil
			}
			if v.Type().Elem().Kind() == reflect.Uint8 {
				return v.Interface(), nil // Preserve JSON's base64 ByteString.
			}
		}
		a := make([]any, v.Len())
		for i := range a {
			var err error
			a[i], err = uaScanJSONSafeValue(v.Index(i), depth+1)
			if err != nil {
				return nil, err
			}
		}
		return a, nil
	case reflect.Struct:
		return uaScanJSONStruct(v, depth)
	case reflect.Map:
		if v.IsNil() {
			return nil, nil
		}
		m := make(map[string]any, v.Len())
		it := v.MapRange()
		for it.Next() {
			key, err := uaScanJSONMapKey(it.Key())
			if err != nil {
				return nil, err
			}
			if _, exists := m[key]; exists {
				return nil, fmt.Errorf("OPC UA JSON map contains duplicate encoded key %q", key)
			}
			m[key], err = uaScanJSONSafeValue(it.Value(), depth+1)
			if err != nil {
				return nil, err
			}
		}
		return m, nil
	}
	return v.Interface(), nil
}

func uaScanValidateExtension(value ua.ExtensionObject) error {
	payload := reflect.ValueOf(value.Value)
	missing := !payload.IsValid()
	if !missing {
		switch payload.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			missing = payload.IsNil()
		}
	}
	if value.EncodingMask != ua.ExtensionObjectEmpty && missing {
		return errors.New("unsupported undecoded OPC UA ExtensionObject: nonempty encoding has no decoded payload")
	}
	return nil
}

// Variant's payload is private in gopcua; ordinary encoding/json silently emits
// {}. Record its public value and wire type/array metadata instead.
func uaScanJSONVariant(variant *ua.Variant, depth int) (any, error) {
	value, err := uaScanJSONSafeValue(reflect.ValueOf(variant.Value()), depth+1)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"$type": "Variant", "value": value, "value_type": variant.Type(),
		"value_is_array": variant.Has(ua.VariantArrayValues), "encoding_mask": variant.EncodingMask(),
		"array_length": variant.ArrayLength(), "array_dimensions": variant.ArrayDimensions(),
	}, nil
}

// Traverse exported structure fields so DataValue, ExtensionObject and nested
// generated UA structures cannot conceal Variants or non-finite floats. Rather
// than guess at opaque/embedded structs' JSON semantics, reject them explicitly.
func uaScanJSONStruct(v reflect.Value, depth int) (any, error) {
	out := make(map[string]any)
	typ := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := typ.Field(i)
		tag := strings.Split(field.Tag.Get("json"), ",")
		if tag[0] == "-" {
			continue
		}
		if field.PkgPath != "" || field.Anonymous {
			return nil, fmt.Errorf("unsupported opaque or embedded OPC UA value field %s.%s", typ, field.Name)
		}
		name := tag[0]
		if name == "" {
			name = field.Name
		}
		fv := v.Field(i)
		omit, quoted := false, false
		for _, option := range tag[1:] {
			switch option {
			case "omitempty":
				omit = omit || uaScanJSONEmpty(fv)
			case "omitzero":
				omit = omit || fv.IsZero()
			case "string":
				quoted = true
			case "":
			default:
				return nil, fmt.Errorf("unsupported OPC UA JSON field option %q", option)
			}
		}
		if omit {
			continue
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("OPC UA structure contains duplicate JSON field %q", name)
		}
		value, err := uaScanJSONSafeValue(fv, depth+1)
		if err != nil {
			return nil, err
		}
		if quoted {
			switch fv.Kind() {
			case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
				reflect.String, reflect.Float32, reflect.Float64:
				encoded, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				value = string(encoded)
			}
		}
		out[name] = value
	}
	return out, nil
}

func uaScanJSONEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

func uaScanJSONMapKey(v reflect.Value) (string, error) {
	if v.Kind() == reflect.String {
		return v.String(), nil
	}
	if v.CanInterface() {
		if tm, ok := v.Interface().(encoding.TextMarshaler); ok {
			b, err := tm.MarshalText()
			return string(b), err
		}
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(v.Uint(), 10), nil
	default:
		return "", fmt.Errorf("unsupported OPC UA JSON map key type %s", v.Type())
	}
}

func uaScanEncodedRow(row uaScanRow) uaScanRow {
	row.Value = uaScanJSONValue{row.Value}
	row.ValueType = uaScanJSONValue{row.ValueType}
	row.SourceTimestamp = uaScanJSONValue{row.SourceTimestamp}
	row.ServerTimestamp = uaScanJSONValue{row.ServerTimestamp}
	return row
}

func uaScanCSVSafe(s string) string {
	trimmed := strings.TrimLeftFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
	if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + s
	}
	return s
}

var uaScanCSVColumns = []string{
	"endpoint", "root_node", "exported_at", "scan_complete", "scan_reason",
	"node_id", "parent_id", "display_name", "browse_name", "node_class", "depth", "has_value",
	"value", "value_type", "value_type_name", "value_is_array", "status",
	"source_timestamp", "server_timestamp", "observed_at", "kind", "error", "namespace_fingerprint",
}

func uaScanCSVValue(value any) (string, error) {
	if value == nil {
		return "null", nil
	}
	if v := reflect.ValueOf(value); v.Kind() == reflect.String {
		return uaScanCSVSafe(v.String()), nil
	}
	if t, ok := value.(time.Time); ok {
		return t.Format(time.RFC3339Nano), nil
	}
	b, err := (uaScanJSONValue{value}).MarshalJSON()
	return string(b), err
}

func uaScanCSVRecord(snapshot uaScanSnapshot, row uaScanRow) ([]byte, error) {
	value, err := uaScanCSVValue(row.Value)
	if err != nil {
		return nil, err
	}
	typ, err := uaScanCSVValue(row.ValueType)
	if err != nil {
		return nil, err
	}
	source, err := uaScanCSVValue(row.SourceTimestamp)
	if err != nil {
		return nil, err
	}
	server, err := uaScanCSVValue(row.ServerTimestamp)
	if err != nil {
		return nil, err
	}
	fields := []string{
		uaScanCSVSafe(snapshot.Endpoint), uaScanCSVSafe(snapshot.RootNode), snapshot.ExportedAt.Format(time.RFC3339Nano),
		strconv.FormatBool(snapshot.ScanComplete), uaScanCSVSafe(snapshot.ScanReason),
		uaScanCSVSafe(row.NodeID), uaScanCSVSafe(row.ParentID), uaScanCSVSafe(row.DisplayName), uaScanCSVSafe(row.BrowseName), uaScanCSVSafe(row.NodeClass),
		strconv.Itoa(row.Depth), strconv.FormatBool(row.HasValue), value, typ,
		uaScanCSVSafe(row.ValueTypeName), strconv.FormatBool(row.ValueIsArray), uaScanCSVSafe(row.Status),
		source, server, row.ObservedAt.Format(time.RFC3339Nano), uaScanCSVSafe(row.Kind), uaScanCSVSafe(row.Error), uaScanCSVSafe(snapshot.NamespaceFingerprint),
	}
	return uaScanCSVLine(fields)
}

func uaScanCSVLine(fields []string) ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	if err := w.Write(fields); err != nil {
		return nil, err
	}
	w.Flush()
	return b.Bytes(), w.Error()
}

func writeUAScanSnapshot(ctx context.Context, path, format string, snapshot uaScanSnapshot) error {
	if ctx == nil {
		return errors.New("OPC UA snapshot context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if format != "json" && format != "csv" {
		return fmt.Errorf("unsupported OPC UA snapshot format %q; use csv or json", format)
	}
	if len(snapshot.Rows) > uaScanRecordLimit {
		return fmt.Errorf("OPC UA snapshot record limit (%d rows) exceeded", uaScanRecordLimit)
	}
	f, err := uaScanCreateFile(path)
	if err != nil {
		return err
	}
	return writeUAScanSnapshotTo(ctx, path, format, snapshot, f, uaScanFileLimit)
}

// The sink variant permits deterministic write/flush/sync/cancellation tests.
// It owns and closes sink, just as the public-path helper owns its new file.
func writeUAScanSnapshotTo(ctx context.Context, path, format string, snapshot uaScanSnapshot, sink uaScanFileSink, limit int64) (err error) {
	w := newUAScanFileWriter(ctx, sink, path, limit)
	defer func() { err = errors.Join(err, w.close()) }()
	snapshot.Endpoint = uaScanFileEndpoint(snapshot.Endpoint)
	if format == "json" {
		meta := struct {
			Endpoint             string    `json:"endpoint"`
			RootNode             string    `json:"root_node"`
			ExportedAt           time.Time `json:"exported_at"`
			ScanComplete         bool      `json:"scan_complete"`
			ScanReason           string    `json:"scan_reason"`
			NamespaceFingerprint string    `json:"namespace_fingerprint"`
		}{snapshot.Endpoint, snapshot.RootNode, snapshot.ExportedAt, snapshot.ScanComplete, snapshot.ScanReason, snapshot.NamespaceFingerprint}
		prefix, e := json.Marshal(meta)
		if e != nil {
			return w.fail(e)
		}
		prefix = append(prefix[:len(prefix)-1], []byte(",\"rows\":[\n")...)
		if e := w.write(prefix); e != nil {
			return e
		}
	} else {
		header, e := uaScanCSVLine(append([]string(nil), uaScanCSVColumns...))
		if e != nil {
			return w.fail(e)
		}
		if e := w.write(header); e != nil {
			return e
		}
	}
	for i, row := range snapshot.Rows {
		if e := ctx.Err(); e != nil {
			return w.fail(e)
		}
		var data []byte
		var e error
		if format == "json" {
			data, e = json.Marshal(uaScanEncodedRow(row))
			if i > 0 {
				data = append([]byte(",\n"), data...)
			}
		} else {
			data, e = uaScanCSVRecord(snapshot, row)
		}
		if e != nil {
			return w.fail(fmt.Errorf("encode row %d: %w", i+1, e))
		}
		if e = w.write(data); e != nil {
			return e
		}
	}
	if format == "json" {
		return w.write([]byte("\n]}\n"))
	}
	return w.fail(ctx.Err())
}

type uaScanRecorder struct {
	mu        sync.Mutex
	writer    *uaScanFileWriter
	format    string
	endpoint  string
	count     int
	maxRows   int
	startedAt time.Time
}

func newUAScanRecorder(path, format, endpoint string) (*uaScanRecorder, error) {
	if format != "jsonl" && format != "csv" {
		return nil, fmt.Errorf("unsupported OPC UA record format %q; use csv or jsonl", format)
	}
	f, err := uaScanCreateFile(path)
	if err != nil {
		return nil, err
	}
	return newUAScanRecorderTo(path, format, endpoint, f, uaScanRecordLimit, uaScanFileLimit)
}

func newUAScanRecorderTo(path, format, endpoint string, sink uaScanFileSink, maxRows int, maxBytes int64) (*uaScanRecorder, error) {
	r := &uaScanRecorder{
		writer: newUAScanFileWriter(context.Background(), sink, path, maxBytes),
		format: format, endpoint: uaScanFileEndpoint(endpoint), maxRows: maxRows, startedAt: time.Now().UTC(),
	}
	if format == "csv" {
		header, err := uaScanCSVLine(append([]string(nil), uaScanCSVColumns...))
		if err == nil {
			err = r.writer.write(header)
		}
		if err != nil {
			r.writer.fail(err)
			return nil, r.writer.close()
		}
	}
	return r, nil
}

func (r *uaScanRecorder) Write(row uaScanRow) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writer.err != nil {
		return r.writer.err
	}
	if r.writer.closed {
		return errors.New("OPC UA recorder is closed")
	}
	if r.count >= r.maxRows {
		return r.writer.fail(fmt.Errorf("OPC UA recording record limit (%d rows) reached", r.maxRows))
	}
	var data []byte
	var err error
	if r.format == "jsonl" {
		record := struct {
			Endpoint string `json:"endpoint"`
			uaScanRow
		}{r.endpoint, uaScanEncodedRow(row)}
		data, err = json.Marshal(record)
		data = append(data, '\n')
	} else {
		data, err = uaScanCSVRecord(uaScanSnapshot{Endpoint: r.endpoint, ExportedAt: r.startedAt, ScanReason: "recording"}, row)
	}
	if err != nil {
		return r.writer.fail(fmt.Errorf("encode record %d: %w", r.count+1, err))
	}
	if err = r.writer.write(data); err == nil {
		r.count++
	}
	return err
}

func (r *uaScanRecorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writer.close()
}

func (r *uaScanRecorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

func (r *uaScanRecorder) Bytes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writer.counter.bytes
}
