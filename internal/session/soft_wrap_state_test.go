package session

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A snapshot writes cells, and cells say nothing about whether a row wrapped.
// These tests hold ApplyTerminalState to the flags the sending emulator had,
// on whichever backend the build selects.

const wrapW, wrapH = 10, 4

func wrapTerm(t *testing.T, input string) vt.Terminal {
	t.Helper()
	term := vt.NewWithScrollback(wrapW, wrapH, 100)
	t.Cleanup(func() { _ = term.Close() })
	if _, err := term.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	return term
}

func rowWraps(t *testing.T, term vt.Terminal) []bool {
	t.Helper()
	out := make([]bool, wrapH)
	for y := range out {
		out[y], _ = term.RowSoftWrapped(y)
	}
	return out
}

func sameFlags(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The reviewer's case. The client's emulator survived a workspace switch
// holding a row that wrapped. While it was away the pane printed a URL that
// fills its row and ends with a newline, then a line a URL could carry on
// into. Applied over the old screen, the snapshot must leave row 0 ending,
// or hints and the link hover join "http://b.c" and "foo.bar/zz".
func TestApplyTerminalStateClearsStaleWraps(t *testing.T) {
	client := wrapTerm(t, "0123456789abc")
	if w, _ := client.RowSoftWrapped(0); !w {
		t.Fatal("the fixture needs a wrapped row 0 on the client")
	}
	daemon := wrapTerm(t, "\x1b[2J\x1b[Hhttp://b.c\r\nfoo.bar/zz")
	if w, _ := daemon.RowSoftWrapped(0); w {
		t.Fatal("the daemon's row 0 ends with a newline and must not read as wrapped")
	}

	state := TerminalStateOf(daemon, wrapW, wrapH, 100, client.ScrollbackLen())
	ApplyTerminalState(client, state)

	if got := rowWraps(t, client); !sameFlags(got, rowWraps(t, daemon)) {
		t.Errorf("client wrap flags %v, daemon's %v", got, rowWraps(t, daemon))
	}
	if cell := client.CellAt(0, 1); cell == nil || cell.Content != "f" {
		t.Fatalf("the snapshot did not land: row 1 starts with %v", cell)
	}
}

// A snapshot from a peer that predates the flags carries none, which reads
// as no row wrapped: stale flags go, and nothing is joined by guesswork.
func TestApplyTerminalStateWithoutWrapBitsClearsFlags(t *testing.T) {
	client := wrapTerm(t, "0123456789abc")
	daemon := wrapTerm(t, "http://b.c\r\nfoo.bar/zz")
	state := TerminalStateOf(daemon, wrapW, wrapH, 100, client.ScrollbackLen())
	state.ScreenWraps, state.ScrollbackWraps = nil, nil // an old peer
	ApplyTerminalState(client, state)
	for y, w := range rowWraps(t, client) {
		if w {
			t.Errorf("row %d reads as wrapped after a snapshot that carries no flags", y)
		}
	}
}

// The positive half, and the loss on attach this fixes: a fresh emulator
// rebuilt from a snapshot gets the wraps the pane had, on the screen and in
// the history.
func TestApplyTerminalStateCarriesWraps(t *testing.T) {
	// Row one of each pair wraps; the pairs end with a newline. Enough of
	// them to push the first pairs into history.
	input := strings.Repeat("0123456789abc\r\n", 4)
	daemon := wrapTerm(t, input)
	fresh := vt.NewWithScrollback(wrapW, wrapH, 100)
	t.Cleanup(func() { _ = fresh.Close() })

	state := TerminalStateOf(daemon, wrapW, wrapH, 100, 0)
	ApplyTerminalState(fresh, state)

	if got, want := rowWraps(t, fresh), rowWraps(t, daemon); !sameFlags(got, want) {
		t.Errorf("screen wrap flags %v, want %v", got, want)
	}
	n := daemon.ScrollbackLen()
	if fresh.ScrollbackLen() != n {
		t.Fatalf("history holds %d rows, want %d", fresh.ScrollbackLen(), n)
	}
	// The newest history row carries on into the screen; the ghostty
	// synthesis cannot reproduce that one (see ghosttyRestore), so it is
	// left out of the comparison on both backends.
	for i := range n - 1 {
		got, _ := fresh.ScrollbackSoftWrapped(i)
		want, _ := daemon.ScrollbackSoftWrapped(i)
		if got != want {
			t.Errorf("history row %d (%q): wrapped = %v, want %v", i, fresh.ScrollbackLine(i).String(), got, want)
		}
	}
}

// The bitset survives a trip through its own encoding.
func TestWrapBitsRoundTrip(t *testing.T) {
	flags := []bool{true, false, false, true, false, false, false, false, true, true}
	if got := wrapFlags(wrapBits(flags), len(flags)); !sameFlags(got, flags) {
		t.Errorf("round trip %v, want %v", got, flags)
	}
	if wrapBits(make([]bool, 20)) != nil {
		t.Error("no flag set should put nothing on the wire")
	}
}
