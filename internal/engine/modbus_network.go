package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ModbusDiscoveryPlan describes explicit TCP-connect or IPv4 ICMP probes.
// A reachable host or open port is not proof of a Modbus device.
type ModbusDiscoveryPlan struct {
	Method                       string
	Targets                      []string
	Port, TimeoutMS, Concurrency int
}
type ModbusDiscoveryResult struct {
	Address          string
	Open             bool
	Error            string
	Completed, Total int
}

// PrepareModbusDiscovery expands an explicit literal list or an aligned IPv4
// prefix /24..32. /24..30 omit network/broadcast; /31 and /32 include all hosts.
func PrepareModbusDiscovery(target string, port, timeout, concurrency int) (ModbusDiscoveryPlan, error) {
	p := ModbusDiscoveryPlan{Port: port, TimeoutMS: timeout, Concurrency: concurrency}
	if len(target) == 0 || len(target) > 16384 || port < 1 || port > 65535 || timeout < 100 || timeout > 2000 || concurrency < 1 || concurrency > 32 {
		return p, fmt.Errorf("目标/端口/超时/并发超界 (端口1..65535，超时100..2000ms，并发1..32)")
	}
	seen := map[netip.Addr]bool{}
	add := func(s string) error {
		a, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil || a.Zone() != "" || a.IsUnspecified() || a.IsMulticast() || a == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
			return fmt.Errorf("目标必须是明确单播IP，不接受主机名、范围猜测或zone")
		}
		a = a.Unmap()
		if seen[a] {
			return fmt.Errorf("目标IP重复")
		}
		seen[a] = true
		p.Targets = append(p.Targets, a.String())
		if len(p.Targets) > 254 {
			return fmt.Errorf("最多254个明确目标")
		}
		return nil
	}
	if strings.Contains(target, "/") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(target))
		if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 24 || prefix != prefix.Masked() {
			return p, fmt.Errorf("网段需对齐IPv4 /24..32；不会自动扩大输入范围")
		}
		count := 1 << uint(32-prefix.Bits())
		a := prefix.Addr()
		for n := 0; n < count; n++ {
			if prefix.Bits() >= 31 || (n > 0 && n < count-1) {
				if err = add(a.String()); err != nil {
					return p, err
				}
			}
			a = a.Next()
		}
	} else {
		for _, s := range strings.Split(target, ",") {
			if err := add(s); err != nil {
				return p, err
			}
		}
	}
	sort.Slice(p.Targets, func(i, j int) bool {
		a, _ := netip.ParseAddr(p.Targets[i])
		b, _ := netip.ParseAddr(p.Targets[j])
		return a.Less(b)
	})
	return p, nil
}
func DiscoverModbusTCP(ctx context.Context, plan ModbusDiscoveryPlan, emit func(ModbusDiscoveryResult)) error {
	return discoverModbusTCP(ctx, plan, (&net.Dialer{}).DialContext, emit)
}
func discoverModbusTCP(ctx context.Context, plan ModbusDiscoveryPlan, dial func(context.Context, string, string) (net.Conn, error), emit func(ModbusDiscoveryResult)) error {
	if plan.Method != "" && plan.Method != "tcp" {
		return fmt.Errorf("TCP discovery cannot execute a different method")
	}
	checked, err := PrepareModbusDiscoveryMethod(strings.Join(plan.Targets, ","), plan.Port, plan.TimeoutMS, plan.Concurrency, "tcp")
	if err != nil {
		return err
	}
	return discoverModbusTargets(ctx, checked, func(ctx context.Context, target string) error {
		c, e := dial(ctx, "tcp", net.JoinHostPort(target, strconv.Itoa(checked.Port)))
		if c != nil {
			_ = c.Close()
		}
		return e
	}, emit)
}
func stringsJoinTargets(targets []string) string { return strings.Join(targets, ",") }
func discoverModbusTargets(ctx context.Context, plan ModbusDiscoveryPlan, probeTarget func(context.Context, string) error, emit func(ModbusDiscoveryResult)) error {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	ctx, stopAll := context.WithCancelCause(ctx)
	defer stopAll(nil)
	jobs := make(chan string)
	results := make(chan ModbusDiscoveryResult, plan.Concurrency)
	var wg sync.WaitGroup
	for n := 0; n < plan.Concurrency; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range jobs {
				if ctx.Err() != nil {
					return
				}
				probe, stop := context.WithTimeout(ctx, time.Duration(plan.TimeoutMS)*time.Millisecond)
				e := probeTarget(probe, a)
				stop()
				if errors.Is(e, ErrModbusICMPUnavailable) {
					stopAll(e)
					return
				}
				r := ModbusDiscoveryResult{Address: a, Open: e == nil, Total: len(plan.Targets)}
				if e != nil {
					r.Error = ModbusErrorClass(e)
					if plan.Method == "ping" {
						r.Error = fmt.Sprintf("%.256s", e.Error())
					}
				}
				select {
				case results <- r:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, a := range plan.Targets {
			select {
			case jobs <- a:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	completed := 0
	for r := range results {
		completed++
		r.Completed = completed
		if emit != nil {
			emit(r)
		}
	}
	return context.Cause(ctx)
}
