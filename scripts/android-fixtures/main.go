// android-fixtures is a disposable host-only test server. It is never imported by the APK.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
	"github.com/gopcua/opcua/uasc"
)

type counters struct{ discoveries, browses, reads, writes, calls atomic.Int64 }

func (c *counters) snapshot() map[string]int64 {
	return map[string]int64{"discoveries": c.discoveries.Load(), "browses": c.browses.Load(), "reads": c.reads.Load(), "writes": c.writes.Load(), "calls": c.calls.Load()}
}
func header(handle uint32) *ua.ResponseHeader {
	return &ua.ResponseHeader{Timestamp: time.Now(), RequestHandle: handle, ServiceResult: ua.StatusOK, ServiceDiagnostics: &ua.DiagnosticInfo{}}
}

func main() {
	ready := flag.String("ready", "android-evidence/opcua-fixture-ready.json", "Ready manifest path (host only)")
	opcPort := flag.Int("opc-port", 48410, "Loopback OPC UA port")
	metricsPort := flag.Int("metrics-port", 48411, "Loopback metrics HTTP port")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	extra, stopKafka, err := startKafkaFixture()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer stopKafka()
	network, stopNetwork, err := startMobileNetworkFixtures()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		stopKafka()
		os.Exit(1)
	}
	defer stopNetwork()
	if err := run(ctx, *ready, *opcPort, *metricsPort, extra, network); err != nil {
		fmt.Fprintln(os.Stderr, err)
		stopNetwork()
		stopKafka()
		os.Exit(1)
	}
}
func run(ctx context.Context, ready string, opcPort, metricsPort int, extras ...map[string]any) error {
	if opcPort < 1024 || opcPort > 65535 || metricsPort < 1024 || metricsPort > 65535 || opcPort == metricsPort {
		return fmt.Errorf("require distinct unprivileged loopback ports")
	}
	// No credentials or persistent trust are created; None is restricted to this disposable loopback fixture.
	srv := server.New(server.EndPoint("127.0.0.1", opcPort), server.EnableAuthMode(ua.UserTokenTypeAnonymous), server.EnableSecurity("None", ua.MessageSecurityModeNone))
	ns := server.NewNodeNameSpace(srv, "原生验收设备")
	root, _ := srv.Namespace(0)
	root.Objects().AddRef(ns.Objects(), id.Organizes, true)
	ns.Objects().AddRef(root.Objects(), id.Organizes, false)
	temperature := ns.AddNewVariableStringNode("Temperature", int32(7))
	pressure := ns.AddNewVariableStringNode("Pressure", float64(101.25))
	for _, node := range []*server.Node{temperature, pressure} {
		ns.Objects().AddRef(node, id.HasComponent, true)
		node.AddRef(ns.Objects(), id.HasComponent, false)
	}
	method := ns.AddNode(server.NewNode(ua.NewStringNodeID(ns.ID(), "Double"), server.Attributes{ua.AttributeIDNodeClass: server.DataValueFromValue(uint32(ua.NodeClassMethod)), ua.AttributeIDBrowseName: server.DataValueFromValue(&ua.QualifiedName{NamespaceIndex: ns.ID(), Name: "Double"}), ua.AttributeIDDisplayName: server.DataValueFromValue(ua.NewLocalizedText("数值翻倍")), ua.AttributeIDExecutable: server.DataValueFromValue(true), ua.AttributeIDUserExecutable: server.DataValueFromValue(true)}, nil, nil))
	ns.Objects().AddRef(method, id.HasComponent, true)
	method.AddRef(ns.Objects(), id.HasComponent, false)
	for _, name := range []string{"InputArguments", "OutputArguments"} {
		property := ns.AddNewVariableStringNode(name, []*ua.ExtensionObject{ua.NewExtensionObject(&ua.Argument{Name: "输入值", DataType: ua.NewNumericNodeID(0, 6), ValueRank: -1, Description: ua.NewLocalizedText("精确 Int32 测试参数")})})
		method.AddRef(property, id.HasProperty, true)
	}
	counts := &counters{}
	srv.RegisterHandler(id.GetEndpointsRequest_Encoding_DefaultBinary, func(_ *uasc.SecureChannel, r ua.Request, _ uint32) (ua.Response, error) {
		req := r.(*ua.GetEndpointsRequest)
		counts.discoveries.Add(1)
		return &ua.GetEndpointsResponse{ResponseHeader: header(req.RequestHeader.RequestHandle), Endpoints: srv.Endpoints()}, nil
	})
	srv.RegisterHandler(id.BrowseRequest_Encoding_DefaultBinary, func(_ *uasc.SecureChannel, r ua.Request, _ uint32) (ua.Response, error) {
		req := r.(*ua.BrowseRequest)
		counts.browses.Add(1)
		results := make([]*ua.BrowseResult, len(req.NodesToBrowse))
		for i, node := range req.NodesToBrowse {
			space, err := srv.Namespace(int(node.NodeID.Namespace()))
			if err != nil {
				results[i] = &ua.BrowseResult{StatusCode: ua.StatusBadNodeIDUnknown}
			} else {
				results[i] = space.Browse(node)
			}
		}
		return &ua.BrowseResponse{ResponseHeader: header(req.RequestHeader.RequestHandle), Results: results}, nil
	})
	srv.RegisterHandler(id.ReadRequest_Encoding_DefaultBinary, func(_ *uasc.SecureChannel, r ua.Request, _ uint32) (ua.Response, error) {
		req := r.(*ua.ReadRequest)
		counts.reads.Add(1)
		values := make([]*ua.DataValue, len(req.NodesToRead))
		for i, node := range req.NodesToRead {
			space, err := srv.Namespace(int(node.NodeID.Namespace()))
			if err != nil {
				values[i] = &ua.DataValue{EncodingMask: ua.DataValueStatusCode, Status: ua.StatusBadNodeIDUnknown}
			} else {
				values[i] = space.Attribute(node.NodeID, node.AttributeID)
			}
		}
		return &ua.ReadResponse{ResponseHeader: header(req.RequestHeader.RequestHandle), Results: values}, nil
	})
	srv.RegisterHandler(id.WriteRequest_Encoding_DefaultBinary, func(_ *uasc.SecureChannel, r ua.Request, _ uint32) (ua.Response, error) {
		req := r.(*ua.WriteRequest)
		counts.writes.Add(1)
		results := make([]ua.StatusCode, len(req.NodesToWrite))
		for i, node := range req.NodesToWrite {
			space, err := srv.Namespace(int(node.NodeID.Namespace()))
			if err != nil {
				results[i] = ua.StatusBadNodeIDUnknown
			} else {
				results[i] = space.SetAttribute(node.NodeID, node.AttributeID, node.Value)
			}
		}
		return &ua.WriteResponse{ResponseHeader: header(req.RequestHeader.RequestHandle), Results: results}, nil
	})
	srv.RegisterHandler(id.CallRequest_Encoding_DefaultBinary, func(_ *uasc.SecureChannel, r ua.Request, _ uint32) (ua.Response, error) {
		req := r.(*ua.CallRequest)
		counts.calls.Add(1)
		results := make([]*ua.CallMethodResult, len(req.MethodsToCall))
		for i, call := range req.MethodsToCall {
			result := &ua.CallMethodResult{StatusCode: ua.StatusBadInvalidArgument}
			if call.ObjectID.Equal(ns.Objects().ID()) && call.MethodID.Equal(method.ID()) && len(call.InputArguments) == 1 {
				if value, ok := call.InputArguments[0].Value().(int32); ok {
					result = &ua.CallMethodResult{StatusCode: ua.StatusOK, InputArgumentResults: []ua.StatusCode{ua.StatusOK}, OutputArguments: []*ua.Variant{ua.MustVariant(value * 2)}}
				}
			}
			results[i] = result
		}
		return &ua.CallResponse{ResponseHeader: header(req.RequestHeader.RequestHandle), Results: results}, nil
	})
	if err := srv.Start(ctx); err != nil {
		return err
	}
	defer srv.Close()
	manifest := map[string]any{"endpoint": fmt.Sprintf("opc.tcp://127.0.0.1:%d", opcPort), "metrics": fmt.Sprintf("http://127.0.0.1:%d/metrics", metricsPort), "namespace": ns.ID(), "object": ns.Objects().ID().String(), "temperature": temperature.ID().String(), "pressure": pressure.ID().String(), "method": method.ID().String(), "security_policy": "None", "security_mode": "None", "loopback_only": true}
	for _, extra := range extras {
		for key, value := range extra {
			manifest[key] = value
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(counts.snapshot())
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(manifest)
	})
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", metricsPort))
	if err != nil {
		return err
	}
	web := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = web.Serve(listener) }()
	defer web.Close()
	if err := os.MkdirAll(filepath.Dir(ready), 0700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(ready, body, 0600); err != nil {
		return err
	}
	defer os.Remove(ready)
	fmt.Printf("OPC fixture ready %s\n", body)
	<-ctx.Done()
	return nil
}
