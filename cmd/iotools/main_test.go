package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHelpAndVersionAreSuccessful(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--version"}} {
		if e := run(args); e != nil {
			t.Fatal(e)
		}
	}
}
func TestInitNeverOverwritesAndValidateDoesNotConnect(t *testing.T) {
	p := filepath.Join(t.TempDir(), "demo.yaml")
	if e := run([]string{"--file", p, "--init"}); e != nil {
		t.Fatal(e)
	}
	old, _ := os.ReadFile(p)
	if e := run([]string{"--file", p, "--init"}); e == nil {
		t.Fatal("init overwrote existing configuration")
	}
	now, _ := os.ReadFile(p)
	if string(old) != string(now) {
		t.Fatal("existing collection changed")
	}
	if e := run([]string{"--file", p, "--validate"}); e != nil {
		t.Fatal(e)
	}
}
func TestAPIRejectsMissingOrPublicScope(t *testing.T) {
	p := filepath.Join(t.TempDir(), "api.yaml")
	text := "version: 1\nrequests:\n- id: device\n  protocol: modbus\n  action: read-holding\n  endpoint: tcp://127.0.0.1:1\n  params: {unit: 1, address: 0}\n"
	os.WriteFile(p, []byte(text), 0600)
	if e := run([]string{"--file", p, "--serve-modbus", "device", "--allow-writes"}); e == nil {
		t.Fatal("unbounded API write scope accepted")
	}
	if e := run([]string{"--file", p, "--serve-modbus", "device", "--listen", "0.0.0.0:8089"}); e == nil {
		t.Fatal("public bind accepted")
	}
}
