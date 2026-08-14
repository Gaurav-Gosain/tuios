package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The matrix. Every route a client takes into a pane, crossed with every shape
// a pane can be in when it takes it, asserting the one contract they all share:
// the client ends up holding what the daemon holds. docs/REHYDRATION.md states
// the contract and why the routes collapse into two implementations of it.
//
// The oracle is the daemon's own emulator, read over the wire the client reads
// it over. Grid, scrollback, cursor and the alternate-screen flag are all
// compared, because the bugs this replaces were each found by noticing one of
// them on screen after the others looked fine.

// paneShape arranges a pane into one of the states rehydration has to survive.
type paneShape struct {
	name string
	// arrange runs with the pane visible and subscribed, before the route.
	arrange func(r *rig, ptyID string)
	// whileAway runs at the point in the route where this client is not
	// subscribed to the pane. Nil when the shape is about the pane itself
	// rather than about what happened behind the client's back.
	whileAway func(r *rig, ptyID string)
	// finish runs after the route and before the comparison, for a shape that
	// leaves the pane still producing.
	finish func(r *rig, ptyID string)
	// check adds what this shape alone is about, on top of the comparison every
	// shape gets.
	check func(t *testing.T, r *rig, ptyID string)
}

// routeCase is one way a client comes to hold a pane. away runs while the pane
// is not subscribed, which is where a shape's whileAway hook goes.
type routeCase struct {
	name string
	run  func(r *rig, away func())
	// rebuilds marks a route that closes every window and builds the session's
	// panes again, which is what decides whether client-local view state can
	// survive it at all.
	rebuilds bool
}

