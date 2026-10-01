package mobileapi

import (
	"crypto/sha256"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityFingerprintUsesCertificateDERAndNeverReturnsPrivateKey(t *testing.T) {
	s := testSession(t)
	cert, key := filepath.Join(s.root, "client.pem"), filepath.Join(s.root, "client-key.pem")
	mustOK(t, s, map[string]any{"op": "opcua.identity", "confirmed": true, "cert_path": cert, "key_path": key, "application_uri": "urn:iotools:loopback-test"})
	found := false
	for _, event := range awaitDone(t, s) {
		if event["kind"] != "identity" {
			continue
		}
		data := event["data"].(map[string]any)
		encoded, err := os.ReadFile(cert)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(encoded)
		if block == nil {
			t.Fatal("invalid certificate")
		}
		expected := fmt.Sprintf("%x", sha256.Sum256(block.Bytes))
		if data["sha256_fingerprint"] != expected {
			t.Fatalf("fingerprint mismatch")
		}
		for _, value := range data {
			if strings.Contains(fmt.Sprint(value), "PRIVATE KEY") {
				t.Fatal("private key content leaked")
			}
		}
		found = true
	}
	if !found {
		t.Fatal("identity event missing")
	}
}
