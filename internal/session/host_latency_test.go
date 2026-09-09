package session

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// TestKeystrokeRoundTripThroughAHost measures what a keystroke costs across
// the relay, against the same keystroke on a local attach, with no network in
// between. The far daemon runs cat, so the echo is the PTY's own and nothing
// else is on the path. It logs the medians; a real link adds one network
// round trip on top of the through-host number.
func TestKeystrokeRoundTripThroughAHost(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	hub, far := startHubAndFar(t)
	waitForHostUp(t, hub, "build")

	catSession := func(d *Daemon, name string) string {
		sess, err := d.manager.CreateSession(name, &SessionConfig{}, 80, 24)
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if _, err := sess.AddDaemonWindowWith(NewWindowOptions{Title: "cat", Command: []string{"cat"}, Focus: true}, nil); err != nil {
			t.Fatalf("AddDaemonWindowWith: %v", err)
		}
		return sess.GetState().Windows[0].PTYID
	}

	measure := func(c *TUIClient, ptyID string, rounds int) time.Duration {
		var mu sync.Mutex
		var got strings.Builder
		if err := c.SubscribePTY(ptyID, 0, true, func(b []byte) {
			mu.Lock()
			got.Write(b)
			mu.Unlock()
		}); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		times := make([]time.Duration, 0, rounds)
		for i := range rounds {
			marker := string(rune('a' + i%26))
			mu.Lock()
			got.Reset()
			mu.Unlock()
			start := time.Now()
			if err := c.WritePTY(ptyID, []byte(marker)); err != nil {
				t.Fatalf("write: %v", err)
			}
			for {
				mu.Lock()
				seen := strings.Contains(got.String(), marker)
				mu.Unlock()
				if seen {
					break
				}
				if time.Since(start) > 5*time.Second {
					t.Fatalf("no echo for %q after 5s", marker)
				}
				time.Sleep(20 * time.Microsecond)
			}
			times = append(times, time.Since(start))
		}
		// Median, so a warm-up outlier does not decide the number.
		for i := 1; i < len(times); i++ {
			for j := i; j > 0 && times[j] < times[j-1]; j-- {
				times[j], times[j-1] = times[j-1], times[j]
			}
		}
		return times[len(times)/2]
	}

	localPTY := catSession(hub, "local-cat")
	local := NewTUIClient()
	if err := local.Connect("test", 80, 24); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = local.Close() }()
	if _, err := local.AttachSession("local-cat", false, 80, 24); err != nil {
		t.Fatalf("attach: %v", err)
	}
	local.StartReadLoop()

	farPTY := catSession(far.daemon, "far-cat")
	through, _ := connectThrough(t, "build", "far-cat")

	const rounds = 200
	localMedian := measure(local, localPTY, rounds)
	throughMedian := measure(through, farPTY, rounds)
	t.Logf("keystroke to echo, median of %d: local %v, through a host %v (no network in between)", rounds, localMedian, throughMedian)
	if throughMedian > 50*time.Millisecond {
		t.Errorf("a keystroke through the relay took %v; something on the path is sleeping", throughMedian)
	}
}
