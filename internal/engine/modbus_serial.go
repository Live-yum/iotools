package engine

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type ModbusSerialPorts struct {
	Names     []string
	Truncated bool
}

// ListModbusSerialPorts reads OS metadata only. It never opens a serial device.
func ListModbusSerialPorts(ctx context.Context) (ModbusSerialPorts, error) {
	return listModbusSerialPorts(ctx)
}
func normalizeModbusPorts(names []string, truncated bool) ModbusSerialPorts {
	seen := map[string]bool{}
	out := ModbusSerialPorts{Truncated: truncated}
	for _, n := range names {
		if n == "" || len(n) > 4096 || strings.IndexFunc(n, unicode.IsControl) >= 0 || seen[n] {
			continue
		}
		seen[n] = true
		out.Names = append(out.Names, n)
	}
	sort.Slice(out.Names, func(i, j int) bool {
		a, b := out.Names[i], out.Names[j]
		if strings.HasPrefix(a, "COM") && strings.HasPrefix(b, "COM") {
			x, e := strconv.Atoi(a[3:])
			y, f := strconv.Atoi(b[3:])
			if e == nil && f == nil {
				return x < y
			}
		}
		return a < b
	})
	if len(out.Names) > 256 {
		out.Names = out.Names[:256]
		out.Truncated = true
	}
	return out
}
