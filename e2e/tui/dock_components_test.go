package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuitest"
)

// dockRowText is the dock's content row, found by the workspace readout the bar
// has always carried rather than by computing where the dock is.
func dockRowText(t *testing.T, term *tuitest.Terminal) string {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for r := rows - 1; r >= 0; r-- {
		if text := s.Line(r); strings.Contains(text, "1:") {
			return text
		}
	}
	t.Fatalf("no dock row carries the workspace readout\n%s", term.Snapshot())
	return ""
}

// TestDockDefaultListsDrawTheSameBar is the promise to everyone with no [dock]
// table: writing the arrangement down as three lists of component names changed
// nothing about the bar. The default config and an explicit default plan must
// produce the same row, cell for cell, on the real binary.
func TestDockDefaultListsDrawTheSameBar(t *testing.T) {
	bare, _ := start(t, startOpts{})
	waitBoot(t, bare)
	newWindow(t, bare)
	waitWindowCount(t, bare, 1, "opening a shell for the bare dock")
	if err := bare.WaitStable(uiTimeout); err != nil {
		t.Fatalf("bare screen never settled: %v\n%s", err, bare.Snapshot())
	}
	bareRow := dockRowText(t, bare)

	// The two configs must differ in the dock lists and in nothing else. The
	// [startup] booleans are the one thing a hand-written file does not inherit
	// from the defaults, and tiled changes the dock's own mode chip, so it is
	// spelled out here to match what the bare run loads.
	base := t.TempDir()
	writeConfig(t, base, `
[startup]
tiled = true

[dock]
left   = ["mode", "workspaces", "trail", "tape"]
center = ["windows"]
right  = ["notifications", "copy-help", "cpu", "ram", "clock", "session-controls"]
`)
	listed := startIn(t, base, startOpts{})
	waitBoot(t, listed)
	newWindow(t, listed)
	waitWindowCount(t, listed, 1, "opening a shell for the listed dock")
	if err := listed.WaitStable(uiTimeout); err != nil {
		t.Fatalf("listed screen never settled: %v\n%s", err, listed.Snapshot())
	}
	listedRow := dockRowText(t, listed)

	if bareRow != listedRow {
		t.Fatalf("the default lists draw a different bar\n bare:   %q\n listed: %q\n%s",
			bareRow, listedRow, listed.Snapshot())
	}
}

// TestDockCustomComponentDraws is the contract on the real binary: a command in
// the config file puts its output on the bar.
func TestDockCustomComponentDraws(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[dock]
left = ["mode", "workspaces", "trail", "custom/marker"]

[dock.custom.marker]
command = "echo DOCKCELL"
refresh = "once"
`)
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")

	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if strings.Contains(dockRowText(t, term), "DOCKCELL") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the custom component never drew its cell\n%s", term.Snapshot())
}

// TestDockBrokenComponentLeavesTheBarAlone pins the failure mode. A component
// whose command exits nonzero is absent, and everything else on the bar is
// exactly where it was: a broken cell must never be a broken bar.
func TestDockBrokenComponentLeavesTheBarAlone(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[dock]
left = ["mode", "workspaces", "trail", "custom/broken"]

[dock.custom.broken]
command = "exit 3"
refresh = "once"
`)
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v\n%s", err, term.Snapshot())
	}

	row := dockRowText(t, term)
	if !strings.Contains(row, "1:1") {
		t.Fatalf("the workspace readout is gone, so a broken component broke the bar: %q\n%s",
			row, term.Snapshot())
	}
}

