package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

type paneShape struct {
	name      string
	arrange   func(r *rig, ptyID string)
	whileAway func(r *rig, ptyID string)
	finish    func(r *rig, ptyID string)
	check     func(t *testing.T, r *rig, ptyID string)
}

type routeCase struct {
	name     string
	run      func(r *rig, away func())
	rebuilds bool
}

var rehydrationShapes = []paneShape{
	{
		name: "live-tail",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf 'TAIL-A\nTA''IL-B\n'`, "TAIL-B")
		},
	},
	{
		name: "scrolled-back",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `i=1; while [ $i -le 60 ]; do echo "SB-$i-END"; i=$((i+1)); done`, "SB-60-END")
			w := r.winByPTY(ptyID)
			r.settle()
			w.EnterCopyMode()
			w.CopyMode.ScrollOffset = 10
			w.ScrollbackOffset = 10
		},
		check: func(t *testing.T, r *rig, ptyID string) {
			w := r.winByPTY(ptyID)
			if r.rebuiltWindows {
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
			r.feedPTY(ptyID, `printf '\033[?1049h\033[H\033[2JAL''T-SCREEN-BODY\r\n'`, "ALT-SCREEN-BODY")
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
			r.feedPTY(ptyID, `printf '\033[?1049h\033[H\033[2JAL''T-OVER-BODY\r\n'`, "ALT-OVER-BODY")
		},
		whileAway: func(r *rig, ptyID string) {
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
			r.feedPTY(ptyID, `printf '\346\227\245\346\234\254\350\252\236 WI''DE-END\n'`, "WIDE-END")
		},
	},
	{
		name: "heavy-sgr",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[1;31mBOLD-RED\033[m \033[4;32mUNDER-GREEN\033[m \033[7mREVERSED\033[m \033[3;9mSTRUCK\033[m\nSG''R-END\n'`, "SGR-END")
		},
	},
	{
		name: "256-and-truecolour",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[38;5;33mINDEXED\033[48;5;226m ON-YELLOW\033[m \033[38;2;200;100;50mTRUECOLOUR\033[m TC''-END\n'`, "TC-END")
		},
	},
	{
		name: "colour-still-in-force",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[35;1mPE''N-LEFT-SET\n'`, "PEN-LEFT-SET")
		},
		finish: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf 'PA''INTED-AFTER-RESTORE\n'`, "PAINTED-AFTER-RESTORE")
		},
	},
	{
		name: "scroll-region",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[H\033[2JHEADER\033[3;9r\033[3;1H'; i=1; while [ $i -le 12 ]; do echo "SR-$i-END"; i=$((i+1)); done`, "SR-12-END")
		},
	},
	{
		name: "origin-mode",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[2;8r\033[?6h\033[1;1HOR''IGIN-BODY-END\n'`, "ORIGIN-BODY-END")
		},
	},
	{
		name: "alt-screen-full-height",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `H=$(stty size | cut -d' ' -f1); printf '\033[?1049h\033[H\033[2J'; `+
				`i=1; while [ $i -lt $H ]; do printf '\033[%d;1HAFROW-%d' $i $i; i=$((i+1)); done; `+
				`printf '\033[%d;1HAFLAST''ROW-END' $H`, "AFLASTROW-END")
		},
		check: func(t *testing.T, r *rig, ptyID string) {
			st, err := r.daemonCells(ptyID, -1)
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
		name: "tui-mid-draw",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf '\033[?1049h\033[H\033[2J\033[1;34m\033(0lqqqk\r\nx  x\r\nmqqqj\r\nTU''I-MID-DRAW\r\n'`, "TUI-MID-DRAW")
			st, err := r.ctl.GetTerminalState(ptyID, -1, 0)
			if err != nil || st == nil || !st.IsAltScreen {
				r.t.Fatalf("the pane never entered the alternate screen (err %v, state %v)", err, st)
			}
		},
	},
	{
		name: "mid-output",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf 'MI''D-READY\n'`, "MID-READY")
		},
		whileAway: func(r *rig, ptyID string) {
			r.startPTY(ptyID, `i=1; while [ $i -le 400 ]; do echo "MID-$i-END"; i=$((i+1)); done`)
		},
		finish: func(r *rig, ptyID string) {
			r.waitDaemonShows(ptyID, "MID-400-END")
		},
	},
	{
		name: "over-buffer-while-away",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf 'OV''ER-READY\n'`, "OVER-READY")
		},
		whileAway: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `i=1; while [ $i -le 2000 ]; do echo "OV-$i-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"; i=$((i+1)); done`, "OV-2000-")
		},
	},
	{
		name: "resized-while-producing",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf 'RP''-READY\n'`, "RP-READY")
			w := r.winByPTY(ptyID)
			r.producing = filepath.Join(r.t.TempDir(), "producing")
			if err := os.WriteFile(r.producing, nil, 0o600); err != nil {
				r.t.Fatal(err)
			}
			r.startPTY(ptyID, `A=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA; `+
				`i=1; while [ -e '`+r.producing+`' ]; do echo "RP-$i-$A$A$A$A-END"; i=$((i+1)); done; `+
				`echo RP-""STOPPED`)
			r.waitDaemonShows(ptyID, "-AAAAAAAAAA")
			before := r.latestRPLine(ptyID)
			full := w.Width
			for range 40 {
				w.Resize(max(full/3, 6), w.Height)
				time.Sleep(2 * time.Millisecond)
				w.Resize(full, w.Height)
				time.Sleep(2 * time.Millisecond)
			}
			if after := r.latestRPLine(ptyID); after <= before {
				r.t.Fatalf("the guest produced nothing while the resizes landed (line %d before, %d after), so this run never reached the seam",
					before, after)
			}
			if err := os.Remove(r.producing); err != nil {
				r.t.Fatal(err)
			}
			r.waitDaemonShows(ptyID, "RP-STOPPED")
		},
	},
	{
		name: "resized-while-away",
		arrange: func(r *rig, ptyID string) {
			r.feedPTY(ptyID, `printf 'RE''SIZE-READY\n'`, "RESIZE-READY")
		},
		whileAway: func(r *rig, ptyID string) {
			w, h := r.ptySize(ptyID)
			if err := r.ctl.ResizePTY(ptyID, w-4, h-2); err != nil {
				r.t.Fatalf("resize while away: %v", err)
			}
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

func compareSides(t *testing.T, r *rig, ptyID string) {
	t.Helper()

	w := r.winByPTY(ptyID)
	st, err := r.daemonCells(ptyID, rigScrollbackOracle)
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

	pen, link := term.CursorPen()
	if got, want := stateCellSig(session.CellStateOf(&uv.Cell{Content: " ", Width: 1, Style: pen, Link: link})),
		penSig(st.Pen); got != want {
		t.Errorf("pen: client %s, daemon %s: the next thing this pane prints is painted with it", got, want)
	}
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

	dn, cn := len(st.Scrollback), term.ScrollbackLen()
	if cn > dn {
		t.Errorf("scrollback: client holds %d lines, daemon holds %d\n%s",
			cn, dn, scrollbackSeam(st, term))
		return
	}
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

func scrollbackSeam(st *session.TerminalState, term vt.Terminal) string {
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

func penSig(ps *session.StyleState) string {
	if ps == nil {
		return "<absent>"
	}
	return stateCellSig(session.CellState{Content: " ", Width: 1, StyleState: *ps})
}

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
