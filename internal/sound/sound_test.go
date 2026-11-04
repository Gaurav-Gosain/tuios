package sound

import (
	"testing"
	"time"
)

// TestCooldownCollapsesABurst is the anti-slot-machine guarantee. A workspace
// where several agents finish in the same instant has to make one sound, and
// the cue is short enough that overlapping plays would be indistinguishable
// noise rather than several notifications.
func TestCooldownCollapsesABurst(t *testing.T) {
	t.Cleanup(func() { lastAt.Store(0) })
	lastAt.Store(0)

	const cooldown = 3 * time.Second
	now := time.Now().UnixNano()

	accepted := 0
	for range 6 {
		if accept(now, cooldown) {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("a burst of 6 simultaneous alerts accepted %d cues, want 1", accepted)
	}

	// Still inside the window.
	if accept(now+int64(cooldown)-1, cooldown) {
		t.Error("a cue inside the cooldown was accepted")
	}
	// Past it, so two genuinely separate events are both heard.
	if !accept(now+int64(cooldown), cooldown) {
		t.Error("a cue past the cooldown was refused")
	}
}
