package ws

import (
	"sync"
	"testing"
	"time"
)

// Subscribe → Publish → Receive on the right topic only.
func TestHubBasicPubSub(t *testing.T) {
	h := NewHub()
	chA, unsubA := h.Subscribe("alpha")
	defer unsubA()
	chB, unsubB := h.Subscribe("beta")
	defer unsubB()

	h.PublishJSON("alpha", "hello", map[string]int{"n": 1})

	select {
	case ev := <-chA:
		if ev.Type != "hello" {
			t.Fatalf("got type=%q", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting on alpha")
	}

	select {
	case ev := <-chB:
		t.Fatalf("beta received an event meant for alpha: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// expected — silence on the wrong topic
	}
}

// Unsubscribe must close the channel and not crash on later Publish.
func TestHubUnsubscribe(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe("topic")
	unsub()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("channel was not closed after unsub")
	}

	// Should be a safe no-op.
	h.PublishJSON("topic", "x", nil)
}

// Concurrent publishers must not deadlock or race.
func TestHubConcurrentPublishers(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe("t")
	defer unsub()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h.PublishJSON("t", "ev", nil)
			}
		}()
	}

	// Drain in another goroutine so the bounded buffer doesn't stall.
	drained := make(chan int, 1)
	go func() {
		count := 0
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					drained <- count
					return
				}
				count++
			case <-time.After(200 * time.Millisecond):
				drained <- count
				return
			}
		}
	}()

	wg.Wait()
	// Give the drainer a moment to finish.
	time.Sleep(50 * time.Millisecond)
	got := <-drained
	if got == 0 {
		t.Fatal("expected at least some events to be delivered")
	}
}
