package sound

import (
	"os"
	"os/exec"
	"path/filepath"
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

// TestZeroCooldownAcceptsEverything keeps the opt-out honest: a user who sets
// the gap to zero has asked for every alert to be audible.
func TestZeroCooldownAcceptsEverything(t *testing.T) {
	t.Cleanup(func() { lastAt.Store(0) })
	now := time.Now().UnixNano()
	for i := range 3 {
		if !accept(now, 0) {
			t.Fatalf("request %d was refused with no cooldown set", i)
		}
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

// TestSpillWritesAPlayableFile checks the embedded cues survive the round trip
// to disk that a player needs, and that the second call reuses the file rather
// than writing one per alert.
func TestSpillWritesAPlayableFile(t *testing.T) {
	cache := map[Cue]string{}
	for _, c := range []Cue{CueDone, CueAttention} {
		path, err := spill(cache, c)
		if err != nil {
			t.Fatalf("spill: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })

		data, err := os.ReadFile(path) //nolint:gosec // a path this test just wrote
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
			t.Fatalf("cue %d is not a WAV: %d bytes, %q", c, len(data), data[:min(12, len(data))])
		}
		again, err := spill(cache, c)
		if err != nil || again != path {
			t.Fatalf("spill wrote a second file for cue %d: %q then %q (%v)", c, path, again, err)
		}
	}
}

// TestRunFallsThroughToAWorkingPlayer is the degradation path: a machine where
// the first player exists but cannot reach a device must still make a sound
// through the next one, and must remember which one worked.
func TestRunFallsThroughToAWorkingPlayer(t *testing.T) {
	fail, ok := lookFor(t, "false")
	if !ok {
		t.Skip("no false(1) on this machine")
	}
	pass, ok := lookFor(t, "true")
	if !ok {
		t.Skip("no true(1) on this machine")
	}
	players := []player{fail, fail, pass}

	played, best := run(players, 0, "ignored.wav")
	if !played {
		t.Fatal("run gave up while a working player was still on the list")
	}
	if best != 2 {
		t.Fatalf("run remembered player %d, want the one that worked (2)", best)
	}
	// The next play starts from the remembered one, so a box where four players
	// fail spawns four processes once rather than on every alert.
	if played, best = run(players, best, "ignored.wav"); !played || best != 2 {
		t.Fatalf("second run: played=%v best=%d, want true and 2", played, best)
	}
}

// TestRunReportsTotalFailure is what makes the subsystem switch itself off: no
// player on the list working is the signal that this machine has no audio.
func TestRunReportsTotalFailure(t *testing.T) {
	fail, ok := lookFor(t, "false")
	if !ok {
		t.Skip("no false(1) on this machine")
	}
	if played, _ := run([]player{fail, fail}, 0, "ignored.wav"); played {
		t.Fatal("run reported a play through two failing players")
	}
}

func lookFor(t *testing.T, name string) (player, bool) {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		return player{}, false
	}
	return player{path: path, argv: func(string) []string { return nil }}, true
}

// TestPlaysThroughARealAudioDevice is the only test that makes a noise, so it
// is opt-in. Everything else here proves the plumbing; this proves the machine
// at the end of it. Run it with:
//
//	TUIOS_SOUND_TEST=1 go test ./internal/sound/ -run RealAudio -v
func TestPlaysThroughARealAudioDevice(t *testing.T) {
	if os.Getenv("TUIOS_SOUND_TEST") == "" {
		t.Skip("set TUIOS_SOUND_TEST=1 to play the cues out loud")
	}
	players := resolvePlayers()
	if len(players) == 0 {
		t.Skip("no audio player on this machine")
	}
	t.Logf("resolved players: %v", playerNames(players))

	cache := map[Cue]string{}
	for _, c := range []Cue{CueDone, CueAttention} {
		path, err := spill(cache, c)
		if err != nil {
			t.Fatalf("spill: %v", err)
		}
		played, best := run(players, 0, path)
		if !played {
			t.Fatalf("no player would play cue %d", c)
		}
		t.Logf("cue %d played by %s", c, players[best].path)
		time.Sleep(700 * time.Millisecond)
	}
	_ = os.RemoveAll(filepath.Dir(cache[CueDone]))
}

func playerNames(ps []player) []string {
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, filepath.Base(p.path))
	}
	return names
}
