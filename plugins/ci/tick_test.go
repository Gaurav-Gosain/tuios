package main

import (
	"testing"
	"time"
)

// TestOldTickStartsNoSecondClock: a tick that the repository before armed
// arrives after a switch, while the new repository runs its own clock. It
// must do nothing. When it armed a clock again, each switch added one more
// clock, and each woke the plugin once a second for as long as the build
// ran.
//
// How it could pass wrongly, written down first:
//   - The new clock could be the one that is dropped: the tick of the new
//     generation must still arm the next one.
//
// NEGATIVE CONTROL: without the generation check in the tickMsg case, the
// old tick returns a command and the test fails.
func TestOldTickStartsNoSecondClock(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	m := newModel(nil, "acme/shop", "")
	m.now = func() time.Time { return now }
	running := func() {
		m.list = []Build{{Key: "a", State: "running", Started: now.Add(-time.Minute)}}
		m.key = "a"
	}
	running()
	if m.tick() == nil {
		t.Fatal("a running build armed no clock")
	}
	old := m.tickGen

	m.setRepo("acme/shop")
	running()
	if m.tick() == nil {
		t.Fatal("the running build after the switch armed no clock")
	}
	if _, cmd := m.update(tickMsg{gen: old}); cmd != nil {
		t.Errorf("ASSERTION: the tick of the repository before armed a second clock")
	}
	if _, cmd := m.update(tickMsg{gen: m.tickGen}); cmd == nil {
		t.Errorf("ASSERTION: the tick of the new clock did not arm the next one")
	}
}
