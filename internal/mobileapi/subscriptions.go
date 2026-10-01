package mobileapi

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/Live-yum/iotools/internal/engine"
)

type subscription struct {
	ID        string    `json:"id"`
	RequestID string    `json:"request_id"`
	Endpoint  string    `json:"endpoint"`
	NodeIDs   []string  `json:"node_ids"`
	Started   time.Time `json:"started"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	LastEvent any       `json:"last_event,omitempty"`
	cancel    context.CancelFunc
}

func (s *Session) startSubscription(c command, p preview) (any, error) {
	r, e := p.Collection.Resolve(p.Request, p.Profile)
	if e != nil {
		return nil, e
	}
	if e = s.prepareRequest(&r); e != nil {
		return nil, e
	}
	s.mu.Lock()
	if s.closed || s.paused || p.Revision != s.revision || time.Now().After(p.Expires) {
		s.mu.Unlock()
		return nil, errors.New("预览已失效，请重新预览")
	}
	if _, ok := s.previews[c.Token]; !ok {
		s.mu.Unlock()
		return nil, errors.New("预览已使用")
	}
	active := 0
	for _, sub := range s.subscriptions {
		if sub.cancel != nil {
			active++
		}
	}
	if active >= 16 {
		s.mu.Unlock()
		return nil, errors.New("最多同时运行16个独立订阅")
	}
	id, e := newToken()
	if e != nil {
		s.mu.Unlock()
		return nil, e
	}
	if len(s.subscriptions) >= 32 {
		for key, sub := range s.subscriptions {
			if sub.cancel == nil {
				delete(s.subscriptions, key)
			}
		}
	}
	nodes := r.Strings("node_ids")
	if len(nodes) == 0 {
		nodes = []string{r.String("node_id", "i=85")}
	}
	ctx, cancel := context.WithCancel(context.Background())
	sub := &subscription{ID: id, RequestID: r.ID, Endpoint: r.Endpoint, NodeIDs: nodes, Started: time.Now().UTC(), Status: "running", cancel: cancel}
	s.subscriptions[id] = sub
	delete(s.previews, c.Token)
	s.mu.Unlock()
	s.emit(id, "subscription.started", map[string]any{"subscription_id": id, "request_id": r.ID, "node_ids": nodes})
	go func() {
		var runErr error
		defer func() {
			if recover() != nil {
				runErr = errors.New("订阅引擎返回异常数据")
			}
			cancel()
			status := "completed"
			message := ""
			if runErr != nil {
				status = "failed"
				message = runErr.Error()
				if errors.Is(runErr, context.Canceled) {
					status = "cancelled"
				}
			}
			s.mu.Lock()
			sub.Status = status
			sub.Error = message
			sub.cancel = nil
			s.mu.Unlock()
			s.emit(id, "subscription.done", map[string]any{"subscription_id": id, "status": status, "error": message})
		}()
		runErr = engine.Run(ctx, r, false, func(ev engine.Event) {
			data := normalizeEvent(ev.Data)
			s.recordConnection(r, ev)
			if ev.Kind == "notification" {
				s.mu.Lock()
				sub.LastEvent = boundedView(data)
				s.mu.Unlock()
			}
			s.emit(id, "subscription.event", map[string]any{"subscription_id": id, "kind": ev.Kind, "data": data})
		})
	}()
	return map[string]any{"run_id": id, "subscription_id": id, "background": true}, nil
}
func (s *Session) listSubscriptions() []subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []subscription{}
	for _, sub := range s.subscriptions {
		out = append(out, *sub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}
func (s *Session) stopSubscriptions(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for key, sub := range s.subscriptions {
		if (id == "" || key == id) && sub.cancel != nil {
			sub.cancel()
			count++
		}
	}
	return count
}
