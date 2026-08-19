package session

import (
	"fmt"
	"testing"
)

// TestTerminalStateSendsOnlyWhatTheCallerLacks pins the contract the have
// parameter adds: the reply carries the rows past what the caller already
// holds, and nothing else, while still reporting the true scrollback length.
//
// The length matters as much as the rows. ApplyTerminalState decides how many
// of the rows it was sent to merge by subtracting its own length from
// ScrollbackLen, so a reply that trimmed the rows and the count together would
// make the client drop the very lines it was missing.
func TestTerminalStateSendsOnlyWhatTheCallerLacks(t *testing.T) {
	const depth = 200
	pty := wirePTY(t, 80, 24, depth)
	full := pty.GetTerminalState(depth, 0)
	if full == nil {
		t.Fatal("no state")
	}
	if len(full.Scrollback) != depth {
		t.Fatalf("a caller holding nothing wanted %d rows, got %d", depth, len(full.Scrollback))
	}

	for _, behind := range []int{0, 1, 17, depth} {
		t.Run(fmt.Sprintf("behind-%d", behind), func(t *testing.T) {
			have := full.ScrollbackLen - behind
			st := pty.GetTerminalState(depth, have)
			if st == nil {
				t.Fatal("no state")
			}
			if len(st.Scrollback) != behind {
				t.Fatalf("a caller behind by %d wanted %d rows, got %d",
					behind, behind, len(st.Scrollback))
			}
			// The true length, not the number of rows sent, or the client's own
			// subtraction against it would discard what it is missing.
			if st.ScrollbackLen != full.ScrollbackLen {
				t.Fatalf("ScrollbackLen reported %d, want the true %d",
					st.ScrollbackLen, full.ScrollbackLen)
			}
			// The rows must be the NEWEST ones. Handing back the oldest would
			// leave a hole exactly where the client stopped watching.
			for i := range st.Scrollback {
				want := full.Scrollback[len(full.Scrollback)-behind+i]
				got := st.Scrollback[i]
				if len(got) != len(want) {
					t.Fatalf("row %d: width %d, want %d", i, len(got), len(want))
				}
				for x := range want {
					if got[x].Content != want[x].Content {
						t.Fatalf("row %d col %d: %q, want %q (rows are not the newest)",
							i, x, got[x].Content, want[x].Content)
					}
				}
			}
		})
	}
}

// TestTerminalStateHaveNeverWidensTheWindow guards the interaction between the
// two bounds: have may only ever narrow the reply, so a caller that asks for no
// scrollback still gets none however far behind it claims to be, and one that
// asks for a small window never gets more than it asked for.
func TestTerminalStateHaveNeverWidensTheWindow(t *testing.T) {
	const depth = 200
	pty := wirePTY(t, 80, 24, depth)

	if st := pty.GetTerminalState(-1, 0); st == nil || len(st.Scrollback) != 0 {
		t.Fatalf("a request for no scrollback got %d rows", len(st.Scrollback))
	}
	if st := pty.GetTerminalState(10, 0); st == nil || len(st.Scrollback) != 10 {
		t.Fatalf("a request bounded at 10 got %d rows", len(st.Scrollback))
	}
	// Behind by 50 but asking for at most 10 still yields 10.
	if st := pty.GetTerminalState(10, depth-50); st == nil || len(st.Scrollback) != 10 {
		t.Fatalf("a request bounded at 10 while 50 behind got %d rows", len(st.Scrollback))
	}
	// A caller claiming more than exists asks for nothing, not for a negative
	// count that would wrap into the whole buffer.
	if st := pty.GetTerminalState(depth, depth*10); st == nil || len(st.Scrollback) != 0 {
		t.Fatalf("a caller claiming more than exists got %d rows", len(st.Scrollback))
	}
}
