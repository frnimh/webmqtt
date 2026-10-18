package bridge

import (
	"fmt"
	"sync"
	"testing"
)

func testEndpoints() []Endpoint {
	return []Endpoint{
		{Name: "publish", Path: "/publish", Method: "PUB", MQTT: EndpointMQTT{Topic: "test/topic", Payload: "configured payload"}},
		{Name: "latest", Path: "/latest", Method: "SUB", MQTT: EndpointMQTT{Topic: "test/topic", Last: 1}},
		{Name: "history", Path: "/history", Method: "SUB", MQTT: EndpointMQTT{Topic: "test/topic", Last: 3}},
	}
}

func TestHistoryRollingAndNonDestructive(t *testing.T) {
	h := NewHistory(testEndpoints())
	if got := h.Last("test/topic", 3); got == nil || len(got) != 0 {
		t.Fatalf("empty history = %v", got)
	}
	for _, value := range []string{"one", "two", "three", "four", "five"} {
		h.Add("test/topic", []byte(value))
	}
	for range 2 {
		messages := h.Last("test/topic", 3)
		if len(messages) != 3 {
			t.Fatalf("history: %+v", messages)
		}
		for i, want := range []string{"three", "four", "five"} {
			if messages[i].Payload != want || messages[i].ReceivedAt.IsZero() {
				t.Fatalf("message %d = %+v", i, messages[i])
			}
		}
		messages[0].Payload = "changed by reader"
	}
	if got := h.Last("test/topic", 1); got[0].Payload != "five" {
		t.Fatalf("latest = %+v", got)
	}
	h.Add("ignored/topic", []byte("ignored"))
	if len(h.Last("ignored/topic", 1)) != 0 {
		t.Fatal("unexpected topic stored")
	}
}

func TestHistoryConcurrent(t *testing.T) {
	h := NewHistory(testEndpoints())
	var wg sync.WaitGroup
	for n := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 100 {
				h.Add("test/topic", []byte(fmt.Sprint(n, i)))
				_ = h.Last("test/topic", 3)
			}
		}()
	}
	wg.Wait()
	if len(h.Last("test/topic", 100)) != 3 {
		t.Fatal("history exceeded capacity")
	}
}