// TestDockIdleCostWithComponentsStaysLow is the invariant on the real binary:
// a dock carrying components that do not poll must not wake the program. If
// loading the component machinery armed a timer of its own, this is where it
// shows up, and it is the same budget the plain idle guard defends.
func TestDockIdleCostWithComponentsStaysLow(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[dock]
left  = ["mode", "workspaces", "trail", "custom/once"]
right = ["custom/pushed", "session-controls"]

[dock.custom.once]
command = "echo ONCE"
refresh = "once"

[dock.custom.pushed]
command = "sleep 300"
refresh = "push"
`)
	var wire byteCounter
	statsPath := filepath.Join(base, "tickstats")
	term := startIn(t, base, startOpts{
		out: &wire,
		env: []string{"TUIOS_STATS_FILE=" + statsPath},
	})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 3, "opening three idle shells")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled before idle: %v\n%s", err, term.Snapshot())
	}

	before := wire.n.Load()
	time.Sleep(10 * time.Second)
	idleBytes := wire.n.Load() - before
	t.Logf("idle wire bytes over 10s with once+push components: %d (budget %d)", idleBytes, idleWireBudget)
	if idleBytes > idleWireBudget {
		t.Fatalf("a dock of once and push components wrote %d bytes over an idle window (budget %d); "+
			"the component engine armed a timer\n%s", idleBytes, idleWireBudget, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
		t.Fatalf("send leader q: %v", err)
	}
	waitExit(t, term, "dock idle test quit")

	ticks, work, render := readTickStats(t, statsPath)
	t.Logf("tick stats with components loaded: ticks=%d work=%d render=%d", ticks, work, render)
	if ticks == 0 {
		t.Fatalf("stats file recorded zero ticks")
	}
	if work >= ticks {
		t.Fatalf("tick work %d did not fall below tick count %d with dock components loaded", work, ticks)
	}
	_ = os.Remove(statsPath)
}

// dockFloodRSSBudget is the most the client may hold, in KiB, while a dock
// component floods its stdout. An idle client sits well under 100 MiB. The
// engine used to read the whole flood before it cut it to 64 KiB, so `yes`
// put gigabytes on the heap inside the three second timeout.
const dockFloodRSSBudget = 300 << 10

// TestDockFloodingComponentStaysBounded runs `yes` as a dock component, again
// every second, and holds the client to a memory budget while it does. The
// client must also keep answering the keyboard, and the cell must show the
// first line, which proves the component ran and its output was read.
//
// The memory is sampled often and the test stops at the first sample over the
// budget, so a build without the cap fails fast instead of eating the machine.
func TestDockFloodingComponentStaysBounded(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[dock]
left = ["mode", "workspaces", "trail", "custom/flood"]

[dock.custom.flood]
command = "yes DOCKFLOOD"
refresh = "1s"
`)
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")

	pid := term.Pid()
	peak := 0
	sawCell := false
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		rss := rssOf(t, pid)
		peak = max(peak, rss)
		if rss > dockFloodRSSBudget {
			t.Fatalf("the client holds %d KiB while a dock component floods its stdout (budget %d KiB); "+
				"the engine reads the flood without a cap", rss, dockFloodRSSBudget)
		}
		if !sawCell && screenHas(term.Screen(), "DOCKFLOOD") {
			sawCell = true
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("client peak RSS over 8s of a flooding dock component: %d KiB (budget %d KiB)", peak, dockFloodRSSBudget)
	if !sawCell {
		t.Fatalf("the flooding component never drew its first line, so its output was never read\n%s", term.Snapshot())
	}

	// The component still runs every second, so this is typed into a flood.
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ALIVE$((40+2))", "ALIVE42", shellTimeout)
}

// TestDockComponentCannotSendUnsafeCharacters puts a bidi override, the
// one-byte CSI (U+009B) and a zero-width space in a component's output. None of
// them may reach the host terminal. The printable text around them, a wide
// character and an emoji must still draw, which is the positive half.
func TestDockComponentCannotSendUnsafeCharacters(t *testing.T) {
	base := t.TempDir()
	// printf octal escapes: \342\200\256 is U+202E, \302\233 is U+009B,
	// \342\200\213 is U+200B. The tail is U+6F22 and U+1F600.
	writeConfig(t, base, `
[dock]
left = ["mode", "workspaces", "trail", "custom/unsafe"]

[dock.custom.unsafe]
command = '''printf 'SAFE\342\200\256RL\302\233OK\342\200\213ZW \346\274\242\360\237\230\200\n' '''
refresh = "once"
`)
	var wire lockedBuffer
	term := startIn(t, base, startOpts{out: &wire})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")
	waitForAll(t, term, uiTimeout, "the component drawing its text", "SAFERLOKZW", "漢")

	sent := wire.String()
	for _, bad := range []struct{ name, seq string }{
		{"the bidi override U+202E", "‮"},
		{"the one-byte CSI U+009B", "\u009b"},
		{"the zero-width space U+200B", "​"},
	} {
		if strings.Contains(sent, bad.seq) {
			t.Errorf("tuios wrote %s to the host terminal from a dock component", bad.name)
		}
	}
	if !strings.Contains(sent, "\U0001F600") {
		t.Errorf("the emoji in the component's output never reached the host terminal")
	}
}

