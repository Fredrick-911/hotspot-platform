package events

import (
	"encoding/json"
	"testing"
	"time"
)

func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("no event received")
		return Event{}
	}
}

func TestPublishReachesOnlyMatchingTopics(t *testing.T) {
	h := NewHub()
	a, cancelA := h.Subscribe(TenantTopic("a"))
	defer cancelA()
	b, cancelB := h.Subscribe(TenantTopic("b"))
	defer cancelB()
	admin, cancelAdmin := h.Subscribe(AdminTopic)
	defer cancelAdmin()

	h.Publish("sale", map[string]int{"amount": 50}, TenantTopic("a"), AdminTopic)

	ev := recv(t, a)
	var data map[string]int
	if err := json.Unmarshal(ev.Data, &data); err != nil || ev.Type != "sale" || data["amount"] != 50 {
		t.Fatalf("event = %+v (%v)", ev, err)
	}
	recv(t, admin)

	select {
	case ev := <-b:
		t.Fatalf("tenant b must not see tenant a's sale, got %+v", ev)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestCancelStopsDeliveryAndPublishNeverBlocks(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("t")
	if h.Listeners("t") != 1 {
		t.Fatal("listener not counted")
	}
	cancel()
	cancel() // calling twice is safe
	if h.Listeners("t") != 0 {
		t.Fatal("listener not removed")
	}
	h.Publish("x", 1, "t")
	select {
	case <-ch:
		t.Fatal("received after cancel")
	default:
	}

	// A subscriber that never reads must not stall the publisher.
	_, cancelSlow := h.Subscribe("slow")
	defer cancelSlow()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			h.Publish("x", i, "slow")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
}
