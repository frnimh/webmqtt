package bridge

import (
	"sync"
	"time"
)

type Message struct {
	Topic      string    `json:"topic"`
	Payload    string    `json:"payload"`
	ReceivedAt time.Time `json:"received_at"`
}

type messageRing struct {
	items []Message
	next  int
	full  bool
}

// History keeps a bounded, non-destructive history for each subscribed topic.
type History struct {
	mu     sync.RWMutex
	topics map[string]*messageRing
}

func NewHistory(endpoints []Endpoint) *History {
	capacities := make(map[string]int)
	for _, e := range endpoints {
		if e.Method == "SUB" && e.MQTT.Last > capacities[e.MQTT.Topic] {
			capacities[e.MQTT.Topic] = e.MQTT.Last
		}
	}
	h := &History{topics: make(map[string]*messageRing, len(capacities))}
	for topic, capacity := range capacities {
		h.topics[topic] = &messageRing{items: make([]Message, capacity)}
	}
	return h
}

func (h *History) Add(topic string, payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.topics[topic]
	if r == nil {
		return
	}
	r.items[r.next] = Message{Topic: topic, Payload: string(payload), ReceivedAt: time.Now().UTC()}
	r.next = (r.next + 1) % len(r.items)
	if r.next == 0 {
		r.full = true
	}
}

func (h *History) Last(topic string, limit int) []Message {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]Message, 0)
	r := h.topics[topic]
	if r == nil || limit <= 0 {
		return result
	}
	count := r.next
	if r.full {
		count = len(r.items)
	}
	if limit > count {
		limit = count
	}
	for i := limit; i > 0; i-- {
		index := (r.next - i + len(r.items)) % len(r.items)
		result = append(result, r.items[index])
	}
	return result
}
