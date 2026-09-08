package engine

import (
	"slices"
	"sync"
)

type streamPacket struct {
	session         *liveSession
	events          []Event
	reset           bool
	finish          bool
	discardRequests bool
	ack             chan struct{}
}

// Only run owns and closes output. Its queue is lossless even when no UI
// consumer is ready; the small inbox is not a lossy event buffer.
type eventStream struct {
	inbox  chan streamPacket
	output chan Event
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

func newEventStream() *eventStream {
	s := &eventStream{
		inbox: make(chan streamPacket, 32), output: make(chan Event),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *eventStream) push(session *liveSession, events ...Event) {
	if len(events) == 0 {
		return
	}
	select {
	case s.inbox <- streamPacket{session: session, events: events}:
	case <-s.stop:
	case <-s.done:
	}
}

func (s *eventStream) activate(session *liveSession) {
	s.control(streamPacket{session: session, reset: true})
}

func (s *eventStream) discardRequests(session *liveSession) {
	s.control(streamPacket{session: session, discardRequests: true})
}

func (s *eventStream) control(packet streamPacket) {
	packet.ack = make(chan struct{})
	select {
	case s.inbox <- packet:
		select {
		case <-packet.ack:
		case <-s.stop:
		case <-s.done:
		}
	case <-s.stop:
	case <-s.done:
	}
}

func (s *eventStream) close() {
	s.once.Do(func() { close(s.stop) })
	<-s.done
}

func (s *eventStream) fail(event Event) {
	select {
	case s.inbox <- streamPacket{events: []Event{event}, finish: true}:
	case <-s.stop:
	case <-s.done:
	}
}

func (s *eventStream) run() {
	defer close(s.done)
	defer close(s.output)
	var active *liveSession
	var queue []Event
	finishing := false
	for {
		if finishing && len(queue) == 0 {
			return
		}
		var output chan Event
		var next Event
		if len(queue) != 0 {
			output, next = s.output, queue[0]
		}
		select {
		case <-s.stop:
			return
		case packet := <-s.inbox:
			if finishing {
				if packet.ack != nil {
					close(packet.ack)
				}
			} else if packet.finish {
				queue = append(queue, packet.events...)
				finishing = true
			} else if packet.reset {
				active, queue = packet.session, nil
				close(packet.ack)
			} else if packet.discardRequests {
				if packet.session == active {
					queue = slices.DeleteFunc(queue, func(event Event) bool {
						return event.Kind == EventPermission || event.Kind == EventQuestion
					})
				}
				close(packet.ack)
			} else if packet.session == active {
				queue = append(queue, packet.events...)
			}
		case output <- next:
			queue[0] = Event{}
			queue = queue[1:]
			if len(queue) == 0 {
				queue = nil
			}
		}
	}
}
