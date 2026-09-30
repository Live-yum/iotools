package engine

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
	"github.com/gopcua/opcua/uasc"
)

func TestOPCUATypedValues(t *testing.T) {
	cases := []struct {
		kind string
		raw  any
		want any
	}{{"Int16", -3, int16(-3)}, {"UInt32", "4294967295", uint32(4294967295)}, {"Boolean", true, true}, {"Double", 2.5, float64(2.5)}, {"ByteString", "AQI=", []byte{1, 2}}, {"String", "hello", "hello"}, {"Int32[]", []any{1, 2}, []int32{1, 2}}, {"Int32[]", []any{}, []int32{}}}
	for _, tc := range cases {
		v, e := opcuaVariant(tc.kind, tc.raw)
		if e != nil || !reflect.DeepEqual(v.Value(), tc.want) {
			t.Fatalf("%s: %v %v", tc.kind, v, e)
		}
	}
	for _, tc := range []struct {
		kind string
		raw  any
	}{{"Int16", 65536}, {"UInt32", -1}, {"Boolean", 2}, {"Int32", 1.1}, {"String", 1}, {"Double", "NaN"}, {"DateTime", "yesterday"}, {"ByteString", "not-base64"}, {"Unknown", 1}} {
		if _, e := opcuaVariant(tc.kind, tc.raw); e == nil {
			t.Fatalf("accepted invalid %s", tc.kind)
		}
	}
	if _, e := opcuaArguments([]any{map[string]any{"type": "Int32", "value": 1}}); e != nil {
		t.Fatal(e)
	}
	if _, e := opcuaArguments([]any{map[string]any{"type": "Int32", "value": 1, "script": "bad"}}); e == nil {
		t.Fatal("unknown argument field accepted")
	}
}

func testUACertificate(t *testing.T, uri string) ([]byte, *rsa.PrivateKey, string, string) {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(uri)
	cert := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "Disposable loopback OPC UA test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{u}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, IsCA: true, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	cp, kp := filepath.Join(dir, "certificate.pem"), filepath.Join(dir, "key.pem")
	if e = os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); e != nil {
		t.Fatal(e)
	}
	return der, key, cp, kp
}

func TestOPCUACertificateTrust(t *testing.T) {
	der, _, ca, _ := testUACertificate(t, "urn:iotools:test:server")
	ep := &ua.EndpointDescription{ServerCertificate: der, Server: &ua.ApplicationDescription{ApplicationURI: "urn:iotools:test:server"}}
	sum := sha256.Sum256(der)
	r := config.Request{Params: map[string]any{"server_cert_sha256": hex.EncodeToString(sum[:])}}
	if e := opcuaTrust(r, ep, "127.0.0.1", time.Now()); e != nil {
		t.Fatal(e)
	}
	r.Params["ca_file"] = ca
	if e := opcuaTrust(r, ep, "127.0.0.1", time.Now()); e != nil {
		t.Fatal(e)
	}
	delete(r.Params, "server_cert_sha256")
	if e := opcuaTrust(r, ep, "127.0.0.1", time.Now()); e != nil {
		t.Fatal(e)
	}
	delete(r.Params, "ca_file")
	if e := opcuaTrust(r, ep, "127.0.0.1", time.Now()); e == nil {
		t.Fatal("discovery treated as trust")
	}
	r.Params["server_cert_sha256"] = strings.Repeat("00", 32)
	if e := opcuaTrust(r, ep, "127.0.0.1", time.Now()); e == nil {
		t.Fatal("bad pin accepted")
	}
	r.Params["server_cert_sha256"] = hex.EncodeToString(sum[:])
	if e := opcuaTrust(r, ep, "192.0.2.1", time.Now()); e == nil {
		t.Fatal("wrong hostname accepted")
	}
	if e := opcuaTrust(r, ep, "127.0.0.1", time.Now().Add(2*time.Hour)); e == nil {
		t.Fatal("expired cert accepted")
	}
	ep.Server.ApplicationURI = "urn:wrong"
	if e := opcuaTrust(r, ep, "127.0.0.1", time.Now()); e == nil {
		t.Fatal("wrong app URI accepted")
	}
}

func TestOPCUANoSecurityDowngrade(t *testing.T) {
	ep := &ua.EndpointDescription{SecurityPolicyURI: ua.SecurityPolicyURINone, SecurityMode: ua.MessageSecurityModeNone, UserIdentityTokens: []*ua.UserTokenPolicy{{TokenType: ua.UserTokenTypeAnonymous}}}
	r := config.Request{Params: map[string]any{}}
	if _, _, e := opcuaOptions(r, []*ua.EndpointDescription{ep}, "127.0.0.1"); e == nil {
		t.Fatal("default downgraded to None")
	}
	r.Params["security_policy"] = "None"
	r.Params["security_mode"] = "None"
	if _, _, e := opcuaOptions(r, []*ua.EndpointDescription{ep}, "127.0.0.1"); e == nil {
		t.Fatal("insecure accepted without opt-in")
	}
	r.Params["allow_insecure"] = true
	if _, _, e := opcuaOptions(r, []*ua.EndpointDescription{ep}, "127.0.0.1"); e != nil {
		t.Fatal(e)
	}
	r.Params["auth"] = "username"
	if _, _, e := opcuaOptions(r, []*ua.EndpointDescription{ep}, "127.0.0.1"); e == nil {
		t.Fatal("insecure credentials allowed")
	}
}

