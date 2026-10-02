//go:build android

package engine

import (
	"context"
	"strings"
	"testing"
)

func TestAndroidSerialPathsRequireUSBHost(t *testing.T) {
	if err := checkModbusDeviceSerial(); err == nil || !strings.Contains(err.Error(), "USB") {
		t.Fatalf("desktop serial paths accepted: %v", err)
	}
	ports, err := ListModbusSerialPorts(context.Background())
	if err == nil || len(ports.Names) != 0 {
		t.Fatalf("Android must not enumerate desktop /dev paths: %+v %v", ports, err)
	}
}
