package session

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// handoffCarriedUnexported names the unexported fields of streamEvent that
// handoffEvents carries beside the ring, since the ring's JSON drops them.
var handoffCarriedUnexported = map[string]bool{"relayed": true}

// TestHandoffCarriesEveryEventCounter fails when the event hub gains a
// last*Seq counter, or streamEvent an unexported field, that the handoff does
// not carry. A counter left behind comes back as 0 after a live upgrade, and a
// subscriber that resumes across it gets no gap marker for what it missed.
func TestHandoffCarriesEveryEventCounter(t *testing.T) {
	hub := reflect.TypeOf(eventHub{})
	carried := reflect.TypeOf(handoffEvents{})
	for i := range hub.NumField() {
		name := hub.Field(i).Name
		if !strings.HasPrefix(name, "last") || !strings.HasSuffix(name, "Seq") {
			continue
		}
		want := "L" + name[1:]
		if _, ok := carried.FieldByName(want); !ok {
			t.Errorf("eventHub.%s has no handoffEvents.%s, so a live upgrade resets it to 0", name, want)
		}
	}
	ev := reflect.TypeOf(streamEvent{})
	for i := range ev.NumField() {
		if f := ev.Field(i); !f.IsExported() && !handoffCarriedUnexported[f.Name] {
			t.Errorf("streamEvent.%s is unexported, so the ring's JSON drops it in a live upgrade; carry it in handoffEvents", f.Name)
		}
	}
}

// TestHandoffKeepsRelayedEventsAndNotices puts a hub and a bus through the
// JSON the manifest writes, and checks what JSON alone would lose.
func TestHandoffKeepsRelayedEventsAndNotices(t *testing.T) {
	old := newEventHubSize(4)
	old.ring[0] = streamEvent{Seq: 5, Type: EventAgentState, relayed: true}
	old.ring[1] = streamEvent{Seq: 6, Type: EventAgentState}
	old.ringLen = 2
	old.seq, old.lastTMLSeq = 6, 4

	var events handoffEvents
	roundTripJSON(t, old.handoffState(), &events)
	hub := newEventHubSize(4)
	hub.restore(events)
	if hub.lastTMLSeq != 4 {
		t.Errorf("lastTMLSeq = %d after the handoff, want 4", hub.lastTMLSeq)
	}
	if !hub.ring[0].relayed || hub.ring[1].relayed {
		t.Errorf("relayed = %v, %v after the handoff, want true, false", hub.ring[0].relayed, hub.ring[1].relayed)
	}

	until := time.Now().Add(time.Minute).Round(0).UTC()
	bus := newAgentBus()
	bus.leaseSeq = 3
	bus.noticed = map[string]noticeMark{
		"pane-a": {id: 9, session: "s"},
		"pane-b": {id: 2, session: "s", lease: 3, leaseTo: 4, leaseUntil: until},
	}
	var mail handoffMail
	roundTripJSON(t, bus.handoffState(), &mail)
	next := newAgentBus()
	next.restore(mail)
	if !reflect.DeepEqual(next.noticed, bus.noticed) || next.leaseSeq != 3 {
		t.Errorf("notices after the handoff = %+v (lease seq %d), want %+v (3)", next.noticed, next.leaseSeq, bus.noticed)
	}
	if !next.commitNotice("pane-b", 3) {
		t.Error("a hook's lease from the old image does not commit on the new one")
	}
}

func roundTripJSON(t *testing.T, in, out any) {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}
