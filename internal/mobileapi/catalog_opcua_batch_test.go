package mobileapi

import (
	"context"
	"strings"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
)

func TestCatalogOPCUAReadBatchSize(t *testing.T) {
	for _, protocol := range Catalog()["protocols"].([]Protocol) {
		if protocol.ID != "opcua" {
			continue
		}
		var batchFields []Field
		for _, field := range protocol.Fields {
			if field.Key == "read_batch_size" {
				batchFields = append(batchFields, field)
			}
		}
		if len(batchFields) != 1 || batchFields[0].Type != "number" {
			t.Fatalf("read_batch_size must have one numeric field: %+v", batchFields)
		}
		field := batchFields[0]
		if !strings.Contains(field.Label, "每批读取属性数量") || !strings.Contains(field.Hint, "默认 100") || !strings.Contains(field.Hint, "1..1000") {
			t.Fatalf("read_batch_size label or bounds missing: %+v", field)
		}
		defaults := map[string]any{}
		for _, action := range protocol.Actions {
			defaults[action.ID] = action.Defaults["read_batch_size"]
		}
		for _, action := range []string{"read", "attributes"} {
			if defaults[action] != 100 {
				t.Errorf("%s read_batch_size = %v, want 100", action, defaults[action])
			}
		}
		return
	}
	t.Fatal("OPC UA protocol missing from catalog")
}

func TestOPCUAReadBatchSizeFormPreview(t *testing.T) {
	s := testSession(t)
	for _, value := range []string{"1", "100", "1000", "+100", "00100"} {
		t.Run(value, func(t *testing.T) {
			r := config.Request{ID: "read", Protocol: "opcua", Action: "read", Endpoint: "opc.tcp://127.0.0.1:4840", Params: map[string]any{"node_id": "i=85", "read_batch_size": value}}
			preview := mustOK(t, s, map[string]any{"op": "preview", "request": r}).(map[string]any)
			request := preview["request"].(map[string]any)
			params := request["params"].(map[string]any)
			if got := params["read_batch_size"]; got != value {
				t.Errorf("preview read_batch_size = %v, want %v", got, value)
			}
		})
	}
}

func TestOPCUAReadBatchSizeInvalidFormValuesRejectedBeforeConnection(t *testing.T) {
	for _, value := range []string{"0", "-1", "1001", "1.5", "100.0", "1e2", "0x64", " 100 ", "100\n", "abc", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			r := config.Request{Protocol: "opcua", Action: "read", Endpoint: "opc.tcp://127.0.0.1:1", Timeout: "1s", Params: map[string]any{"node_id": "i=85", "read_batch_size": value}}
			err := engine.Run(context.Background(), r, false, nil)
			if err == nil || err.Error() != "read_batch_size must be an integer in 1..1000" {
				t.Fatalf("invalid form value %q was not rejected before connection: %v", value, err)
			}
		})
	}
}
