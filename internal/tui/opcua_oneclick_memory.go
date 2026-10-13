package tui

import (
	"encoding"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gopcua/opcua/ua"
)

// This is a conservative allocation reservation, not a measurement of RSS.
// It covers owned observations plus conversion/escaping/CSV copies. The engine
// has already decoded its current response before emit; that transport-side
// allocation is outside this snapshot ownership boundary.
const uaOneClickMemoryLimit int64 = 256 << 20

var errUAOneClickMemoryLimit = errors.New("OPC UA snapshot memory budget (256 MiB reservation) exceeded; stopped this service and preserved prior observations")

type uaOneClickMemory struct{ remaining int64 }

// Charge monotonically, including superseded observations and temporary copies.
// This avoids subtracting a value while another representation still owns it.
func (b *uaOneClickMemory) charge(n int64) error {
	if n < 0 || n > b.remaining {
		return errUAOneClickMemoryLimit
	}
	b.remaining -= n
	return nil
}

func (b *uaOneClickMemory) bytes(n int) error {
	if n < 0 || int64(n) > b.remaining/32 {
		return errUAOneClickMemoryLimit
	}
	// Strings can escape to six bytes per byte, followed by JSON buffer growth,
	// a string copy and CSV quote doubling. Bytes also need base64 scratch.
	return b.charge(int64(n) * 32)
}

