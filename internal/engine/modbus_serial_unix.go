//go:build !windows

package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func modbusSerialName(name string) bool {
	for _, prefix := range []string{"ttyUSB", "ttyACM", "ttyS", "ttyAMA", "ttyTHS", "rfcomm", "cu.", "tty."} {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			return true
		}
	}
	return false
}
func listModbusSerialPorts(ctx context.Context) (ModbusSerialPorts, error) {
	return modbusSerialMetadata(ctx, "/dev", os.Open, os.Stat)
}

// Injectable metadata operations allow enumeration tests without opening ports.
func modbusSerialMetadata(ctx context.Context, root string, open func(string) (*os.File, error), stat func(string) (os.FileInfo, error)) (ModbusSerialPorts, error) {
	names := []string{}
	truncated := false
	for _, dir := range []string{root, filepath.Join(root, "serial", "by-id")} {
		if err := ctx.Err(); err != nil {
			return ModbusSerialPorts{}, err
		}
		f, e := open(dir)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return ModbusSerialPorts{}, e
		}
		entries, e := f.ReadDir(8193)
		_ = f.Close()
		if e != nil && !errors.Is(e, io.EOF) {
			return ModbusSerialPorts{}, e
		}
		if len(entries) > 8192 {
			entries = entries[:8192]
			truncated = true
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return ModbusSerialPorts{}, err
			}
			if dir == root && !modbusSerialName(entry.Name()) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			info, e := stat(path)
			if e == nil && info.Mode()&os.ModeCharDevice != 0 {
				names = append(names, path)
			}
		}
	}
	return normalizeModbusPorts(names, truncated), nil
}
