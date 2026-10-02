package engine

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestModbusAPICarriesExplicitUSBTransportContext(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	transport := &usbTestTransport{exchange: func(ctx context.Context, endpoint string, request []byte, settings ModbusRTUSettings, timeout time.Duration) ([]byte, error) {
		if endpoint != "usb://123/0" || request[0] != 1 || request[1] != 3 {
			t.Errorf("unexpected explicit USB request: %s %x", endpoint, request)
		}
		return usbTestResponse(t, 1, 3, []byte{2, 0, 42}), nil
	}}
	ctx, cancel := context.WithCancel(WithModbusRTUTransport(context.Background(), transport))
	done := make(chan error, 1)
	base := usbTestRequest()
	base.Protocol = "modbus"
	go func() { done <- ServeModbusAPI(ctx, address, base, nil) }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(2 * time.Second):
			t.Error("controller did not stop")
		}
	}()
	client := &http.Client{Timeout: time.Second}
	ready := false
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		response, e := client.Get("http://" + address + "/health")
		if e == nil {
			_ = response.Body.Close()
			ready = response.StatusCode == 200
			if ready {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("loopback controller did not start")
	}
	calls, _ := transport.counts()
	if calls != 0 {
		t.Fatal("health opened the device")
	}
	for i := 0; i < 2; i++ {
		response, err := client.Post("http://"+address+"/read", "application/json", bytes.NewBufferString(`{"type":"holding","address":0,"count":1}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 || !strings.Contains(string(body), "42") {
			t.Fatalf("transport context lost: %d %s", response.StatusCode, body)
		}
	}
	calls, closes := transport.counts()
	if calls != 2 || closes != 0 {
		t.Fatalf("repeated reads must retain selected port: calls=%d closes=%d", calls, closes)
	}
	response, err := client.Post("http://"+address+"/write", "application/json", bytes.NewBufferString(`{"type":"holding","address":0,"values":[9]}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only controller accepted write: %d", response.StatusCode)
	}
	calls, _ = transport.counts()
	if calls != 2 {
		t.Fatal("rejected write reached device")
	}
}