var rehydrationShapes = []paneShape{
	{
		name: "live-tail",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf 'TAIL-A\nTAIL-B\n'`, "TAIL-B")
		},
	},
	{
		name: "scrolled-back",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `i=1; while [ $i -le 60 ]; do echo "SB-$i-END"; i=$((i+1)); done`, "SB-60-END")
			w := r.winByPTY(ptyID)
			r.settle()
			// Scrolled up, which is a position the user chose and expects
			// back. Copy mode carries the offset and mirrors it onto the
			// window, which is what the wheel and the motion keys both do.
			w.EnterCopyMode()
			w.CopyMode.ScrollOffset = 10
			w.ScrollbackOffset = 10
		},
		check: func(t *testing.T, r *rig, ptyID string) {
			w := r.winByPTY(ptyID)
			if r.rebuiltWindows {
				// Where the pane is scrolled to is this viewer's state, not the
				// pane's: it is not on the wire, and a second client watching
				// the same pane must not be dragged to where this one scrolled.
				// A route that closes every window therefore loses it, and the
				// pane comes back at the tail. Asserted rather than skipped so
				// the loss is a decision on the record: restoring the raw
				// offset after the pane produced more output would put the user
				// somewhere they never were, so recovering this means anchoring
				// to a scrollback line rather than to a distance from the
				// bottom. See docs/REHYDRATION.md.
				if w.InCopyMode() || w.ScrollbackOffset != 0 {
					t.Errorf("a route that rebuilds windows came back at offset %d (copy mode %v), want the tail",
						w.ScrollbackOffset, w.InCopyMode())
				}
				return
			}
			if !w.InCopyMode() || w.ScrollbackOffset != 10 {
				t.Errorf("the pane came back at offset %d (copy mode %v), want offset 10: coming back to a pane you had scrolled up in and finding it at the tail loses the place the user chose",
					w.ScrollbackOffset, w.InCopyMode())
			}
		},
	},
	{
		name: "alt-screen",
		arrange: func(r *rig, ptyID string) {
			// Enter the alternate screen and draw in it, the way vim or htop
			// leaves a pane. Written by hand rather than by running an editor so
			// the test does not depend on one being installed.
			r.feedPTY(ptyID, `printf '\033[?1049h\033[H\033[2JALT-SCREEN-BODY\r\n'`, "ALT-SCREEN-BODY")
			// A shape that quietly failed to arrange itself would make every
			// alt-screen row of the matrix pass by testing nothing.
			st, err := r.ctl.GetTerminalState(ptyID, -1)
			if err != nil || st == nil || !st.IsAltScreen {
				r.t.Fatalf("the pane never entered the alternate screen (err %v, state %v)", err, st)
			}
		},
		check: func(t *testing.T, r *rig, ptyID string) {
			st, err := r.ctl.GetTerminalState(ptyID, -1)
			if err != nil {
				t.Fatalf("read the daemon's copy: %v", err)
			}
			if !st.IsAltScreen {
				t.Fatalf("the daemon lost the alternate screen, so the route cannot be blamed for the client")
			}
			w := r.winByPTY(ptyID)
			if !w.IsAltScreen() {
				t.Errorf("the pane came back out of the alternate screen: a vim or htop pane taken through this route is left showing the shell's screen")
			}
		},
	},
	{
		name: "alt-screen-over-buffer",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[?1049h\033[H\033[2JALT-OVER-BODY\r\n'`, "ALT-OVER-BODY")
		},
		whileAway: func(r *rig, ptyID string) {
			// More than the ring holds, produced inside the alternate screen,
			// so the sequence that entered it has rolled out of the ring by the
			// time the client comes back. A pane running vim or htop across a
			// switch is exactly this.
			r.feedPTY(ptyID, `i=1; while [ $i -le 2000 ]; do echo "AO-$i-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"; i=$((i+1)); done`, "AO-2000-")
		},
		check: func(t *testing.T, r *rig, ptyID string) {
			st, err := r.ctl.GetTerminalState(ptyID, -1)
			if err != nil {
				t.Fatalf("read the daemon's copy: %v", err)
			}
			if !st.IsAltScreen {
				t.Fatalf("the daemon lost the alternate screen, so the route cannot be blamed for the client")
			}
			if w := r.winByPTY(ptyID); !w.IsAltScreen() {
				t.Errorf("the pane came back out of the alternate screen")
			}
		},
	},
	{
		name: "wide-runes",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\346\227\245\346\234\254\350\252\236 WIDE-END\n'`, "WIDE-END")
		},
	},
	{
		name: "mid-output",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf 'MID-READY\n'`, "MID-READY")
		},
		whileAway: func(r *rig, ptyID string) {
			// Started and not waited on, so it is still producing when the
			// route puts the pane back on screen. A pane caught mid-output is
			// the one whose replay lands in the middle of a line.
			r.startPTY(ptyID, `i=1; while [ $i -le 400 ]; do echo "MID-$i-END"; i=$((i+1)); done`)
		},
		finish: func(r *rig, ptyID string) {
			r.waitDaemonShows(ptyID, "MID-400-END")
		},
	},
	{
		name: "over-buffer-while-away",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf 'OVER-READY\n'`, "OVER-READY")
		},
		whileAway: func(r *rig, ptyID string) {
			// More than the 64KB ring holds, so the client cannot be resumed
			// and the bytes it missed are gone.
			r.feedPTY(ptyID, `i=1; while [ $i -le 2000 ]; do echo "OV-$i-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"; i=$((i+1)); done`, "OV-2000-")
		},
	},
	{
		name: "resized-while-away",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf 'RESIZE-READY\n'`, "RESIZE-READY")
		},
		whileAway: func(r *rig, ptyID string) {
			w, h := r.ptySize(ptyID)
			if err := r.ctl.ResizePTY(ptyID, w-4, h-2); err != nil {
				r.t.Fatalf("resize while away: %v", err)
			}
			// Waited for, so the shape is a pane that was resized while it was
			// hidden rather than one whose resize is still in flight as it comes
			// back. The latter is a different question and this row is not it.
			rigWaitUntil(r.t, "the daemon to apply the resize", func() bool {
				gw, gh := r.ptySize(ptyID)
				return gw == w-4 && gh == h-2
			})
		},
	},
}

var rehydrationRoutes = []routeCase{
	{
		name:     "reattach",
		rebuilds: true,
		run: func(r *rig, away func()) {
			r.detach()
			away()
			r.attach()
		},
	},
	{
		name:     "session-switch",
		rebuilds: true,
		run: func(r *rig, away func()) {
			other := r.otherSession()
			if err := r.m.SwitchToSession(other); err != nil {
				r.t.Fatalf("switch away: %v", err)
			}
			away()
			if err := r.m.SwitchToSession(r.session); err != nil {
				r.t.Fatalf("switch back: %v", err)
			}
		},
	},
	{
		name: "workspace-switch",
		run: func(r *rig, away func()) {
			r.m.SwitchToWorkspace(2)
			away()
			r.m.SwitchToWorkspace(1)
		},
	},
	{
		// The first client stays attached and subscribed throughout, so this is
		// a second client arriving at a live session rather than the same one
		// coming back. It is also the mechanism a first attach uses, on a client
		// that has never seen the pane.
		name:     "second-client",
		rebuilds: true,
		run: func(r *rig, away func()) {
			away()
			r.attach()
		},
	},
}

func TestRehydrationMatrix(t *testing.T) {
	for _, rt := range rehydrationRoutes {
		for _, shape := range rehydrationShapes {
			t.Run(rt.name+"/"+shape.name, func(t *testing.T) {
				r := newRig(t, 1)
				r.rebuiltWindows = rt.rebuilds
				ptyID := r.win(0).PTYID

				shape.arrange(r, ptyID)
				r.settle()

				rt.run(r, func() {
					if shape.whileAway != nil {
						shape.whileAway(r, ptyID)
					}
				})

				if shape.finish != nil {
					shape.finish(r, ptyID)
				}
				r.settle()
				r.converge(ptyID)
				compareSides(t, r, ptyID)
				if shape.check != nil {
					shape.check(t, r, ptyID)
				}
			})
		}
	}
}

// compareSides is the assertion the whole matrix exists to make.
func compareSides(t *testing.T, r *rig, ptyID string) {
	t.Helper()

	w := r.winByPTY(ptyID)
	st, err := r.ctl.GetTerminalState(ptyID, rigScrollbackOracle)
	if err != nil {
		t.Fatalf("read the daemon's copy: %v", err)
	}
	if st == nil {
		t.Fatal("the daemon has no copy of the pane")
	}

	w.RLockIO()
	defer w.RUnlockIO()
	term := w.Terminal
	if term == nil {
		t.Fatal("the client has no emulator for the pane")
	}

	if term.Width() != st.Width || term.Height() != st.Height {
		t.Fatalf("size: client %dx%d, daemon %dx%d",
			term.Width(), term.Height(), st.Width, st.Height)
	}

	if got := w.IsAltScreen(); got != st.IsAltScreen {
		t.Errorf("alternate screen: client %v, daemon %v", got, st.IsAltScreen)
	}
	if pos := term.CursorPosition(); pos.X != st.CursorX || pos.Y != st.CursorY {
		t.Errorf("cursor: client %d,%d, daemon %d,%d", pos.X, pos.Y, st.CursorX, st.CursorY)
	}

	// Grid, cell for cell.
	var diffs []string
	for y := range st.Height {
		for x := range st.Width {
			want := " "
			if y < len(st.Screen) && x < len(st.Screen[y]) {
				want = stateCellSig(st.Screen[y][x])
			}
			got := uvCellSig(term.CellAt(x, y))
			if got != want {
				diffs = append(diffs, fmt.Sprintf("  (%d,%d) client %q daemon %q", x, y, got, want))
			}
		}
	}
	if len(diffs) > 0 {
		if len(diffs) > 12 {
			diffs = append(diffs[:12], fmt.Sprintf("  ... and %d more", len(diffs)-12))
		}
		t.Errorf("grid differs in %d cells:\n%s\n%s", len(diffs), strings.Join(diffs, "\n"),
			sideBySide(st, w))
	}

	// Scrollback: the client may hold less history than the daemon, never more,
	// and never a line the daemon does not have at that offset.
	dn, cn := len(st.Scrollback), term.ScrollbackLen()
	if cn > dn {
		t.Errorf("scrollback: client holds %d lines, daemon holds %d", cn, dn)
		return
	}
	base := dn - cn
	for i := range cn {
		got := strings.TrimRight(term.ScrollbackLine(i).String(), " ")
		want := stateRow(st.Scrollback[base+i])
		if got != want {
			t.Errorf("scrollback line %d of %d (daemon line %d):\n  client %q\n  daemon %q",
				i, cn, base+i, got, want)
			return
		}
	}
}

// sideBySide renders both copies of a pane for a failure message.
func sideBySide(st *session.TerminalState, w *terminal.Window) string {
	var b strings.Builder
	b.WriteString("--- daemon screen ---\n")
	for _, row := range st.Screen {
		b.WriteString(stateRow(row))
		b.WriteByte('\n')
	}
	b.WriteString("--- client screen ---\n")
	for y := range w.Terminal.Height() {
		var row strings.Builder
		for x := range w.Terminal.Width() {
			cell := w.Terminal.CellAt(x, y)
			if cell == nil || cell.Content == "" {
				row.WriteByte(' ')
				continue
			}
			row.WriteString(cell.Content)
		}
		b.WriteString(strings.TrimRight(row.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}