// dockCellFind returns the dock row and the column where text starts, scanning
// the grid's cells rather than Line so a cap glyph beside it is its own cell.
func dockCellFind(s tuitest.Screen, text string) (x, y int, ok bool) {
	cols, rows := s.Size()
	runes := []rune(text)
	for y = rows - 1; y >= 0; y-- {
		for x = 0; x+len(runes) <= cols; x++ {
			match := true
			for i, r := range runes {
				if s.Cell(x+i, y).Rune != r {
					match = false
					break
				}
			}
			if match {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

func dockRGB(r, g, b uint8) tuitest.Color {
	return tuitest.Color{Kind: tuitest.ColorRGB, R: r, G: g, B: b}
}

// TestDockCellStyleAndSessionComponent is the contract for the session cell and
// for cell styles on the real binary. Listing "session" puts the formatted
// workspace and pane on the bar; [dock.session] and [dock.custom.NAME] colour
// the cell and draw their caps in its background colour; a cell with a custom
// background and no foreground gets ink that reads on that background. The
// unstyled custom cell is the positive half: it shows the colours come from the
// style and are not on every cell.
// The session cell sits on the right and the custom ones on the left because
// the two sides gate a cell in different places.
func TestDockCellStyleAndSessionComponent(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[dock]
left  = ["mode", "workspaces", "trail", "custom/plain", "custom/light", "custom/named"]
right = ["session", "notifications", "copy-help", "cpu", "ram", "clock", "session-controls"]

[dock.session]
format    = "S[{workspace}:{pane}]"
bg        = "#7fbbb3"
fg        = "#272e33"
bold      = true
cap_left  = "<"
cap_right = ">"

[dock.custom.plain]
command = "echo PLAINCELL"
refresh = "once"

[dock.custom.light]
command   = "echo LIGHTCELL"
refresh   = "once"
bg        = "#f5f5f5"
cap_right = ")"

[dock.custom.named]
command = "echo NAMEDCELL"
refresh = "once"
bg      = "bright_magenta"
fg      = "accent"
`)
	term := startIn(t, base, startOpts{env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")
	// The workspace number is the current workspace and the pane number is the
	// focused pane's place in it, so an empty second workspace tells them apart.
	if err := term.SendKeys(tuitest.Alt('2')); err != nil {
		t.Fatalf("send alt+2: %v", err)
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, a := dockCellFind(s, "S[2:0]")
		_, _, b := dockCellFind(s, "PLAINCELL")
		_, _, c := dockCellFind(s, "LIGHTCELL")
		_, _, d := dockCellFind(s, "NAMEDCELL")
		return a && b && c && d
	}, uiTimeout); err != nil {
		t.Fatalf("the session and custom cells never all drew: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()

	teal, ink, light := dockRGB(0x7f, 0xbb, 0xb3), dockRGB(0x27, 0x2e, 0x33), dockRGB(0xf5, 0xf5, 0xf5)

	x, y, _ := dockCellFind(s, "S[2:0]")
	body := s.Cell(x, y)
	if body.Bg != teal || body.Fg != ink || !body.Bold {
		t.Errorf("session cell is fg %+v bg %+v bold %v, want fg %+v bg %+v bold", body.Fg, body.Bg, body.Bold, ink, teal)
	}
	left, right := s.Cell(x-2, y), s.Cell(x+len("S[2:0]")+1, y)
	if left.Content != "<" || right.Content != ">" {
		t.Errorf("session caps are %q and %q, want \"<\" and \">\"\n%s", left.Content, right.Content, term.Snapshot())
	}
	if left.Fg != teal || right.Fg != teal {
		t.Errorf("session caps are drawn in %+v and %+v, want the cell's background %+v", left.Fg, right.Fg, teal)
	}

	qx, qy, _ := dockCellFind(s, "PLAINCELL")
	plain := s.Cell(qx, qy)
	if plain.Bg.Kind != tuitest.ColorRGB || plain.Bg == teal || plain.Bg == light || plain.Bold {
		t.Errorf("unstyled custom cell is bg %+v bold %v, want the dock's own ground", plain.Bg, plain.Bold)
	}

	lx, ly, _ := dockCellFind(s, "LIGHTCELL")
	lc := s.Cell(lx, ly)
	if lc.Bg != light {
		t.Errorf("styled custom cell has bg %+v, want %+v", lc.Bg, light)
	}
	if ratio := overlay.ContrastRatio(tuiColor(lc.Fg), tuiColor(lc.Bg)); ratio < overlay.ContrastFloor {
		t.Errorf("a cell with a custom bg and no fg has ink %+v on %+v, %.2f:1, under the %.1f:1 floor", lc.Fg, lc.Bg, ratio, overlay.ContrastFloor)
	}
	if c := s.Cell(lx+len("LIGHTCELL")+1, ly); c.Content != ")" || c.Fg != light {
		t.Errorf("styled custom cell's cap is %q in %+v, want \")\" in %+v", c.Content, c.Fg, light)
	}

	// A slot name and a role name. With no theme, bright_magenta is the
	// terminal's own slot 13, so the host's palette paints it, and accent is
	// the dock's own accent, the CharmTone Charple the mode pill uses.
	nx, ny, _ := dockCellFind(s, "NAMEDCELL")
	nc := s.Cell(nx, ny)
	if nc.Bg.Kind != tuitest.ColorIndexed || nc.Bg.Index != 13 {
		t.Errorf("a cell with bg = \"bright_magenta\" is on %+v, want slot 13", nc.Bg)
	}
	if charple := dockRGB(0x6b, 0x50, 0xff); nc.Fg != charple {
		t.Errorf("a cell with fg = \"accent\" is in %+v, want the dock's accent %+v", nc.Fg, charple)
	}
}

// TestDockCellBadColoursKeepTheDocksGround is what a value that is not a
// colour does: it is dropped where the cell is drawn, as the config warning says, so
// the cell keeps the dock's own ground. Passed on as it is, lipgloss would make
// it no colour at all and the cell would draw on the terminal's background.
// The bad cell is compared with an unstyled one beside it, which is the
// positive half: that ground is the dock's own and not the terminal's.
func TestDockCellBadColoursKeepTheDocksGround(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[dock]
left = ["mode", "workspaces", "trail", "custom/plain", "custom/bad"]

[dock.custom.plain]
command = "echo PLAINCELL"
refresh = "once"

[dock.custom.bad]
command   = "echo BADCELL"
refresh   = "once"
bg        = "pink"
fg        = "rouge"
cap_right = "}"
`)
	term := startIn(t, base, startOpts{env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, a := dockCellFind(s, "PLAINCELL")
		_, _, b := dockCellFind(s, "BADCELL")
		return a && b
	}, uiTimeout); err != nil {
		t.Fatalf("the custom cells never drew: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()

	qx, qy, _ := dockCellFind(s, "PLAINCELL")
	plain := s.Cell(qx, qy)
	if plain.Bg.Kind != tuitest.ColorRGB {
		t.Fatalf("the unstyled cell is on %+v, want the dock's own ground", plain.Bg)
	}
	bx, by, _ := dockCellFind(s, "BADCELL")
	bad := s.Cell(bx, by)
	if bad.Bg != plain.Bg || bad.Fg != plain.Fg {
		t.Errorf("a cell with bad colours is fg %+v bg %+v, want the dock's own fg %+v bg %+v", bad.Fg, bad.Bg, plain.Fg, plain.Bg)
	}
	if c := s.Cell(bx+len("BADCELL")+1, by); c.Content != "}" || c.Fg != plain.Bg {
		t.Errorf("a cell with bad colours has cap %q in %+v, want \"}\" in the dock's ground %+v", c.Content, c.Fg, plain.Bg)
	}
}

// TestDockSessionDefaultFormat is the default text: the session name, the
// workspace and the pane. The test session has no name, so the cell is the two
// numbers, with the space the empty name would leave trimmed away: a cell drawn
// "< 1:1 >" and not "<  1:1 >". It is also the check that "session" is in no
// default list: the bar's own workspace readout is the one "1:1" on a bare dock,
// and listing the component is what makes a second.
func TestDockSessionDefaultFormat(t *testing.T) {
	bare, _ := start(t, startOpts{})
	waitBoot(t, bare)
	newWindow(t, bare)
	waitWindowCount(t, bare, 1, "opening a shell for the bare dock")
	if err := bare.WaitStable(uiTimeout); err != nil {
		t.Fatalf("bare screen never settled: %v\n%s", err, bare.Snapshot())
	}
	if row := dockRowText(t, bare); strings.Count(row, "1:1") != 1 {
		t.Fatalf("a bare dock should carry the workspace readout once and no session cell: %q", row)
	}

	base := t.TempDir()
	writeConfig(t, base, `
[dock]
right = ["session", "session-controls"]

[dock.session]
cap_left  = "<"
cap_right = ">"
`)
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, ok := dockCellFind(s, "< 1:1 >")
		return ok
	}, uiTimeout); err != nil {
		t.Fatalf("the session cell never drew \"< 1:1 >\": %v\n%s", err, term.Snapshot())
	}
}
