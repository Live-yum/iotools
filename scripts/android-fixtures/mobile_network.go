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
	var httpPosts atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var value map[string]string
			decoder := json.NewDecoder(io.LimitReader(r.Body, 65537))
			if decoder.Decode(&value) != nil || len(value) != 1 || value["operation"] != "aot-local-verify" {
				http.Error(w, "invalid synthetic operation", 400)
				return
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				http.Error(w, "invalid trailing payload", 400)
				return
			}
			httpPosts.Add(1)
		} else if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": "AOT HTTP 验收成功", "operation": r.Method})
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"modbus_reads": mb.reads.Load(), "modbus_writes": mb.writes.Load(), "http_posts": httpPosts.Load(), "mqtt_packets_received": atomic.LoadInt64(&broker.Info.PacketsReceived)})
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
	case 1, 2:
		if len(pdu) != 5 {
			return fail(3)
		}
		address, count := int(binary.BigEndian.Uint16(pdu[1:3])), int(binary.BigEndian.Uint16(pdu[3:5]))
		if count < 1 || count > 2000 || address+count > 65536 {
			return fail(2)
		}
		out := make([]byte, 2+(count+7)/8)
		out[0] = fc
		out[1] = byte((count + 7) / 8)
		for i := 0; i < count; i++ {
			if (address+i)%2 == 0 {
				out[2+i/8] |= 1 << uint(i%8)
			}
		}
		m.reads.Add(1)
		return out
	case 23:
		if len(pdu) < 10 {
			return fail(3)
		}
		ra, rc, wa, wc := int(binary.BigEndian.Uint16(pdu[1:3])), int(binary.BigEndian.Uint16(pdu[3:5])), int(binary.BigEndian.Uint16(pdu[5:7])), int(binary.BigEndian.Uint16(pdu[7:9]))
		if rc < 1 || rc > 125 || wc < 1 || wc > 121 || ra+rc > len(m.words) || wa+wc > len(m.words) || int(pdu[9]) != 2*wc || len(pdu) != 10+2*wc {
			return fail(3)
		}
		for i := 0; i < wc; i++ {
			m.words[wa+i] = binary.BigEndian.Uint16(pdu[10+2*i:])
		}
		out := make([]byte, 2+2*rc)
		out[0] = fc
		out[1] = byte(2 * rc)
		for i := 0; i < rc; i++ {
			binary.BigEndian.PutUint16(out[2+2*i:], m.words[ra+i])
		}
		m.reads.Add(1)
		m.writes.Add(1)
		return out
	case 43:
		if len(pdu) != 4 || pdu[1] != 14 || pdu[2] < 1 || pdu[2] > 4 {
			return fail(3)
		}
		objects := []string{"iotools fixture", "local-loopback", "1.0"}
		start := int(pdu[3])
		if start >= len(objects) {
			return fail(2)
		}
		count := len(objects) - start
		if pdu[2] == 4 {
			count = 1
		}
		out := []byte{43, 14, pdu[2], 0x81, 0, 0, byte(count)}
		for i := start; i < start+count; i++ {
			out = append(out, byte(i), byte(len(objects[i])))
			out = append(out, []byte(objects[i])...)
		}
		m.reads.Add(1)
		return out
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
