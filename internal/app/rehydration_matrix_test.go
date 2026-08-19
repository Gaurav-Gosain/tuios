package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	uv "github.com/charmbracelet/ultraviolet"
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
			st, err := r.ctl.GetTerminalState(ptyID, -1, 0)
			if err != nil || st == nil || !st.IsAltScreen {
				r.t.Fatalf("the pane never entered the alternate screen (err %v, state %v)", err, st)
			}
		},
		check: func(t *testing.T, r *rig, ptyID string) {
			st, err := r.ctl.GetTerminalState(ptyID, -1, 0)
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
			st, err := r.ctl.GetTerminalState(ptyID, -1, 0)
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
		// The 16-colour palette with attributes on top, which is what ls,
		// grep and a prompt put on screen. These follow the user's terminal
		// theme, so a route that brings them back as the RGB they resolve to
		// repaints the pane in shades the user never chose.
		name: "heavy-sgr",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[1;31mBOLD-RED\033[m \033[4;32mUNDER-GREEN\033[m \033[7mREVERSED\033[m \033[3;9mSTRUCK\033[m SGR-END\n'`, "SGR-END")
		},
	},
	{
		name: "256-and-truecolour",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[38;5;33mINDEXED\033[48;5;226m ON-YELLOW\033[m \033[38;2;200;100;50mTRUECOLOUR\033[m TC-END\n'`, "TC-END")
		},
	},
	{
		// A guest that set a rendition and left it in force. What matters is
		// not the text already on screen but the line printed after the route
		// has finished: it arrives on the live stream and is painted with
		// whatever pen the client's emulator is holding.
		name: "colour-still-in-force",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[35;1mPEN-LEFT-SET\n'`, "PEN-LEFT-SET")
		},
		finish: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf 'PAINTED-AFTER-RESTORE\n'`, "PAINTED-AFTER-RESTORE")
		},
	},
	{
		// A status line held out of the scrolling part of the screen, and
		// enough output afterwards to make the pane scroll inside it.
		name: "scroll-region",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[H\033[2JHEADER\033[3;9r\033[3;1H'; i=1; while [ $i -le 12 ]; do echo "SR-$i-END"; i=$((i+1)); done`, "SR-12-END")
		},
	},
	{
		name: "origin-mode",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[2;8r\033[?6h\033[1;1HORIGIN-BODY-END\n'`, "ORIGIN-BODY-END")
		},
	},
	{
		// A full-screen program using every row it has, with something on the
		// last one, which is where an editor keeps its status line. The
		// alternate screen has no scrollback, so a row lost off the bottom
		// here is lost for good: the guest will not redraw it unless its own
		// size changes, and a client resizing its local emulator does not
		// change it.
		name: "alt-screen-full-height",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `H=$(stty size | cut -d' ' -f1); printf '\033[?1049h\033[H\033[2J'; `+
				`i=1; while [ $i -lt $H ]; do printf '\033[%d;1HAFROW-%d' $i $i; i=$((i+1)); done; `+
				`printf '\033[%d;1HAFLASTROW-END' $H`, "AFLASTROW-END")
		},
		check: func(t *testing.T, r *rig, ptyID string) {
			st, err := r.ctl.GetTerminalState(ptyID, -1, 0)
			if err != nil || st == nil {
				t.Fatalf("read the daemon's copy: %v", err)
			}
			if !strings.Contains(stateText(st), "AFLASTROW-END") {
				t.Fatalf("the daemon lost the last row, so the route cannot be blamed for the client")
			}
			if w := r.winByPTY(ptyID); !strings.Contains(clientText(w), "AFLASTROW-END") {
				t.Errorf("the bottom row of the full-screen program is gone: an editor taken through this route comes back without its status line")
			}
		},
	},
	{
		// A full-screen program caught between frames: in the alternate
		// screen, drawing a box out of the DEC line-drawing set, with that set
		// still selected and a colour still in force.
		name: "tui-mid-draw",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[?1049h\033[H\033[2J\033[1;34m\033(0lqqqk\r\nx  x\r\nmqqqj\r\nTUI-MID-DRAW\r\n'`, "TUI-MID-DRAW")
			st, err := r.ctl.GetTerminalState(ptyID, -1, 0)
			if err != nil || st == nil || !st.IsAltScreen {
				r.t.Fatalf("the pane never entered the alternate screen (err %v, state %v)", err, st)
			}
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
		// A pane resized while it is producing. Where a line wraps is decided
		// by the width the emulator had when it consumed the bytes, so the two
		// copies agree only if they change width at the same byte. A line that
		// wrapped on one side and not the other is in the scrollback for good:
		// nothing lays a scrollback line out again, on either side.
		//
		// This is the seam the client used to lose. It resized its own emulator
		// the moment the layout asked and told the daemon over a message that
		// was not waited for, so everything the guest produced in between was
		// laid out one width apart.
		name: "resized-while-producing",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf 'RP-READY\n'`, "RP-READY")
			w := r.winByPTY(ptyID)
			// Lines longer than the pane at every width it is taken through, so
			// each one is a wrap decision, and started rather than waited for so
			// the resizes below land among them.
			r.startPTY(ptyID, `A=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA; `+
				`i=1; while [ $i -le 20000 ]; do echo "RP-$i-$A$A$A$A-END"; i=$((i+1)); done`)
			// Resized only once the pane is known to be producing. A resize
			// that lands before the guest has said anything settles on both
			// sides before the first byte and tests nothing.
			//
			// Cut to a third of the width, so a line that took four rows takes
			// ten: a seam laid out at the wrong width is a different number of
			// scrollback lines, not only different content in them.
			// Gated on any produced line rather than a numbered one. These
			// lines wrap to about three rows each at this width, so the run
			// fills more rows than the emulator keeps, and a line numbered low
			// enough to prove the guest has only just started is also the first
			// to be evicted. Waiting for one that is already gone spends the
			// whole deadline and reports a timeout instead of a divergence.
			r.waitDaemonShows(ptyID, "RP-")
			// Resized repeatedly, the way dragging a border over a pane that is
			// producing does. One resize settles on both sides in about the time
			// it takes the daemon to read a message; a run of them keeps the
			// daemon a width behind for as long as the drag lasts, which is the
			// state the two copies can disagree in.
			full := w.Width
			for range 40 {
				w.Resize(max(full/3, 6), w.Height)
				time.Sleep(2 * time.Millisecond)
				w.Resize(full, w.Height)
				time.Sleep(2 * time.Millisecond)
			}
			// The seam only exists while the guest is producing. If it got all
			// the way to the end first, the resizes landed on output that was
			// already laid out and settled, and the case proves nothing. That
			// is worth a failure rather than a pass, because the pass would be
			// indistinguishable from a real one.
			if r.daemonShows(ptyID, "RP-20000-") {
				r.t.Fatal("the guest finished before the resizes landed, so this run never reached the seam")
			}
		},
		finish: func(r *rig, ptyID string) {
			r.waitDaemonShows(ptyID, "RP-20000-")
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
	st, err := r.ctl.GetTerminalState(ptyID, rigScrollbackOracle, 0)
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

	// What the pane holds is only half of it. These three decide how the output
	// that has not arrived yet is painted, where it lands, and which glyphs it
	// draws, and none of them can be read back off the cells.
	pen, link := term.CursorPen()
	if got, want := stateCellSig(session.CellStateOf(&uv.Cell{Content: " ", Width: 1, Style: pen, Link: link})),
		penSig(st.Pen); got != want {
		t.Errorf("pen: client %s, daemon %s: the next thing this pane prints is painted with it", got, want)
	}
	// A pane whose guest set no margins carries none, and scrolls its whole
	// screen at whatever size this client has it at.
	m := term.ScrollRegion()
	want := []int{0, 0, term.Width(), term.Height()}
	if len(st.Margins) == 4 {
		want = st.Margins
	}
	if m.Min.X != want[0] || m.Min.Y != want[1] || m.Dx() != want[2] || m.Dy() != want[3] {
		t.Errorf("scroll region: client %v of %dx%d, want %v: the next line this pane scrolls takes the wrong rows with it",
			m, term.Width(), term.Height(), want)
	}
	if ids, gl, gr := term.Charsets(); len(st.Charsets) == 6 &&
		(int(ids[0]) != st.Charsets[0] || int(ids[1]) != st.Charsets[1] ||
			int(ids[2]) != st.Charsets[2] || int(ids[3]) != st.Charsets[3] ||
			gl != st.Charsets[4] || gr != st.Charsets[5]) {
		t.Errorf("charsets: client %q gl=%d gr=%d, daemon %v: the next box the guest draws comes out as letters", ids, gl, gr, st.Charsets)
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
		// Counts alone say nothing about which side is wrong. Read from the
		// oldest line down, the first line the two disagree on is where the
		// extra one came from: a client line that is a prefix of the daemon's
		// is the same output wrapped at a narrower width, which is a size the
		// two applied at different points in the stream, not a duplicate.
		t.Errorf("scrollback: client holds %d lines, daemon holds %d\n%s",
			cn, dn, scrollbackSeam(st, term))
		return
	}
	// Compared cell for cell like the screen, not as text. History that came
	// back in the wrong colours read the same as history that came back right.
	// The whole extent is scanned before anything is reported. The first
	// differing line alone cannot tell a one-line seam from two windows offset
	// against each other for their entire length, and those are different bugs.
	base := dn - cn
	first, last, ndiff := -1, -1, 0
	firstCol, firstGot, firstWant := 0, "", ""
	for i := range cn {
		line := term.ScrollbackLine(i)
		row := st.Scrollback[base+i]
		for x := range max(len(line), len(row)) {
			got, want := " ", " "
			if x < len(line) {
				got = uvCellSig(&line[x])
			}
			if x < len(row) {
				want = stateCellSig(row[x])
			}
			if got != want {
				if first < 0 {
					first, firstCol, firstGot, firstWant = i, x, got, want
				}
				last = i
				ndiff++
				break
			}
		}
	}
	if first >= 0 {
		show := func(i int) string {
			return fmt.Sprintf("  [%d] client %q\n      daemon %q", i,
				strings.TrimRight(term.ScrollbackLine(i).String(), " "), stateRow(st.Scrollback[base+i]))
		}
		t.Errorf("scrollback differs on %d of %d lines, first %d last %d, first at column %d:\n  client %s\n  daemon %s\n%s\n%s",
			ndiff, cn, first, last, firstCol, firstGot, firstWant,
			show(first), show(last))
	}
}

// scrollbackSeam reports the oldest scrollback line the two sides disagree on,
// as text, with the lines either side of it for context.
func scrollbackSeam(st *session.TerminalState, term *vt.Emulator) string {
	daemon := func(i int) string { return stateRow(st.Scrollback[i]) }
	client := func(i int) string { return strings.TrimRight(term.ScrollbackLine(i).String(), " ") }

	n := min(len(st.Scrollback), term.ScrollbackLen())
	seam := n
	for i := range n {
		if client(i) != daemon(i) {
			seam = i
			break
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "the two agree on lines 0..%d and part company at %d:\n", seam-1, seam)
	for i := max(seam-1, 0); i < min(seam+2, n); i++ {
		fmt.Fprintf(&b, "  [%d] client %q\n      daemon %q\n", i, client(i), daemon(i))
	}
	return b.String()
}

// penSig describes a serialized pen the way stateCellSig describes a cell, so
// the two sides of the comparison read the same.
func penSig(ps *session.StyleState) string {
	if ps == nil {
		return "<absent>"
	}
	return stateCellSig(session.CellState{Content: " ", Width: 1, StyleState: *ps})
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
