//go:build !android

package engine

// Desktop device-path RTU support is unchanged by the Android USB transport.
func checkModbusDeviceSerial() error { return nil }
