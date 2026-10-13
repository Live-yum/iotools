package engine

import (
	"testing"

	"github.com/gopcua/opcua/ua"
)

func TestOPCUADataReportsVariantPresenceIncludingNull(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value *ua.DataValue
		want  bool
	}{
		{"bad-node", &ua.DataValue{Status: ua.StatusBadNodeIDUnknown}, false},
		{"denied", &ua.DataValue{Status: ua.StatusBadUserAccessDenied}, false},
		{"null-variant", &ua.DataValue{Value: &ua.Variant{}, Status: ua.StatusOK}, true},
		{"int-variant", &ua.DataValue{Value: ua.MustVariant(int32(7)), Status: ua.StatusOK}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := opcuaData("i=1", tc.value)
			if got["has_value"] != tc.want {
				t.Fatalf("got %+v; presence must mean Variant exists", got)
			}
			if tc.name == "null-variant" && (got["value"] != nil || got["value_type"] != ua.TypeIDNull) {
				t.Fatalf("UA Null corrupted: %+v", got)
			}
		})
	}
}
