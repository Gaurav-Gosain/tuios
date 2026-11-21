package session

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

const (
	fidelityCols = 40
	fidelityRows = 8
)

var wireShapes = []struct {
	name string
	out  string
}{
	{
		name: "ansi-palette",
		out: "\x1b[31mred\x1b[32mgreen\x1b[33myellow\x1b[m plain\r\n" +
			"\x1b[91mbright\x1b[97mwhite\x1b[m\r\n" +
			"\x1b[44;37mon-blue\x1b[m\r\n",
	},
	{
		name: "256-colour",
		out:  "\x1b[38;5;33mindexed\x1b[48;5;226m on-yellow\x1b[m\r\n",
	},
	{
		name: "truecolour",
		out:  "\x1b[38;2;12;34;56mrgb\x1b[48;2;200;100;50m on-rgb\x1b[m\r\n",
	},
	{
		name: "attributes",
		out: "\x1b[1mbold\x1b[2mfaint\x1b[3mitalic\x1b[m\r\n" +
			"\x1b[5mblink\x1b[7mreverse\x1b[8mconceal\x1b[9mstruck\x1b[m\r\n",
	},
	{
		name: "underline-styles",
		out: "\x1b[4:1msingle\x1b[4:2mdouble\x1b[4:3mcurly\x1b[m\r\n" +
			"\x1b[4:4mdotted\x1b[4:5mdashed\x1b[m\r\n" +
			"\x1b[4:3;58;5;196mcurly-red\x1b[59m\x1b[m\r\n",
	},
	{
		name: "hyperlink",
		out:  "plain \x1b]8;;https://example.com\x07linked\x1b]8;;\x07 plain\r\n",
	},
	{
		name: "pen-left-set",
		out:  "\x1b[1;38;5;208;48;2;20;20;20;4:3mstill in force",
	},
	{
		name: "scroll-region",
		out:  "\x1b[HHEADER\r\n\x1b[3;7r\x1b[3;1Hbody one\r\nbody two\r\n",
	},
	{
		name: "origin-mode",
		out:  "\x1b[2;6r\x1b[?6h\x1b[1;1Hinside the margins\r\n",
	},
	{
		name: "line-drawing-charset",
		out:  "\x1b(0lqqqk\r\nx   x\r\nmqqqj\r\n",
	},
	{
		name: "cursor-shape",
		out:  "\x1b[6 q$ ",
	},
	{
		name: "wide-runes",
		out:  "\x1b[35m日本語\x1b[m ascii\r\n",
	},
	{
		name: "alt-screen-over-shell",
		out: "$ ls\r\n\x1b[34mdir\x1b[m  file\r\n$ vim\r\n" +
			"\x1b[?1049h\x1b[H\x1b[2J\x1b[7m~ EDITOR \x1b[m\r\nline of text\r\n\x1b[?25l",
	},
	{
		name: "coloured-scrollback",
		out:  colouredLines(30),
	},
}

func colouredLines(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "\x1b[3%dm%02d line with colour\x1b[m\r\n", i%8, i)
	}
	return b.String()
}

func TestWireCarriesTheWholeCell(t *testing.T) {
	for _, form := range wireForms {
		for _, shape := range wireShapes {
			t.Run(form.name+"/"+shape.name, func(t *testing.T) {
				daemon := vt.NewEmulator(fidelityCols, fidelityRows)
				defer func() { _ = daemon.Close() }()
				if _, err := daemon.Write([]byte(shape.out)); err != nil {
					t.Fatalf("feed the daemon emulator: %v", err)
				}

				client := vt.NewEmulator(fidelityCols, fidelityRows)
				defer func() { _ = client.Close() }()
				state := TerminalStateOf(daemon, fidelityCols, fidelityRows, 20000, 0)
				ApplyTerminalState(client, throughWire(t, state, form.packed))

				compareEmulators(t, daemon, client)
			})
		}
	}
}

var wireForms = []struct {
	name   string
	packed bool
}{
	{"cells", false},
	{"packed", true},
}

