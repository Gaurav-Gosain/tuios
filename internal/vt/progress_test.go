package vt

import "testing"

// TestParseProgress checks the OSC 9;4 payload parse: the state and percentage
// are read, the percentage is clamped, a bare payload clears, and anything that
// is not a progress report is refused so it can fall through to Notify.
func TestParseProgress(t *testing.T) {
	cases := []struct {
		payload string
		state   ProgressState
		percent int
		ok      bool
	}{
		{"4;1;50", ProgressNormal, 50, true},
		{"4;3", ProgressIndeterminate, 0, true},
		{"4;0", ProgressClear, 0, true},
		{"4;2;10", ProgressError, 10, true},
		{"4;4;90", ProgressWarning, 90, true},
		{"4", ProgressClear, 0, true},
		{"4\a", ProgressClear, 0, true},
		{"4;1;250", ProgressNormal, 100, true}, // clamped
		{"4;1;-5", ProgressNormal, 0, true},    // clamped
		{"4;9", 0, 0, false},                   // no such state
		{"4;x", 0, 0, false},                   // not a number
		// A notification body that merely starts with "4" is not a progress
		// report and has to stay a notification.
		{"42 files changed", 0, 0, false},
		{"finished", 0, 0, false},
	}
	for _, c := range cases {
		state, percent, ok := parseProgress(c.payload)
		if ok != c.ok || (ok && (state != c.state || percent != c.percent)) {
			t.Errorf("parseProgress(%q) = (%d, %d, %v), want (%d, %d, %v)",
				c.payload, state, percent, ok, c.state, c.percent, c.ok)
		}
	}
}

// TestEmulatorRoutesProgressAndNotify proves the emulator splits OSC 9 two ways:
// a 9;4 progress report reaches the Progress callback and never Notify, and an
// ordinary 9 notification still reaches Notify.
func TestEmulatorRoutesProgressAndNotify(t *testing.T) {
	var gotState ProgressState
	var gotPercent int
	progressCalls := 0
	var notified []string

	em := NewEmulator(80, 24)
	em.SetCallbacks(Callbacks{
		Progress: func(state ProgressState, percent int) {
			gotState, gotPercent = state, percent
			progressCalls++
		},
		Notify: func(_, body string) { notified = append(notified, body) },
	})

	// A working report.
	_, _ = em.Write([]byte("\x1b]9;4;3;0\x07"))
	if progressCalls != 1 || gotState != ProgressIndeterminate {
		t.Fatalf("after 9;4;3: calls=%d state=%d, want 1 and indeterminate", progressCalls, gotState)
	}
	if len(notified) != 0 {
		t.Fatalf("progress report leaked to Notify: %q", notified)
	}

	// A determinate report carries its percentage.
	_, _ = em.Write([]byte("\x1b]9;4;1;42\x07"))
	if gotState != ProgressNormal || gotPercent != 42 {
		t.Fatalf("after 9;4;1;42: state=%d percent=%d, want normal and 42", gotState, gotPercent)
	}

	// Clearing it.
	_, _ = em.Write([]byte("\x1b]9;4;0\x07"))
	if gotState != ProgressClear {
		t.Fatalf("after 9;4;0: state=%d, want clear", gotState)
	}

	// An ordinary notification is untouched by any of this.
	_, _ = em.Write([]byte("\x1b]9;build finished\x07"))
	if len(notified) != 1 || notified[0] != "build finished" {
		t.Fatalf("notifications = %q, want one \"build finished\"", notified)
	}
	if progressCalls != 3 {
		t.Fatalf("progress calls = %d, want 3", progressCalls)
	}
}
