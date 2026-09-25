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

// TestPlayNeverBlocks is the property the Update goroutine depends on. Play is
// called from the frame loop, so it has to return whether or not anything is
// listening, whether or not the queue is full, and whether or not this machine
// has an audio device.
func TestPlayNeverBlocks(t *testing.T) {
	t.Setenv(DisableEnv, "1")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			Play(Request{Cue: CueAttention})
			Play(Request{Cue: CueDone, Cooldown: time.Hour})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Play blocked its caller")
	}
}