// TestWireLeavesTheAlternateScreen covers the pane whose emulator survives a
// workspace switch. Its guest quit a full-screen program while the pane was
// hidden, so the snapshot it comes back to is of the shell's screen while the
// emulator holding it is still pointed at the alternate buffer.
func TestWireLeavesTheAlternateScreen(t *testing.T) {
	daemon := vt.NewEmulator(fidelityCols, fidelityRows)
	defer func() { _ = daemon.Close() }()
	client := vt.NewEmulator(fidelityCols, fidelityRows)
	defer func() { _ = client.Close() }()

	if _, err := daemon.Write([]byte("$ prompt\r\n\x1b[?1049h\x1b[H\x1b[2JEDITOR")); err != nil {
		t.Fatalf("feed the daemon emulator: %v", err)
	}
	ApplyTerminalState(client, TerminalStateOf(daemon, fidelityCols, fidelityRows, 0, 0))
	if !client.ActiveScreenIsAlt() {
		t.Fatal("the client never entered the alternate screen, so the case is not set up")
	}

	if _, err := daemon.Write([]byte("\x1b[?1049l")); err != nil {
		t.Fatalf("leave the alternate screen: %v", err)
	}
	ApplyTerminalState(client, TerminalStateOf(daemon, fidelityCols, fidelityRows, 0, 0))

	if client.ActiveScreenIsAlt() {
		t.Fatal("the client is still writing into the alternate buffer while its modes say it left: the shell's screen was blitted into the buffer nobody is looking at, and everything the pane prints next scrolls into a scrollback that is switched off")
	}
	compareEmulators(t, daemon, client)
}

// TestWireFillsAClientOfADifferentSize is the reported shape: an editor comes
// back from a session switch with the bottom rows of its screen blank, the last
// line of the file and the whole status line gone, everything above them
// untouched.
//
// A client's emulator is sized by that client's own layout, and the snapshot
// describes the size the daemon has the pane at. When the two disagree the blit
// used to be taken silently, because writing a cell outside the buffer is a
// no-op: the rows that did not fit were dropped. The alternate screen keeps no
// scrollback, and the guest is not going to redraw rows whose size it was never
// told changed, so those rows stay blank for the rest of the pane's life.
func TestWireFillsAClientOfADifferentSize(t *testing.T) {
	for _, short := range []int{1, 2, 5} {
		t.Run(fmt.Sprintf("client-%d-rows-shorter", short), func(t *testing.T) {
			daemon := vt.NewEmulator(fidelityCols, fidelityRows)
			defer func() { _ = daemon.Close() }()

			var b strings.Builder
			b.WriteString("\x1b[?1049h\x1b[H\x1b[2J")
			for row := 1; row < fidelityRows; row++ {
				fmt.Fprintf(&b, "\x1b[%d;1Hrow %d of the editor", row, row)
			}
			fmt.Fprintf(&b, "\x1b[%d;1H\x1b[7m NORMAL  main  the status line \x1b[m", fidelityRows)
			if _, err := daemon.Write([]byte(b.String())); err != nil {
				t.Fatalf("feed the daemon emulator: %v", err)
			}

			client := vt.NewEmulator(fidelityCols, fidelityRows-short)
			defer func() { _ = client.Close() }()
			ApplyTerminalState(client, TerminalStateOf(daemon, fidelityCols, fidelityRows, 0, 0))

			if client.Height() != fidelityRows {
				t.Fatalf("the client is %d rows to the daemon's %d: the snapshot describes a screen of a given size and the daemon is authoritative for it",
					client.Height(), fidelityRows)
			}
			compareEmulators(t, daemon, client)
		})
	}
}

