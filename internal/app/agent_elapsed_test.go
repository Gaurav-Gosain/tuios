package app

import (
	"testing"
	"time"
)

// TestAgentElapsedFormat pins the three-cell budget and the states that stay
// blank. The agents section right-aligns this in place of a state word, so a
// four-cell string would push the name column.
func TestAgentElapsedFormat(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	at := func(d time.Duration) int64 { return now.Add(-d).UnixNano() }

	cases := []struct {
		name  string
		state string
		since time.Duration
		want  string
	}{
		{"fresh", "working", 5 * time.Second, "<1m"},
		{"minutes", "working", 7 * time.Minute, "7m"},
		{"last minute", "needs_input", 59 * time.Minute, "59m"},
		{"hours", "needs_input", 3 * time.Hour, "3h"},
		{"days", "errored", 50 * time.Hour, "2d"},
		{"idle stays blank", "idle", time.Hour, ""},
		{"no state", "", time.Hour, ""},
		// A daemon on a slightly ahead clock must not print a negative age.
		{"clock skew", "working", -2 * time.Minute, "<1m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := agentElapsed(tc.state, at(tc.since), now)
			if got != tc.want {
				t.Fatalf("agentElapsed(%q, -%v) = %q, want %q", tc.state, tc.since, got, tc.want)
			}
			if len(got) > 3 {
				t.Fatalf("elapsed %q is wider than the 3-cell budget", got)
			}
		})
	}

	if got := agentElapsed("working", 0, now); got != "" {
		t.Fatalf("an unstamped pane must show nothing, got %q", got)
	}
}

// TestAgentElapsedBucketIsMinuteGranular is the no-tick guard: the render cache
// keys on this bucket, so it must not change between two reads inside the same
// minute. A seconds-granular value would rebuild the whole rail every second
// that anything else drew a frame.
func TestAgentElapsedBucketIsMinuteGranular(t *testing.T) {
	stamp := time.Now().Add(-90 * time.Second).UnixNano()

	first := agentElapsedBucket(stamp)
	second := agentElapsedBucket(stamp)
	if first != second {
		t.Fatalf("bucket changed within the same minute: %d then %d", first, second)
	}
	if first != 1 {
		t.Fatalf("90s should read as minute bucket 1, got %d", first)
	}
	if got := agentElapsedBucket(0); got != 0 {
		t.Fatalf("an unstamped pane must fold a constant, got %d", got)
	}
}
