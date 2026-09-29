package tuie2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// Multifocus against the real binary: the two toggle actions on keys bound in
// config.toml (#232), a paste reaching every pane of the set with each pane's
// own bracketed-paste mode (#236), and panes in the set drawn undimmed under
// dim_unfocused (#234).
//
// How these could pass wrongly, written down first:
//   - The pasted text could be the command line the harness typed. The paste
//     payload is typed nowhere else, and the commands in the panes do not hold
//     it.
//   - Both panes could show the paste because one pane shows it twice. The
//     check needs one copy with the paste markers and one without, and only
//     one pane has bracketed paste on.
//   - The dim check could compare a pane with itself. Each pane prints its own
//     marker in the same colour, and the check compares the two markers.

// multifocusConfig binds the two actions under the leader and, when dim is
// above zero, turns on dim_unfocused with a theme so there is a ground to dim
// toward.
func multifocusConfig(dim int, dimMultifocus bool) string {
	// Tiled, so the two panes sit side by side and neither covers the
	// other's marker.
	body := "[startup]\ntiled = true\n\n[keybindings.prefix_mode]\n" +
		"toggle_multifocus_active = [\"y\"]\n" +
		"toggle_multifocus_all = [\"Y\"]\n"
	if dim > 0 {
		body = fmt.Sprintf("[appearance]\ntheme = \"catppuccin_mocha\"\ndim_unfocused = %d\ndim_multifocus = %v\n\n", dim, dimMultifocus) + body
	}
	return body
}

// leaderKey sends the leader and then key.
func leaderKey(t *testing.T, term *tuitest.Terminal, key string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), key); err != nil {
		t.Fatalf("send leader %s: %v", key, err)
	}
}

// mfWaitText waits for want on screen.
func mfWaitText(t *testing.T, term *tuitest.Terminal, want string) {
	t.Helper()
	if err := term.WaitForText(want, uiTimeout); err != nil {
		t.Fatalf("no %q message: %v\n%s", want, err, term.Snapshot())
	}
}

// TestMultifocusToggleKeysAndPasteBroadcast binds both toggles, drives them by
// key, and pastes into a set of two panes.
func TestMultifocusToggleKeysAndPasteBroadcast(t *testing.T) {
	base := spotlightConfigFile(t, multifocusConfig(0, false))
	term := startIn(t, base, startOpts{cols: 140, rows: 40})
	waitBoot(t, term)

	// Pane A turns bracketed paste on and runs cat -v, which shows the paste
	// markers as text. Pane B runs cat -v with the mode off.
	newWindow(t, term)
	enterTerminalMode(t, term)
	if err := term.SendKeys(`printf '\033[?2004h'; echo READY"A"; cat -v`, tuitest.Enter); err != nil {
		t.Fatalf("type in pane A: %v", err)
	}
	mfWaitText(t, term, "READYA")
	leaveTerminalMode(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	if err := term.SendKeys(`echo READY"B"; cat -v`, tuitest.Enter); err != nil {
		t.Fatalf("type in pane B: %v", err)
	}
	mfWaitText(t, term, "READYB")

	// toggle_multifocus_active adds the focused pane, and again removes it.
	leaderKey(t, term, "y")
	mfWaitText(t, term, "Multifocus: 1 windows")
	leaderKey(t, term, "y")
	mfWaitText(t, term, "Multifocus: removed window")

	// toggle_multifocus_all adds both panes.
	leaderKey(t, term, "Y")
	mfWaitText(t, term, "Multifocus: 2 windows")

	if err := term.Type("\x1b[200~MFPASTE\x1b[201~"); err != nil {
		t.Fatalf("send bracketed paste: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send enter: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		wrapped := strings.Count(text, "[200~MFPASTE")
		plain := strings.Count(text, "MFPASTE") - wrapped
		return wrapped > 0 && plain > 0
	}, shellTimeout); err != nil {
		t.Fatalf("the paste did not reach both panes, wrapped in pane A and raw in pane B: %v\n%s", err, term.Snapshot())
	}

	// toggle_multifocus_all with every pane in the set clears it.
	leaderKey(t, term, "Y")
	mfWaitText(t, term, "Multifocus: cleared")
	alive(t, term, "after the multifocus toggles and the paste")
}

// markerFg is the foreground of the first cell of marker on screen.
func markerFg(s tuitest.Screen, marker string) (tuitest.Color, bool) {
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if i := strings.Index(line, marker); i >= 0 {
			return s.Cell(len([]rune(line[:i])), r).Fg, true
		}
	}
	return tuitest.Color{}, false
}

// TestMultifocusDim checks the dim of a pane in the multifocus set, with
// dim_multifocus off (the default) and on.
func TestMultifocusDim(t *testing.T) {
	for _, dimMultifocus := range []bool{false, true} {
		t.Run(fmt.Sprintf("dim_multifocus=%v", dimMultifocus), func(t *testing.T) {
			base := spotlightConfigFile(t, multifocusConfig(60, dimMultifocus))
			term := startIn(t, base, startOpts{cols: 140, rows: 40})
			waitBoot(t, term)

			// Both markers are printed in one colour, assembled by the shell so
			// the command line does not hold them.
			for _, name := range []string{"A", "B"} {
				newWindow(t, term)
				enterTerminalMode(t, term)
				runInShell(t, term, `printf '\033[38;2;220;220;220m%s\033[0m\n' "INK`+name+`X"`, "INK"+name+"X", shellTimeout)
				leaveTerminalMode(t, term)
			}

			// B is focused, so A is dimmed.
			var full tuitest.Color
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				a, okA := markerFg(s, "INKAX")
				b, okB := markerFg(s, "INKBX")
				full = b
				return okA && okB && a != b
			}, uiTimeout); err != nil {
				t.Fatalf("pane A is not dimmed before multifocus, so this proves nothing: %v\n%s", err, term.Snapshot())
			}

			leaderKey(t, term, "Y")
			mfWaitText(t, term, "Multifocus: 2 windows")

			undimmed := func(s tuitest.Screen) bool {
				a, ok := markerFg(s, "INKAX")
				return ok && a == full
			}
			if dimMultifocus {
				// Give the frame time to change, then check it did not.
				if err := term.WaitFor(undimmed, uiTimeout/4); err == nil {
					t.Fatalf("dim_multifocus = true, but pane A in the set was drawn undimmed\n%s", term.Snapshot())
				}
			} else if err := term.WaitFor(undimmed, uiTimeout); err != nil {
				t.Fatalf("pane A in the multifocus set is still dimmed: %v\n%s", err, term.Snapshot())
			}

			// Out of the set, A is dimmed again.
			leaderKey(t, term, "Y")
			mfWaitText(t, term, "Multifocus: cleared")
			if err := term.WaitFor(func(s tuitest.Screen) bool { return !undimmed(s) }, uiTimeout); err != nil {
				t.Fatalf("pane A is undimmed after the set was cleared: %v\n%s", err, term.Snapshot())
			}
		})
	}
}
