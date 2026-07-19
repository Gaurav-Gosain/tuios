package session

import (
	"strings"
	"testing"
	"time"
)

// TestClosestMatch_LongTargetIsCheap checks that the did-you-mean hint costs
// the same for a huge verb name as for a plausible one.
//
// The request line limit is 16 MiB and anything that can open the daemon socket
// can send one. closestMatch used to run a full Levenshtein comparison against
// every registered verb, so a 16 MiB name burned about five seconds of CPU on
// the connection's own goroutine, every request, for a suggestion that was
// always empty. The distance is at least the length difference, so a candidate
// that far away can never win and must never be compared.
func TestClosestMatch_LongTargetIsCheap(t *testing.T) {
	known := knownVerbNames()
	if len(known) == 0 {
		t.Fatal("no verbs registered")
	}

	for _, size := range []int{1 << 16, 1 << 20, 4 << 20, 16 << 20} {
		target := strings.Repeat("a", size)

		done := make(chan string, 1)
		start := time.Now()
		go func() { done <- closestMatch(target, known) }()

		select {
		case got := <-done:
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("closestMatch on a %d-byte verb took %v", size, elapsed)
			}
			if got != "" {
				t.Errorf("closestMatch on a %d-byte verb suggested %q", size, got)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("closestMatch on a %d-byte verb did not return within 10s", size)
		}
	}
}

// TestClosestMatch_StillSuggests checks the length guard did not cost the hint
// its actual job.
func TestClosestMatch_StillSuggests(t *testing.T) {
	known := knownVerbNames()

	// Build a typo from a real verb so the test does not hard-code a verb name
	// that may be renamed later.
	var base string
	for _, n := range known {
		if len(n) >= 8 {
			base = n
			break
		}
	}
	if base == "" {
		t.Skip("no verb long enough to build a typo from")
	}

	tests := []struct {
		name   string
		target string
		want   string
	}{
		{"dropped character", base[:len(base)-1], base},
		{"transposed tail", base[:len(base)-2] + string(base[len(base)-1]) + string(base[len(base)-2]), base},
		{"exact match is not a suggestion", base, ""},
		{"nothing close", strings.Repeat("q", len(base)), ""},
		{"empty", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := closestMatch(tc.target, known); got != tc.want {
				t.Errorf("closestMatch(%q) = %q, want %q", tc.target, got, tc.want)
			}
		})
	}
}

// TestEchoName_BoundsTheResponse checks that a caller-supplied name echoed into
// an error message cannot make the response scale with the request.
func TestEchoName_BoundsTheResponse(t *testing.T) {
	if got := echoName("list-verbs"); got != "list-verbs" {
		t.Errorf("echoName truncated a short name: %q", got)
	}

	huge := strings.Repeat("a", 16<<20)
	got := echoName(huge)
	if len(got) > maxEchoedName+64 {
		t.Errorf("echoName returned %d bytes for a %d-byte name", len(got), len(huge))
	}
	if !strings.HasPrefix(got, "aaaa") {
		t.Errorf("echoName dropped the prefix the caller sent: %q", got)
	}
	if !strings.Contains(got, "16777216 bytes") {
		t.Errorf("echoName did not report the real length: %q", got)
	}
}
