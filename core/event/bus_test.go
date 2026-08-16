package event_test

import (
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestBus_MultipleSubscribersEachReceiveOneEvent(t *testing.T) {
	bus := event.NewInProcessBus()

	var gotA, gotB []event.Event
	bus.Subscribe("", func(e event.Event) { gotA = append(gotA, e) })
	bus.Subscribe("", func(e event.Event) { gotB = append(gotB, e) })

	published := event.Event{Type: event.EventCreated, Kind: resource.KindOrganization, ID: "org-1", Actor: "alice", At: time.Now()}
	bus.Publish(published)

	if len(gotA) != 1 || gotA[0] != published {
		t.Errorf("subscriber A got %+v, want exactly [%+v]", gotA, published)
	}
	if len(gotB) != 1 || gotB[0] != published {
		t.Errorf("subscriber B got %+v, want exactly [%+v]", gotB, published)
	}
}

func TestBus_Unsubscribe_StopsDelivery(t *testing.T) {
	bus := event.NewInProcessBus()

	var count int
	unsubscribe := bus.Subscribe("", func(event.Event) { count++ })

	bus.Publish(event.Event{Type: event.EventCreated, Kind: resource.KindOrganization, ID: "org-1"})
	if count != 1 {
		t.Fatalf("count after first publish = %d, want 1", count)
	}

	unsubscribe()
	bus.Publish(event.Event{Type: event.EventCreated, Kind: resource.KindOrganization, ID: "org-2"})
	if count != 1 {
		t.Errorf("count after unsubscribe + second publish = %d, want still 1", count)
	}

	// Calling unsubscribe again must be safe (no panic, no double-effect).
	unsubscribe()
}

func TestBus_Subscribe_FiltersByKind(t *testing.T) {
	bus := event.NewInProcessBus()

	var orgEvents, allEvents []event.Event
	bus.Subscribe(resource.KindOrganization, func(e event.Event) { orgEvents = append(orgEvents, e) })
	bus.Subscribe("", func(e event.Event) { allEvents = append(allEvents, e) })

	bus.Publish(event.Event{Type: event.EventCreated, Kind: resource.KindOrganization, ID: "org-1"})
	bus.Publish(event.Event{Type: event.EventCreated, Kind: resource.KindProject, ID: "project-1"})

	if len(orgEvents) != 1 {
		t.Errorf("kind-filtered subscriber got %d events, want 1 (Organization only)", len(orgEvents))
	}
	if len(allEvents) != 2 {
		t.Errorf("unfiltered subscriber got %d events, want 2 (both kinds)", len(allEvents))
	}
}

func TestBus_NoSubscribers_PublishDoesNotPanic(t *testing.T) {
	bus := event.NewInProcessBus()
	bus.Publish(event.Event{Type: event.EventDeleted, Kind: resource.KindOrganization, ID: "org-1"})
}
