//go:build !windows

package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type serialTestInfo struct {
	name string
	mode os.FileMode
}

func (f serialTestInfo) Name() string       { return f.name }
func (f serialTestInfo) Size() int64        { return 0 }
func (f serialTestInfo) Mode() os.FileMode  { return f.mode }
func (f serialTestInfo) ModTime() time.Time { return time.Time{} }
func (f serialTestInfo) IsDir() bool        { return false }
func (f serialTestInfo) Sys() any           { return nil }
func TestModbusSerialEnumerationMetadataOnlyFixture(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"ttyUSB0", "ttyACM1", "ttyS2", "ttyAMA0", "cu.usbmodem1", "random", "ttyUSBfake"} {
		if e := os.WriteFile(filepath.Join(root, name), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.MkdirAll(filepath.Join(root, "serial", "by-id"), 0700); e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(filepath.Join(root, "serial", "by-id", "usb-vendor"), nil, 0600)
	opened := []string{}
	stat := func(path string) (os.FileInfo, error) {
		mode := os.ModeDevice | os.ModeCharDevice
		if filepath.Base(path) == "ttyUSBfake" {
			mode = 0600
		}
		return serialTestInfo{filepath.Base(path), mode}, nil
	}
	p, e := modbusSerialMetadata(context.Background(), root, func(path string) (*os.File, error) { opened = append(opened, path); return os.Open(path) }, stat)
	if e != nil || len(p.Names) != 6 || len(opened) != 2 {
		t.Fatal(p, opened, e)
	}
	for _, path := range opened {
		if path != root && path != filepath.Join(root, "serial", "by-id") {
			t.Fatal("opened device", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = modbusSerialMetadata(ctx, root, os.Open, stat); e == nil {
		t.Fatal("ignored cancellation")
	}
}
