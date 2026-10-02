//go:build windows

package engine

import (
	"context"
	"errors"
	"golang.org/x/sys/windows/registry"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
)

func listModbusSerialPorts(ctx context.Context) (ModbusSerialPorts, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return ModbusSerialPorts{}, nil
	}
	if err != nil {
		return ModbusSerialPorts{}, err
	}
	defer key.Close()
	names, err := key.ReadValueNames(257)
	if err != nil && !errors.Is(err, io.EOF) {
		return ModbusSerialPorts{}, err
	}
	truncated := len(names) > 256
	if truncated {
		names = names[:256]
	}
	ports := []string{}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return ModbusSerialPorts{}, err
		}
		buf := make([]byte, 256)
		n, kind, e := key.GetValue(name, buf)
		if e != nil || kind != registry.SZ || n < 2 || n > len(buf) || n%2 != 0 {
			continue
		}
		words := make([]uint16, n/2)
		for i := range words {
			words[i] = uint16(buf[i*2]) | uint16(buf[i*2+1])<<8
		}
		s := strings.TrimRight(string(utf16.Decode(words)), "\x00")
		if !strings.HasPrefix(s, "COM") {
			continue
		}
		number, e := strconv.Atoi(s[3:])
		if e == nil && number > 0 && number <= 65535 {
			ports = append(ports, s)
		}
	}
	return normalizeModbusPorts(ports, truncated), nil
}
