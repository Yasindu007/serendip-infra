package event

import (
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// EventType is the kind of resource lifecycle change an Event describes.
type EventType string

const (
	EventCreated EventType = "created"
	EventUpdated EventType = "updated"
	EventDeleted EventType = "deleted"
)

// Event is published after a resource mutation's transaction commits --
// never before, so a rolled-back mutation is never observed as an event.
type Event struct {
	Type  EventType
	Kind  resource.Kind
	ID    string
	Actor string
	At    time.Time
}

// Bus is the in-process publish/subscribe mechanism for resource
// lifecycle events (core/store/sqlite publishes post-commit).
type Bus interface {
	// Subscribe registers handler for events of the given kind, or every
	// kind if kind is "". The returned unsubscribe stops delivery; safe
	// to call more than once.
	Subscribe(kind resource.Kind, handler func(Event)) (unsubscribe func())
	Publish(e Event)
}

// InProcessBus is the default Bus: synchronous, single-process, no
// persistence or retry. Handlers run on the publishing goroutine, so they
// must be fast and non-blocking -- this is spec 001's whole scope for
// core/event; anything heavier (durable delivery, async dispatch) is a
// later spec's problem if it turns out to be needed.
type InProcessBus struct {
	mu          sync.RWMutex
	nextID      int
	subscribers map[int]subscription
}

type subscription struct {
	kind    resource.Kind // "" means every kind
	handler func(Event)
}

func NewInProcessBus() *InProcessBus {
	return &InProcessBus{subscribers: make(map[int]subscription)}
}

var _ Bus = (*InProcessBus)(nil)

func (b *InProcessBus) Subscribe(kind resource.Kind, handler func(Event)) func() {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subscribers[id] = subscription{kind: kind, handler: handler}
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subscribers, id)
			b.mu.Unlock()
		})
	}
}

func (b *InProcessBus) Publish(e Event) {
	// Copy matching handlers out under RLock, then run them unlocked --
	// a handler that calls Subscribe/Unsubscribe/Publish on this same bus
	// must not deadlock against its own publisher.
	b.mu.RLock()
	handlers := make([]func(Event), 0, len(b.subscribers))
	for _, sub := range b.subscribers {
		if sub.kind == "" || sub.kind == e.Kind {
			handlers = append(handlers, sub.handler)
		}
	}
	b.mu.RUnlock()

	for _, h := range handlers {
		h(e)
	}
}
