// Package ws is a tiny in-process pub/sub for WebSocket fan-out.
//
// We deliberately do NOT use Redis Pub/Sub here. The api-gateway is the
// only WebSocket terminator (single binary, single process), so the simpler
// path is direct in-memory channels. If we ever scale to multiple gateway
// pods we'll add a Redis Pub/Sub bridge — but until then, less is more.
//
// Topics are free-form strings keyed by the WebSocket route:
//   - "bootstrap"          → /ws/bootstrap
//   - "bg-jobs"            → /ws/bg-jobs
//   - "queue"              → /ws/queue
//   - "training/{id}"      → /ws/training/{id}
//
// Publishers call Hub.Publish; subscribers call Hub.Subscribe and read
// from the returned channel until they're done.
package ws

import (
	"encoding/json"
	"sync"
)

type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type subscriber struct {
	ch    chan Event
	topic string
}

type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[*subscriber]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[string]map[*subscriber]struct{}{}}
}

// Subscribe registers a buffered channel for a topic. Buffer size is
// intentional: a slow client should not block publishers. If the buffer
// fills up, the dropped event is the client's problem — we log it via
// the unsubscribe path.
func (h *Hub) Subscribe(topic string) (<-chan Event, func()) {
	s := &subscriber{ch: make(chan Event, 32), topic: topic}

	h.mu.Lock()
	if h.subs[topic] == nil {
		h.subs[topic] = map[*subscriber]struct{}{}
	}
	h.subs[topic][s] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		if set, ok := h.subs[topic]; ok {
			delete(set, s)
			if len(set) == 0 {
				delete(h.subs, topic)
			}
		}
		h.mu.Unlock()
		close(s.ch)
	}
	return s.ch, unsubscribe
}

// Publish fans out an event to every subscriber on the topic.
// Drops are silent (channel full) — preferable to back-pressure.
func (h *Hub) Publish(topic string, event Event) {
	h.mu.RLock()
	set := h.subs[topic]
	subs := make([]*subscriber, 0, len(set))
	for s := range set {
		subs = append(subs, s)
	}
	h.mu.RUnlock()

	for _, s := range subs {
		select {
		case s.ch <- event:
		default:
			// Receiver is slow; drop this event for them only.
		}
	}
}

// PublishJSON is a convenience for the common case of a JSON payload.
func (h *Hub) PublishJSON(topic, eventType string, payload any) {
	data, _ := json.Marshal(payload)
	h.Publish(topic, Event{Type: eventType, Data: data})
}