type fakeUABrowser struct{ next, release int }

func (f *fakeUABrowser) Browse(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
	return &ua.BrowseResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{{BrowseName: &ua.QualifiedName{Name: "first"}}}, ContinuationPoint: []byte{1}}}}, nil
}
func (f *fakeUABrowser) BrowseNext(_ context.Context, r *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
	if r.ReleaseContinuationPoints {
		f.release++
		return &ua.BrowseNextResponse{}, nil
	}
	f.next++
	return &ua.BrowseNextResponse{Results: []*ua.BrowseResult{{References: []*ua.ReferenceDescription{{BrowseName: &ua.QualifiedName{Name: "second"}}, {BrowseName: &ua.QualifiedName{Name: "third"}}}, ContinuationPoint: []byte{2}}}}, nil
}
func TestOPCUABrowseContinuationReleasedAtBound(t *testing.T) {
	fake := &fakeUABrowser{}
	r := config.Request{Params: map[string]any{"max_references": 2}}
	count := 0
	e := opcuaBrowse(context.Background(), fake, r, func(Event) { count++ })
	if e == nil || fake.next != 1 || fake.release != 1 || count != 2 {
		t.Fatalf("error=%v next=%d release=%d count=%d", e, fake.next, fake.release, count)
	}
}

// All network tests create a disposable server bound to loopback only. No plant,
// LAN discovery scan, production credential or industrial endpoint is accessed.
func localUAServer(t *testing.T, secure bool) (string, *server.MapNamespace, map[string]any) {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	opts := []server.Option{server.EndPoint("127.0.0.1", port), server.EnableAuthMode(ua.UserTokenTypeAnonymous)}
	params := map[string]any{"security_policy": "None", "security_mode": "None", "allow_insecure": true}
	if secure {
		der, key, _, _ := testUACertificate(t, "urn:iotools:test:server")
		_, _, clientCert, clientKey := testUACertificate(t, "urn:iotools:test:client")
		opts = append(opts, server.Certificate(der), server.PrivateKey(key))
		for _, policy := range []string{"Basic256Sha256", "Aes128_Sha256_RsaOaep", "Aes256_Sha256_RsaPss"} {
			opts = append(opts, server.EnableSecurity(policy, ua.MessageSecurityModeSignAndEncrypt))
		}
		sum := sha256.Sum256(der)
		params = map[string]any{"cert_file": clientCert, "key_file": clientKey, "server_cert_sha256": hex.EncodeToString(sum[:])}
	} else {
		opts = append(opts, server.EnableSecurity("None", ua.MessageSecurityModeNone))
	}
	srv := server.New(opts...)
	ns := server.NewMapNamespace(srv, "iotools-disposable-test")
	ns.Data["Value"] = int32(7)
	for i := 0; i < 205; i++ {
		ns.Data[fmt.Sprintf("Extra%03d", i)] = int32(i)
	}
	root, _ := srv.Namespace(0)
	root.Objects().AddRef(ns.Objects(), id.HasComponent, true)
	srv.RegisterHandler(id.CallRequest_Encoding_DefaultBinary, func(_ *uasc.SecureChannel, request ua.Request, _ uint32) (ua.Response, error) {
		req := request.(*ua.CallRequest)
		result := &ua.CallMethodResult{StatusCode: ua.StatusBadInvalidArgument}
		if len(req.MethodsToCall) == 1 && len(req.MethodsToCall[0].InputArguments) == 1 {
			if input, ok := req.MethodsToCall[0].InputArguments[0].Value().(int32); ok {
				v, _ := ua.NewVariant(input * 2)
				result = &ua.CallMethodResult{StatusCode: ua.StatusOK, OutputArguments: []*ua.Variant{v}}
			}
		}
		return &ua.CallResponse{ResponseHeader: &ua.ResponseHeader{Timestamp: time.Now(), RequestHandle: req.RequestHeader.RequestHandle, ServiceResult: ua.StatusOK, ServiceDiagnostics: &ua.DiagnosticInfo{}}, Results: []*ua.CallMethodResult{result}}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	if e = srv.Start(ctx); e != nil {
		cancel()
		t.Fatal(e)
	}
	t.Cleanup(func() { cancel(); _ = srv.Close() })
	return fmt.Sprintf("opc.tcp://127.0.0.1:%d", port), ns, params
}

func TestOPCUALoopbackDiscoveryBrowseReadWriteCallSubscribe(t *testing.T) {
	endpoint, ns, params := localUAServer(t, false)
	node := ua.NewStringNodeID(ns.ID(), "Value").String()
	run := func(action string, extra map[string]any) ([]Event, error) {
		p := map[string]any{}
		for k, v := range params {
			p[k] = v
		}
		for k, v := range extra {
			p[k] = v
		}
		r := config.Request{Protocol: "opcua", Action: action, Endpoint: endpoint, Timeout: "12s", Params: p}
		var events []Event
		err := Run(context.Background(), r, true, func(e Event) { events = append(events, e) })
		return events, err
	}
	if events, e := run("discover", nil); e != nil || len(events) == 0 {
		t.Fatalf("discover: %v", e)
	}
	events, e := run("browse", map[string]any{"node_id": ua.NewNumericNodeID(ns.ID(), id.ObjectsFolder).String()})
	if e != nil {
		t.Fatal(e)
	}
	refs := 0
	for _, event := range events {
		if event.Kind == "reference" {
			refs++
		}
	}
	if refs < 206 {
		t.Fatalf("continuation browse returned %d references", refs)
	}
	if _, e = run("read", map[string]any{"node_id": node}); e != nil {
		t.Fatal(e)
	}
	write := config.Request{Protocol: "opcua", Action: "write", Endpoint: endpoint, Params: map[string]any{"node_id": node, "value_type": "Int32", "value": 42}}
	if e = Run(context.Background(), write, false, nil); e == nil {
		t.Fatal("write confirmation bypass")
	}
	if ns.GetValue("Value") != int32(7) {
		t.Fatal("write ran before confirmation")
	}
	if _, e = run("write", map[string]any{"node_id": node, "value_type": "Int32", "value": 42}); e != nil {
		t.Fatal(e)
	}
	if ns.GetValue("Value") != int32(42) {
		t.Fatal("typed write did not reach simulator")
	}
	if events, e = run("call", map[string]any{"object_id": "i=85", "method_id": "ns=1;s=Double", "arguments": []any{map[string]any{"type": "Int32", "value": 6}}}); e != nil {
		t.Fatal(e)
	}
	if got := events[len(events)-1].Data.(map[string]any)["outputs"].([]any)[0]; got != int32(12) {
		t.Fatalf("method output %v", got)
	}
	if events, e = run("subscribe", map[string]any{"node_id": node, "interval_ms": 100, "max_events": 1}); e != nil {
		t.Fatal(e)
	}
	if events[len(events)-1].Kind != "notification" {
		t.Fatal("no real monitored notification")
	}
	if _, e = run("read", map[string]any{"node_id": "ns=65530;s=missing"}); e == nil {
		t.Fatal("bad node reported as success")
	}
}

func TestOPCUALoopbackPinnedSecureRead(t *testing.T) {
	endpoint, ns, params := localUAServer(t, true)
	params["node_id"] = ua.NewStringNodeID(ns.ID(), "Value").String()
	r := config.Request{Protocol: "opcua", Action: "read", Endpoint: endpoint, Timeout: "15s", Params: params}
	if e := Run(context.Background(), r, false, nil); e != nil {
		t.Fatal(e)
	}
	params["server_cert_sha256"] = strings.Repeat("00", 32)
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("wrong pinned identity connected")
	}
}

func TestOPCUACallWriteGateAndCancelledSubscription(t *testing.T) {
	if err := Run(context.Background(), config.Request{Protocol: "opcua", Action: "call", Endpoint: "opc.tcp://127.0.0.1:1"}, false, nil); err == nil || !strings.Contains(err.Error(), "write operation") {
		t.Fatalf("call confirmation missing: %v", err)
	}
	endpoint, ns, params := localUAServer(t, false)
	params["node_id"] = ua.NewStringNodeID(ns.ID(), "Value").String()
	params["max_events"] = 100
	params["interval_ms"] = 50
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	notified := false
	err := Run(ctx, config.Request{Protocol: "opcua", Action: "subscribe", Endpoint: endpoint, Timeout: "10s", Params: params}, false, func(event Event) {
		if event.Kind == "notification" {
			notified = true
			cancel()
		}
	})
	if !notified {
		t.Fatalf("no notification before cancellation: %v", err)
	}
	if err == nil {
		t.Fatal("subscription falsely reported completion after cancellation")
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("subscription cancellation/cleanup was not bounded")
	}
}

func TestOPCUAModernPoliciesOnEncryptedLoopback(t *testing.T) {
	endpoint, ns, params := localUAServer(t, true)
	params["node_id"] = ua.NewStringNodeID(ns.ID(), "Value").String()
	for _, policy := range []string{"Basic256Sha256", "Aes128_Sha256_RsaOaep", "Aes256_Sha256_RsaPss"} {
		params["security_policy"] = policy
		if e := Run(context.Background(), config.Request{Protocol: "opcua", Action: "read", Endpoint: endpoint, Timeout: "10s", Params: params}, false, nil); e != nil {
			t.Fatalf("%s: %v", policy, e)
		}
	}
	for _, policy := range []string{"Basic128Rsa15", "Basic256"} {
		params["security_policy"] = policy
		if _, _, e := opcuaOptions(config.Request{Params: params}, nil, "127.0.0.1"); e == nil || !strings.Contains(e.Error(), "allow_legacy_security") {
			t.Fatalf("legacy gate: %v", e)
		}
	}
}
