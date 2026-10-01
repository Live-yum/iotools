package mobileapi

import (
	"testing"

	"github.com/Live-yum/iotools/internal/config"
)

func TestNativeSerialCapabilityIsHostOnlyAndSurvivesPreferences(t *testing.T) {
	request := config.Request{ID: "serial", Protocol: "modbus", Action: "read-holding", Endpoint: "rtu:///iotools-nonexistent-test-port", Params: map[string]any{"unit": 1, "address": 0, "count": 1}}
	mobile := testSession(t, request)
	if cmd(t, mobile, map[string]any{"op": "preview", "request_id": "serial"})["ok"] != false {
		t.Fatal("a mobile/default host enabled native serial")
	}
	for _, key := range []string{"native_serial", "NativeSerial"} {
		if cmd(t, mobile, map[string]any{"op": "options.set", "options": map[string]any{key: true}})["ok"] != false {
			t.Fatal("JSON expanded host serial capability")
		}
	}
	desktop, err := Open(mobile.path, "test", Options{NativeSerial: true, RequireExisting: true})
	if err != nil {
		t.Fatal(err)
	}
	defer desktop.Close()
	previewToken(t, desktop, "serial") // Local validation only: nonexistent port is never opened.
	mustOK(t, desktop, map[string]any{"op": "options.set", "options": map[string]any{"read_only": true, "history": false}})
	previewToken(t, desktop, "serial")
	request.Action = "write-register"
	request.Params = map[string]any{"unit": 1, "address": 0, "value": 99}
	if cmd(t, desktop, map[string]any{"op": "preview", "request": request})["ok"] != false {
		t.Fatal("native serial capability bypassed read-only protection")
	}
}
