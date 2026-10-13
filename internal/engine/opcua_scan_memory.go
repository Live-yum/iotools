package engine

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/gopcua/opcua/ua"
)

// A monotonic allocation reservation for one scan's owned metadata, frontier,
// deduplication and formatting scratch, not a process RSS limit. The current
// server response is already decoded before admission; its allocation (and any
// storage independently retained by an event consumer) is outside this bound.
const opcuaScanMemoryLimit int64 = 128 << 20

var errOPCUAScanMemoryLimit = errors.New("OPC UA scan metadata memory budget (128 MiB reservation) exceeded; scan incomplete")

// Cleanup encodes at most one token at a time with this independent scratch
// allowance. An oversized decoded token cannot safely be sent back merely to
// release it: that would allocate it again in the request encoder.
const opcuaScanCleanupTokenLimit = (32 << 20) / 32

var errOPCUAScanReleaseLimit = fmt.Errorf("%w; continuation token exceeds bounded cleanup encoding budget; token was not released to the server", errOPCUAScanMemoryLimit)
var errOPCUAScanReleaseFailed = errors.New("OPC UA scan continuation release failed or timed out; server-side release unconfirmed")

type opcuaScanMemory struct{ remaining int64 }

func (b *opcuaScanMemory) charge(n int64) error {
	if n < 0 || n > b.remaining {
		return errOPCUAScanMemoryLimit
	}
	b.remaining -= n
	return nil
}

func (b *opcuaScanMemory) bytes(n int) error {
	if n < 0 || int64(n) > b.remaining/32 {
		return errOPCUAScanMemoryLimit
	}
	// Includes owned clones, canonical/base64 IDs, JSON escaping (up to 6x),
	// buffer growth and diagnostic formatting copies, before any of these run.
	return b.charge(int64(n) * 32)
}

// NodeID exposes no allocation-free payload-length accessor. Inspect only its
// fixed private representation, never call String/Encode to measure it. Both
// bid and GUID.Data4 may alias a much larger decoded buffer, so clone on admit.
func (b *opcuaScanMemory) nodeID(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			return b.nodeID(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if err := b.nodeID(v.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Slice:
		return b.bytes(v.Len())
	}
	return nil
}

func (b *opcuaScanMemory) ownNodeID(id *ua.NodeID) (*ua.NodeID, string, error) {
	if id == nil || id.Type() > ua.NodeIDTypeByteString {
		return nil, "", errors.New("malformed scan NodeID")
	}
	trial := *b
	if err := trial.charge(512); err != nil {
		return nil, "", err
	}
	if err := trial.nodeID(reflect.ValueOf(id)); err != nil {
		return nil, "", err
	}
	owned := ua.NewNodeIDFromExpandedNodeID(ua.NewExpandedNodeID(id, "", 0))
	key := owned.String()
	*b = trial
	return owned, key, nil
}

func (b *opcuaScanMemory) ownNames(n uaScanNode) (uaScanNode, error) {
	trial := *b
	if err := trial.charge(512); err != nil { // event map, frontier growth and seen entry
		return uaScanNode{}, err
	}
	for _, s := range []string{n.parent, n.display, n.browse} {
		if err := trial.bytes(len(s)); err != nil {
			return uaScanNode{}, err
		}
	}
	n.parent, n.display, n.browse = strings.Clone(n.parent), strings.Clone(n.display), strings.Clone(n.browse)
	*b = trial
	return n, nil
}

func (b *opcuaScanMemory) ownPoint(point []byte) ([]byte, error) {
	trial := *b
	if err := trial.charge(128); err != nil {
		return nil, err
	}
	if err := trial.bytes(len(point)); err != nil {
		return nil, err
	}
	*b = trial
	return bytes.Clone(point), nil
}
