package engine

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
)

type Event struct {
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	Data any       `json:"data"`
}
type Emit func(Event)

func send(emit Emit, kind string, data any) {
	if emit != nil {
		emit(Event{time.Now().UTC(), kind, data})
	}
}
func Run(ctx context.Context, r config.Request, allowWrites bool, emit Emit) error {
	if r.Mutates() && !allowWrites {
		return fmt.Errorf("%s is a write operation; explicit confirmation or --allow-writes is required", r.Action)
	}
	d, e := r.Duration()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	switch r.Protocol {
	case "http":
		return runHTTP(ctx, r, emit)
	case "mqtt":
		return runMQTT(ctx, r, emit)
	case "kafka":
		return runKafka(ctx, r, emit)
	case "modbus":
		return runModbus(ctx, r, emit)
	case "opcua":
		return runOPCUA(ctx, r, emit)
	}
	return fmt.Errorf("unsupported protocol %q", r.Protocol)
}
func tlsConfig(r config.Request) (*tls.Config, error) {
	t := &tls.Config{MinVersion: tls.VersionTLS12}
	if file := r.String("ca_file", ""); file != "" {
		pem, e := os.ReadFile(file)
		if e != nil {
			return nil, e
		}
		pool, e := x509.SystemCertPool()
		if e != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA file contains no certificates")
		}
		t.RootCAs = pool
	}
	cert, key := r.String("cert_file", ""), r.String("key_file", "")
	if (cert == "") != (key == "") {
		return nil, fmt.Errorf("cert_file and key_file must be provided together")
	}
	if cert != "" {
		c, e := tls.LoadX509KeyPair(cert, key)
		if e != nil {
			return nil, e
		}
		t.Certificates = []tls.Certificate{c}
	}
	return t, nil
}
func unsupported(r config.Request, allowed ...string) error {
	return fmt.Errorf("%s action %q unsupported; choose %s", r.Protocol, r.Action, strings.Join(allowed, ", "))
}
