package engine

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitUAIdentityGenerationNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "client.pem"), filepath.Join(dir, "client-key.pem")
	if e := GenerateOPCUAClientIdentity(cert, key, "urn:iotools:test-client"); e != nil {
		t.Fatal(e)
	}
	pair, e := tls.LoadX509KeyPair(cert, key)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil || len(parsed.URIs) != 1 || parsed.URIs[0].String() != "urn:iotools:test-client" {
		t.Fatal("application identity missing", e)
	}
	before, _ := os.ReadFile(key)
	if e := GenerateOPCUAClientIdentity(cert, key, "urn:iotools:other"); e == nil {
		t.Fatal("overwrote identity")
	}
	after, _ := os.ReadFile(key)
	if string(before) != string(after) {
		t.Fatal("private key changed")
	}
	if e := GenerateOPCUAClientIdentity(filepath.Join(dir, "new.pem"), filepath.Join(dir, "new-key.pem"), "relative"); e == nil {
		t.Fatal("relative URI accepted")
	}
}

func TestCancelledIdentityDoesNotCreateFiles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if e := GenerateOPCUAClientIdentityContext(ctx, cert, key, "urn:iotools:test"); e == nil {
		t.Fatal("cancelled generation succeeded")
	}
	if _, e := os.Stat(cert); !os.IsNotExist(e) {
		t.Fatal("cancelled generation wrote certificate")
	}
	if _, e := os.Stat(key); !os.IsNotExist(e) {
		t.Fatal("cancelled generation wrote key")
	}
}
