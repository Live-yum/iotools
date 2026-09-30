package engine

import "github.com/Live-yum/iotools/internal/config"

// ModbusRules validates and returns an independent typed annotation model for
// native editors; it performs no device or file operations.
func ModbusRules(r config.Request) ([]RegisterRule, error) {
	a, err := parseRegisterAnnotations(r)
	return a.Rules, err
}

// ValidateModbusRaw exposes the same bounded function/range classification used
// by the transport. A UI preview cannot weaken the engine's write gate.
func ValidateModbusRaw(r config.Request) ([]byte, error) {
	if err := validateParams(r); err != nil {
		return nil, err
	}
	return validateRawPDU(r)
}
