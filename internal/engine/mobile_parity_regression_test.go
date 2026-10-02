package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kmsg"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestModbusReadBitsSupportsFull2000BitWireRange(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var requests atomic.Int64
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			conn, e := listener.Accept()
			if e != nil {
				done <- e
				return
			}
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			header := make([]byte, 7)
			_, e = io.ReadFull(conn, header)
			if e != nil {
				_ = conn.Close()
				done <- e
				return
			}
			pdu := make([]byte, 5)
			_, e = io.ReadFull(conn, pdu)
			if e != nil {
				_ = conn.Close()
				done <- e
				return
			}
			if binary.BigEndian.Uint16(pdu[3:5]) != 2000 {
				_ = conn.Close()
				done <- io.ErrUnexpectedEOF
				return
			}
			requests.Add(1)
			answer := make([]byte, 252)
			answer[0] = pdu[0]
			answer[1] = 250
			for j := 2; j < len(answer); j++ {
				answer[j] = 0x55
			}
			binary.BigEndian.PutUint16(header[4:6], 253)
			_, e = conn.Write(append(header, answer...))
			_ = conn.Close()
			if e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	for _, action := range []string{"read-coils", "read-discrete"} {
		seen := false
		r := config.Request{Protocol: "modbus", Action: action, Endpoint: "tcp://" + listener.Addr().String(), Timeout: "2s", Params: map[string]any{"address": 0, "count": 2000, "unit": 1}}
		if err := Run(context.Background(), r, false, func(e Event) {
			if e.Kind == "bits" {
				bits := e.Data.(map[string]any)["values"].([]bool)
				seen = len(bits) == 2000 && bits[0] && !bits[1] && bits[1998] && !bits[1999]
			}
		}); err != nil {
			t.Fatal(err)
		}
		if !seen {
			t.Fatal("2000-bit response mismatch")
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatal("wire count mismatch")
	}
	for _, tc := range []struct {
		action         string
		count, address int
	}{{"read-coils", 2001, 0}, {"read-discrete", 2000, 65000}, {"read-holding", 126, 0}} {
		r := config.Request{Protocol: "modbus", Action: tc.action, Endpoint: "tcp://" + listener.Addr().String(), Timeout: "1s", Params: map[string]any{"address": tc.address, "count": tc.count, "unit": 1}}
		if err := Run(context.Background(), r, false, nil); err == nil || !(strings.Contains(err.Error(), "count must be an integer") || strings.Contains(err.Error(), "invalid address/count/unit")) {
			t.Fatalf("oversized range did not fail before network I/O: %v", err)
		}
	}
}
func TestKafkaLagSingularGroupNeverBroadensToAllGroups(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.ListenFn(func(network, address string) (net.Listener, error) {
		_, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		return net.Listen(network, net.JoinHostPort("127.0.0.1", port))
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()
	var mu sync.Mutex
	var described []string
	var listed int
	cluster.Control(func(request kmsg.Request) (kmsg.Response, error, bool) {
		mu.Lock()
		defer mu.Unlock()
		switch r := request.(type) {
		case *kmsg.ListGroupsRequest:
			listed++
		case *kmsg.DescribeGroupsRequest:
			described = append(described, r.Groups...)
		}
		return nil, nil, false
	})
	request := config.Request{Protocol: "kafka", Action: "lag", Endpoint: cluster.ListenAddrs()[0], Timeout: "3s", Params: map[string]any{"group": "only-this-group"}}
	if err := Run(context.Background(), request, false, nil); err != nil && !errors.Is(err, kerr.GroupIDNotFound) {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if listed != 0 || len(described) == 0 {
		t.Fatalf("expected selected group only; listed=%d described=%v", listed, described)
	}
	for _, group := range described {
		if group != "only-this-group" {
			t.Fatal("unexpected group")
		}
	}
}
