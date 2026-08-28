package session

import "testing"

// A strip scrolled to its left end is a position, not a silence, and the wire
// has to carry the difference.
//
// gob omits a field holding its type's zero value, and it flattens a pointer to
// the thing pointed at, so a *int carrying 0 is encoded as nothing at all and
// decodes as nil - "this peer has not said". Home is the commonest place a
// strip is, so that would have made the one offset that matters most the one
// offset that never travels. A non-nil pointer to a struct is not elided, which
// is why ScrollStrip is one.
//
// NEGATIVE CONTROL: this test was written against a *int field and failed on
// exactly the case below, with the round trip returning nil for offset 0 while
// 51 and 65 travelled fine. It caught the bug in a two-client run first: the
// peer followed every step along the strip except the one that ended at home.
func TestScrollStripSurvivesTheWireAtHome(t *testing.T) {
	for _, offset := range []int{0, 1, 65} {
		state := &SessionState{Name: "wire", ScrollStrip: &ScrollStripState{ViewportX: offset}}
		data, err := gobCodec.Encode(state)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		var back SessionState
		if err := gobCodec.Decode(data, &back); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if back.ScrollStrip == nil {
			t.Fatalf("offset %d came back as a peer that had said nothing about its strip", offset)
		}
		if back.ScrollStrip.ViewportX != offset {
			t.Errorf("offset %d came back as %d", offset, back.ScrollStrip.ViewportX)
		}
	}

	// The other half: a peer that really has said nothing still decodes as nil,
	// so "keep the strip you have" stays reachable.
	data, err := gobCodec.Encode(&SessionState{Name: "wire"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var back SessionState
	if err := gobCodec.Decode(data, &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back.ScrollStrip != nil {
		t.Errorf("a state with no strip decoded as one at %d", back.ScrollStrip.ViewportX)
	}
}

// The offset is only meaningful beside the workspace it was measured on, so a
// stale snapshot that named a different workspace must not scroll the strip of
// the one the daemon is actually showing.
//
// NEGATIVE CONTROL: with the two lines that carry the daemon's strip over
// removed from reconcileStale, this fails saying workspace 1's offset was kept
// for workspace 2.
func TestReconcileStaleTakesTheStripWithTheWorkspace(t *testing.T) {
	live := func(string) bool { return true }

	// Same workspace: the client's own scroll stands.
	incoming := &SessionState{CurrentWorkspace: 1, ScrollStrip: &ScrollStripState{ViewportX: 40}}
	canonical := &SessionState{CurrentWorkspace: 1, ScrollStrip: &ScrollStripState{ViewportX: 0}}
	reconcileStale(incoming, canonical, live)
	if incoming.ScrollStrip == nil || incoming.ScrollStrip.ViewportX != 40 {
		t.Errorf("a scroll on the workspace being shown was overwritten: %+v", incoming.ScrollStrip)
	}

	// Another workspace: the daemon's travels with the workspace it belongs to.
	incoming = &SessionState{CurrentWorkspace: 1, ScrollStrip: &ScrollStripState{ViewportX: 40}}
	canonical = &SessionState{CurrentWorkspace: 2, ScrollStrip: &ScrollStripState{ViewportX: 7}}
	reconcileStale(incoming, canonical, live)
	if incoming.ScrollStrip == nil || incoming.ScrollStrip.ViewportX != 7 {
		t.Errorf("workspace 1's offset was kept for workspace 2: %+v", incoming.ScrollStrip)
	}
}

// The fingerprint is what decides whether a push is worth sending, so a scroll
// that moved nothing else has to change it.
//
// NEGATIVE CONTROL: with the strip left out of StateFingerprint, this fails on
// both counts, and internal/app's TestPeerAdoptsADeliberateScroll fails with
// the wheel scroll never leaving the client that made it.
func TestFingerprintSeesTheStrip(t *testing.T) {
	at := func(x int) *SessionState {
		return &SessionState{Name: "fp", ScrollStrip: &ScrollStripState{ViewportX: x}}
	}
	if StateFingerprint(at(0)) == StateFingerprint(at(40)) {
		t.Error("two strips at different offsets fingerprint the same, so a scroll is never pushed")
	}
	if StateFingerprint(at(0)) == StateFingerprint(&SessionState{Name: "fp"}) {
		t.Error("a strip at home fingerprints as a peer that has not said")
	}
	if StateFingerprint(at(40)) != StateFingerprint(at(40)) {
		t.Error("the same strip fingerprints differently")
	}
}
