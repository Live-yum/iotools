package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Only the CI host links these disposable services. They never enter an APK.
func startMobileNetworkFixtures() (map[string]any, func(), error) {
	broker := mqtt.New(&mqtt.Options{InlineClient: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := broker.AddHook(new(auth.AllowHook), nil); err != nil {
		return nil, nil, err
	}
	listener := listeners.NewTCP(listeners.Config{ID: "flutter-local", Address: "127.0.0.1:48414"})
	if err := broker.AddListener(listener); err != nil {
		return nil, nil, err
	}
	if err := broker.Serve(); err != nil {
		return nil, nil, err
	}
	stopBroker := func() {
		listener.Close(func(_ string) {
			for _, client := range broker.Clients.GetAll() {
				client.Stop(nil)
			}
		})
		_ = broker.Close()
	}
	if err := broker.Publish("/sensors//temp/", []byte(`{"value":23,"unit":"°C","a.b":1,"a":{"b":2}}`), true, 1); err != nil {
		stopBroker()
		return nil, nil, err
	}
	mb, err := startModbusFixture("127.0.0.1:48415")
	if err != nil {
		stopBroker()
		return nil, nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"modbus_reads": mb.reads.Load(), "modbus_writes": mb.writes.Load()})
	})
	metrics, err := net.Listen("tcp", "127.0.0.1:48416")
	if err != nil {
		mb.close()
		stopBroker()
		return nil, nil, err
	}
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = httpServer.Serve(metrics) }()
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
		mb.close()
		stopBroker()
	}
	return map[string]any{"mqtt_endpoint": "mqtt://127.0.0.1:48414", "mqtt_retained_topic": "/sensors//temp/", "modbus_endpoint": "tcp://127.0.0.1:48415", "network_metrics_url": "http://127.0.0.1:48416/metrics"}, stop, nil
}

type modbusFixture struct {
	listener      net.Listener
	mu            sync.Mutex
	words         [256]uint16
	clients       map[net.Conn]bool
	stopped       bool
	wait          sync.WaitGroup
	reads, writes atomic.Int64
}

func startModbusFixture(address string) (*modbusFixture, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	m := &modbusFixture{listener: listener, clients: make(map[net.Conn]bool)}
	m.words[0] = 7
	m.words[1] = 42
	m.wait.Add(1)
	go func() {
		defer m.wait.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			m.mu.Lock()
			if m.stopped {
				m.mu.Unlock()
				_ = conn.Close()
				return
			}
			m.clients[conn] = true
			m.wait.Add(1)
			m.mu.Unlock()
			go m.serve(conn)
		}
	}()
	return m, nil
}
func (m *modbusFixture) close() {
	m.mu.Lock()
	m.stopped = true
	_ = m.listener.Close()
	for c := range m.clients {
		_ = c.Close()
	}
	m.mu.Unlock()
	m.wait.Wait()
}
func (m *modbusFixture) serve(conn net.Conn) {
	defer m.wait.Done()
	defer func() { _ = conn.Close(); m.mu.Lock(); delete(m.clients, conn); m.mu.Unlock() }()
	for {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		header := make([]byte, 7)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		length := int(binary.BigEndian.Uint16(header[4:6]))
		if binary.BigEndian.Uint16(header[2:4]) != 0 || length < 2 || length > 254 {
			return
		}
		pdu := make([]byte, length-1)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			return
		}
		answer := m.reply(header[6], pdu)
		binary.BigEndian.PutUint16(header[4:6], uint16(len(answer)+1))
		out := append(header, answer...)
		for len(out) > 0 {
			n, err := conn.Write(out)
			if err != nil {
				return
			}
			out = out[n:]
		}
	}
}
func (m *modbusFixture) reply(unit byte, pdu []byte) []byte {
	if len(pdu) == 0 {
		return []byte{0x80, 3}
	}
	fc := pdu[0]
	fail := func(code byte) []byte { return []byte{fc | 0x80, code} }
	if unit != 1 {
		return fail(11)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch fc {
	case 3, 4:
		if len(pdu) != 5 {
			return fail(3)
		}
		address, count := int(binary.BigEndian.Uint16(pdu[1:3])), int(binary.BigEndian.Uint16(pdu[3:5]))
		if count < 1 || count > 125 || address+count > len(m.words) {
			return fail(2)
		}
		out := make([]byte, 2+2*count)
		out[0] = fc
		out[1] = byte(count * 2)
		for i := 0; i < count; i++ {
			binary.BigEndian.PutUint16(out[2+2*i:], m.words[address+i])
		}
		m.reads.Add(1)
		return out
	case 6:
		if len(pdu) != 5 {
			return fail(3)
		}
		address := int(binary.BigEndian.Uint16(pdu[1:3]))
		if address >= len(m.words) {
			return fail(2)
		}
		m.words[address] = binary.BigEndian.Uint16(pdu[3:5])
		m.writes.Add(1)
		return append([]byte(nil), pdu...)
	case 16:
		if len(pdu) < 6 {
			return fail(3)
		}
		address, count := int(binary.BigEndian.Uint16(pdu[1:3])), int(binary.BigEndian.Uint16(pdu[3:5]))
		if count < 1 || count > 123 || address+count > len(m.words) || int(pdu[5]) != 2*count || len(pdu) != 6+2*count {
			return fail(3)
		}
		for i := 0; i < count; i++ {
			m.words[address+i] = binary.BigEndian.Uint16(pdu[6+2*i:])
		}
		m.writes.Add(1)
		return append([]byte(nil), pdu[:5]...)
	default:
		return fail(1)
	}
}
func (m *modbusFixture) endpoint() string { return fmt.Sprint(m.listener.Addr()) }
