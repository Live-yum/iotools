package main

import (
	"crypto/x509"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPortableEmbeddedRoots(t *testing.T) {
	if os.Getenv("IOTOOLS_ROOTS_CHILD") == "1" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil || len(pool.Subjects()) < 50 {
			t.Fatalf("embedded public roots unavailable: %v", err)
		}
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestPortableEmbeddedRoots$")
	env := []string{}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GODEBUG=") && !strings.HasPrefix(e, "IOTOOLS_ROOTS_CHILD=") {
			env = append(env, e)
		}
	}
	command.Env = append(env, "GODEBUG=x509usefallbackroots=1", "IOTOOLS_ROOTS_CHILD=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("isolated fallback test: %v %s", err, output)
	}
}
