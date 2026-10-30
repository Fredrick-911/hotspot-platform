// Package events is a tiny in-memory publish/subscribe hub. The dashboards use
// it for live updates (a new sale appears on the owner's screen without a
// refresh) over Server-Sent Events. It works inside ONE API process; running
// several API servers would need Redis or PostgreSQL LISTEN/NOTIFY instead.
package events

import (
	"encoding/json"
	"sync"
)

// Event is one message sent to browsers.
type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Topic names.
func TenantTopic(tenantID string) string { return "tenant:" + tenantID }
func PortalTopic(tenantID string) string { return "portal:" + tenantID }

const AdminTopic = "admin"

// Hub fans events out to subscribers.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]struct{}
}

// NewHub returns an empty Hub.
func NewHub() *Hub { return &Hub{subs: map[string]map[chan Event]struct{}{}} }

// Subscribe returns a channel of events for topic and a function that ends the
// subscription. The channel is buffered; a slow reader misses events instead of
// blocking publishers.
func (h *Hub) Subscribe(topic string) (<-chan Event, func()) {
	ch := make(chan Event, 16)
	h.mu.Lock()
	if h.subs[topic] == nil {
		h.subs[topic] = map[chan Event]struct{}{}
	}
	h.subs[topic][ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs[topic], ch)
			if len(h.subs[topic]) == 0 {
				delete(h.subs, topic)
			}
			h.mu.Unlock()
		})
	}
}

// Publish sends an event to everyone listening on the given topics.
func (h *Hub) Publish(typ string, data any, topics ...string) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	ev := Event{Type: typ, Data: raw}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, t := range topics {
		for ch := range h.subs[t] {
			select {
			case ch <- ev:
			default: // reader is too slow; drop rather than block a payment
			}
		}
	}
}

// Listeners returns how many browsers are subscribed to topic (for tests/metrics).
func (h *Hub) Listeners(topic string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[topic])
}