func (b *uaOneClickMemory) measure(v reflect.Value, depth int) error {
	if depth > 100 {
		return errors.New("OPC UA snapshot value nesting exceeds 100 levels (possibly cyclic)")
	}
	if err := b.charge(128); err != nil || !v.IsValid() {
		return err
	}
	if err := b.charge(int64(v.Type().Size())); err != nil {
		return err
	}
	switch v.Type() {
	case reflect.TypeFor[time.Time]():
		// The owned copy keeps only instant/offset, not a possibly large
		// location table supplied by an injected/decoded value.
		return b.charge(1024)
	case reflect.TypeFor[*ua.NodeID](), reflect.TypeFor[ua.NodeID]():
		// Measure private bid/gid fields below without calling String or
		// MarshalJSON, either of which would allocate before admission.
	default:
		if (v.Type().Implements(reflect.TypeFor[json.Marshaler]()) || v.Type().Implements(reflect.TypeFor[encoding.TextMarshaler]())) && v.Type() != reflect.TypeFor[*time.Time]() {
			return errors.New("unsupported unbounded custom OPC UA JSON marshaler")
		}
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if !v.IsNil() {
			return b.measure(v.Elem(), depth+1)
		}
	case reflect.String:
		return b.bytes(v.Len())
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			return b.bytes(v.Len()) // copied to exact length after admission
		}
		// Reserve expanded []any slots and per-element work before walking.
		if err := b.bytes(v.Len()); err != nil {
			return err
		}
		for i := 0; i < v.Len(); i++ {
			if err := b.measure(v.Index(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		if int64(v.Len()) > b.remaining/256 {
			return errUAOneClickMemoryLimit
		}
		if err := b.charge(int64(v.Len()) * 256); err != nil {
			return err
		}
		it := v.MapRange()
		for it.Next() {
			key := it.Key()
			if key.Kind() != reflect.String && key.Type().Implements(reflect.TypeFor[encoding.TextMarshaler]()) {
				return errors.New("unsupported unbounded OPC UA map key marshaler")
			}
			switch key.Kind() {
			case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			default:
				return errors.New("unsupported unbounded OPC UA map key")
			}
			if err := b.measure(key, depth+1); err != nil {
				return err
			}
			if err := b.measure(it.Value(), depth+1); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if err := b.bytes(len(field.Name) + len(field.Tag)); err != nil {
				return err
			}
			if err := b.measure(v.Field(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Float32, reflect.Float64:
		if math.IsNaN(v.Float()) || math.IsInf(v.Float(), 0) {
			return b.charge(2048) // tagged maps and both encoder representations
		}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return b.charge(128) // boxes and worst-case numeric text/copy scratch
	default:
		return errors.New("unsupported unbounded OPC UA value kind")
	}
	return nil
}

// Preflight is allocation-free with respect to the payload; only after it fits
// do we normalize using the existing lossless integer/Variant/nonfinite schema.
func (b *uaOneClickMemory) ownEvent(event engine.Event) (engine.Event, error) {
	fields, ok := event.Data.(map[string]any)
	if !ok {
		return engine.Event{}, errors.New("unsupported non-object OPC UA event")
	}
	{
		// These engine fields are strings. Never let an injected Stringer call
		// fmt.Sprint and allocate arbitrary output in uaScanString after preflight.
		for _, key := range []string{"node_id", "parent_id", "display_name", "browse_name", "node_class", "status", "error", "value_type_name", "fingerprint", "reason"} {
			if fields[key] != nil {
				if _, ok := fields[key].(string); !ok {
					return engine.Event{}, errors.New("unsupported non-string OPC UA event metadata")
				}
			}
		}
		if fields["depth"] != nil {
			switch fields["depth"].(type) {
			case int, int64, uint32, float64, string:
			default:
				return engine.Event{}, errors.New("unsupported OPC UA depth metadata")
			}
		}
	}
	var keys []string
	switch event.Kind {
	case "scan-namespace":
		keys = []string{"fingerprint"}
	case "scan-node":
		keys = []string{"node_id", "parent_id", "display_name", "browse_name", "node_class", "depth"}
	case "scan-error":
		keys = []string{"error"}
	case "scan-summary":
		keys = []string{"complete", "reason"}
	case "value":
		keys = []string{"node_id", "has_value", "value", "value_type", "value_type_name", "value_is_array", "status", "error", "source_timestamp", "server_timestamp"}
	default:
		return engine.Event{}, errors.New("unsupported OPC UA observation kind")
	}
	trial := *b
	for _, key := range keys {
		if err := trial.measure(reflect.ValueOf(fields[key]), 0); err != nil {
			return engine.Event{}, err
		}
	}
	if err := trial.charge(2048); err != nil { // event scratch, row, seen/index maps and slice growth
		return engine.Event{}, err
	}
	// Build only retained fields, with constant keys. Unused event fields and
	// response-backed map keys are never held by the snapshot.
	owned := make(map[string]any, len(keys))
	for _, key := range keys {
		safe, err := uaScanJSONSafeValue(reflect.ValueOf(fields[key]), 0)
		if err != nil {
			return engine.Event{}, err
		}
		value, err := uaOneClickOwnValue(reflect.ValueOf(safe))
		if err != nil {
			return engine.Event{}, err
		}
		owned[key] = value.Interface()
	}
	event.Data = owned
	event.Kind = strings.Clone(event.Kind)
	_, offset := event.Time.Zone()
	event.Time = event.Time.In(time.FixedZone("", offset))
	*b = trial
	return event, nil
}

// Safe normalization still deliberately preserves ByteString/NodeID/DateTime
// encodings. Detach all response-backed storage, including slices into the
// middle of a larger buffer whose retained allocation cannot be inferred from cap.
func uaOneClickOwnValue(v reflect.Value) (reflect.Value, error) {
	if !v.IsValid() {
		return reflect.Zero(reflect.TypeFor[any]()), nil
	}
	if v.CanInterface() {
		switch value := v.Interface().(type) {
		case time.Time:
			_, offset := value.Zone()
			return reflect.ValueOf(value.In(time.FixedZone("", offset))), nil
		case *time.Time:
			if value == nil {
				return v, nil
			}
			_, offset := value.Zone()
			copy := value.In(time.FixedZone("", offset))
			return reflect.ValueOf(&copy), nil
		case *ua.NodeID:
			if value == nil {
				return v, nil
			}
			return reflect.ValueOf(ua.NewNodeIDFromExpandedNodeID(&ua.ExpandedNodeID{NodeID: value})), nil
		}
	}
	out := reflect.New(v.Type()).Elem()
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			child, err := uaOneClickOwnValue(v.Elem())
			if err != nil {
				return reflect.Value{}, err
			}
			out.Set(child)
		}
	case reflect.String:
		out.SetString(strings.Clone(v.String()))
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice {
			if v.IsNil() {
				return out, nil
			}
			out.Set(reflect.MakeSlice(v.Type(), v.Len(), v.Len()))
			if v.Type().Elem().Kind() == reflect.Uint8 {
				reflect.Copy(out, v)
				return out, nil
			}
		}
		for i := 0; i < v.Len(); i++ {
			child, err := uaOneClickOwnValue(v.Index(i))
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(child)
		}
	case reflect.Map:
		if !v.IsNil() {
			out.Set(reflect.MakeMapWithSize(v.Type(), v.Len()))
			it := v.MapRange()
			for it.Next() {
				key, err := uaOneClickOwnValue(it.Key())
				if err != nil {
					return reflect.Value{}, err
				}
				child, err := uaOneClickOwnValue(it.Value())
				if err != nil {
					return reflect.Value{}, err
				}
				out.SetMapIndex(key, child)
			}
		}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		out.Set(v)
	default:
		return reflect.Value{}, errors.New("unsupported owned OPC UA representation")
	}
	return out, nil
}