func compareEmulators(t *testing.T, want, got vt.Terminal) {
	t.Helper()

	if got.IsAltScreen() != want.IsAltScreen() {
		t.Errorf("alternate screen: client %v, daemon %v", got.IsAltScreen(), want.IsAltScreen())
	}
	if g, w := got.CursorPosition(), want.CursorPosition(); g != w {
		t.Errorf("cursor: client %v, daemon %v", g, w)
	}
	if got.IsCursorHidden() != want.IsCursorHidden() {
		t.Errorf("cursor hidden: client %v, daemon %v", got.IsCursorHidden(), want.IsCursorHidden())
	}
	if g, w := penSig(got), penSig(want); g != w {
		t.Errorf("pen:\n  daemon %s\n  client %s\nthe next thing this pane prints is painted with it", w, g)
	}
	if g, w := got.ScrollRegion(), want.ScrollRegion(); g != w {
		t.Errorf("scroll region: client %v, daemon %v: the next line this pane scrolls takes the wrong rows with it", g, w)
	}
	gs, gsteady := got.CursorStyle()
	ws, wsteady := want.CursorStyle()
	if gs != ws || gsteady != wsteady {
		t.Errorf("cursor shape: client style %d steady %v, daemon style %d steady %v: the pane comes back with a cursor the guest did not ask for",
			gs, gsteady, ws, wsteady)
	}
	gi, ggl, ggr := got.Charsets()
	wi, wgl, wgr := want.Charsets()
	if gi != wi || ggl != wgl || ggr != wgr {
		t.Errorf("charsets: client %q gl=%d gr=%d, daemon %q gl=%d gr=%d: the next box the guest draws comes out as letters",
			gi, ggl, ggr, wi, wgl, wgr)
	}

	var diffs []string
	for y := range want.Height() {
		for x := range want.Width() {
			w, g := cellSig(want.CellAt(x, y)), cellSig(got.CellAt(x, y))
			if w != g {
				diffs = append(diffs, fmt.Sprintf("  screen (%d,%d)\n    daemon %s\n    client %s", x, y, w, g))
			}
			if !want.IsAltScreen() {
				continue
			}
			w, g = cellSig(want.MainCellAt(x, y)), cellSig(got.MainCellAt(x, y))
			if w != g {
				diffs = append(diffs, fmt.Sprintf("  screen underneath (%d,%d)\n    daemon %s\n    client %s", x, y, w, g))
			}
		}
	}

	if wn, gn := want.ScrollbackLen(), got.ScrollbackLen(); wn != gn {
		t.Errorf("scrollback: client holds %d lines, daemon holds %d", gn, wn)
	} else {
		for i := range wn {
			wl, gl := want.ScrollbackLine(i), got.ScrollbackLine(i)
			for x := range max(len(wl), len(gl)) {
				w, g := lineCellSig(wl, x), lineCellSig(gl, x)
				if w != g {
					diffs = append(diffs, fmt.Sprintf("  scrollback line %d col %d\n    daemon %s\n    client %s", i, x, w, g))
				}
			}
		}
	}

	if len(diffs) > 0 {
		if len(diffs) > 8 {
			diffs = append(diffs[:8], fmt.Sprintf("  ... and %d more", len(diffs)-8))
		}
		t.Errorf("the client's copy differs from the daemon's in %d places:\n%s",
			len(diffs), strings.Join(diffs, "\n"))
	}
}

func lineCellSig(line uv.Line, x int) string {
	if x >= len(line) {
		return "-"
	}
	return cellSig(&line[x])
}

func cellSig(c *uv.Cell) string {
	if c == nil {
		return "<nil>"
	}
	content := c.Content
	if content == "" {
		content = " "
	}
	return fmt.Sprintf("%q w=%d fg=%s bg=%s ul=%d ulc=%s attrs=%08b link=%q/%q",
		content, c.Width, colorSig(c.Style.Fg), colorSig(c.Style.Bg),
		c.Style.Underline, colorSig(c.Style.UnderlineColor), c.Style.Attrs,
		c.Link.URL, c.Link.Params)
}

func penSig(e vt.Terminal) string {
	pen, link := e.CursorPen()
	return cellSig(&uv.Cell{Content: " ", Width: 1, Style: pen, Link: link})
}

func colorSig(c any) string {
	if c == nil {
		return "default"
	}
	return fmt.Sprintf("%T(%v)", c, c)
}
