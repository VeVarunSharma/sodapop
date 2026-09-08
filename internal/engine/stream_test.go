package engine

import (
	"fmt"
	"sync"
	"testing"
)

func TestEventQueueIsLosslessUnderBackpressure(t *testing.T) {
	stream := newEventStream()
	t.Cleanup(stream.close)
	session := &liveSession{}
	stream.activate(session)
	for i := range 3000 {
		stream.push(session, Event{Kind: EventPermission, ID: fmt.Sprint(i)})
	}
	for i := range 3000 {
		event := receive(t, stream.output)
		if event.Kind != EventPermission || event.ID != fmt.Sprint(i) {
			t.Fatalf("lost or reordered critical event %d: %+v", i, event)
		}
	}
}

func TestEventQueueClosesSafelyWithConcurrentProducers(t *testing.T) {
	stream := newEventStream()
	session := &liveSession{}
	stream.activate(session)
	var producers sync.WaitGroup
	for range 20 {
		producers.Go(func() {
			for range 200 {
				stream.push(session, Event{Kind: EventDelta})
			}
		})
	}
	stream.close()
	producers.Wait()
	stream.close()
	if _, open := <-stream.output; open {
		t.Fatal("output remained open")
	}
}

func TestEventQueueDropsOldScopeOnlyOnExplicitSwitch(t *testing.T) {
	stream := newEventStream()
	t.Cleanup(stream.close)
	old, current := &liveSession{}, &liveSession{}
	stream.activate(old)
	for range 10 {
		stream.push(old, Event{ID: "old"})
	}
	stream.activate(current)
	stream.push(old, Event{ID: "late-old"})
	stream.push(current, Event{ID: "current"})
	if event := receive(t, stream.output); event.ID != "current" {
		t.Fatalf("old scope leaked: %+v", event)
	}
}
