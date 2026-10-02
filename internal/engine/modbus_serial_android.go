//go:build android

package engine

import (
	"context"
	"errors"
)

func checkModbusDeviceSerial() error {
	return errors.New("Android cannot open desktop serial paths; explicitly select, permit and open an attached USB serial port")
}

// Android's app sandbox cannot treat /dev enumeration as permission to use a
// USB device. The native UI enumerates UsbManager device descriptors instead.
func listModbusSerialPorts(ctx context.Context) (ModbusSerialPorts, error) {
	if err := ctx.Err(); err != nil {
		return ModbusSerialPorts{}, err
	}
	return ModbusSerialPorts{}, errors.New("select an attached USB serial port in Android USB settings; desktop serial enumeration is unavailable")
}
