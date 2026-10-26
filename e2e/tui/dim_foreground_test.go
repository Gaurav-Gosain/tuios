package tuie2e

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// dim_foreground is a colour transform, so it is read where a host terminal
// would see it: the fg and bg of a marker the shell printed, in a pane that is
// not focused, against the same marker in the pane that is.
//
// Both markers carry a colour of their own in both layers. A cell that names
// none is dimmed toward the theme's ground whether or not the option is set,
// and would not tell its two values apart.

// dimUnfocusedConfig is a tiled catppuccin_mocha session with dim_unfocused
// on and extra appended to [appearance].
func dimUnfocusedConfig(extra string) string {
	return "[appearance]\ntheme = \"catppuccin_mocha\"\ndim_unfocused = 60\n" + extra +
		"\n[startup]\ntiled = true\n"
}

// markerCell is the first cell of marker on screen.
func markerCell(s tuitest.Screen, marker string) (tuitest.Cell, bool) {
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if before, _, ok := strings.Cut(line, marker); ok {
			return s.Cell(len([]rune(before)), r), true
		}
	}
	return tuitest.Cell{}, false
}

// dimPair boots tuios with body, prints a marker in each of two panes, and
// returns the marker's cell in the unfocused pane (A) and in the focused one
// (B) once A has been dimmed enough to differ from B. A frame in which A is
// still drawn like B has not been dimmed yet, and reading it would read the
// state before the option applied.
func dimPair(t *testing.T, body string) (unfocused, focused tuitest.Cell) {
	t.Helper()
	base := spotlightConfigFile(t, body)
	// True colour is asked for: the default host here is a 256-colour one, which
	// snaps colours to palette entries and could blur a small dim into none.
	term := startIn(t, base, startOpts{cols: 140, rows: 40, env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)

	// The marker is assembled by the shell so the command line does not hold
	// it, in a colour on a ground of its own.
	for _, name := range []string{"A", "B"} {
		newWindow(t, term)
		enterTerminalMode(t, term)
		runInShell(t, term,
			`printf '\033[38;2;220;220;220;48;2;40;80;120m%s\033[0m\n' "DIM`+name+`""X"`,
			"DIM"+name+"X", shellTimeout)
		leaveTerminalMode(t, term)
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		a, okA := markerCell(s, "DIMAX")
		b, okB := markerCell(s, "DIMBX")
		unfocused, focused = a, b
		return okA && okB && (a.Fg != b.Fg || a.Bg != b.Bg)
	}, uiTimeout); err != nil {
		t.Fatalf("pane A is not dimmed at all, so this proves nothing: %v\n%s", err, term.Snapshot())
	}
	return unfocused, focused
}

// TestDimForeground drives appearance.dim_foreground. On (the default), the
// unfocused pane's text is dimmed and so is its background. Off, the text keeps
// the colour the focused pane has while the background is still dimmed.
//
// Each half is the other's positive half: the default fixture shows the text
// does dim, so the unchanged text with the option off is a result of the
// option and not of a dim that never reached text.
func TestDimForeground(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		a, b := dimPair(t, dimUnfocusedConfig(""))
		if a.Fg == b.Fg {
			t.Errorf("dim_foreground unset: the unfocused text is %+v, the same as the focused text; it was not dimmed", a.Fg)
		}
		if a.Bg == b.Bg {
			t.Errorf("dim_foreground unset: the unfocused background is %+v, the same as the focused one; it was not dimmed", a.Bg)
		}
	})
	t.Run("off", func(t *testing.T) {
		a, b := dimPair(t, dimUnfocusedConfig("dim_foreground = false\n"))
		if a.Fg != b.Fg {
			t.Errorf("dim_foreground = false: the unfocused text is %+v, want the focused text's %+v", a.Fg, b.Fg)
		}
		if a.Bg == b.Bg {
			t.Errorf("dim_foreground = false: the unfocused background is %+v, the same as the focused one; "+
				"the background must still dim", a.Bg)
		}
	})
}

// nordANSI and the two defaults below are the host of discussion #850: ghostty
// on its Nord theme, with no tuios theme. hostterm answers OSC 10, 11 and 4
// with them, and the frames are drawn with them, since they are what the host
// paints an index and the default colours in.
var nordANSI = [16]string{
	"#3b4252", "#bf616a", "#a3be8c", "#ebcb8b", "#81a1c1", "#b48ead", "#88c0d0", "#e5e9f0",
	"#4c566a", "#bf616a", "#a3be8c", "#ebcb8b", "#81a1c1", "#b48ead", "#8fbcbb", "#eceff4",
}

const nordBg, nordFg = "#2e3440", "#d8dee9"

// nordPalette is the Nord host as a renderer palette.
func nordPalette() *shot.Palette {
	p := &shot.Palette{FG: hexColor(nordFg), BG: hexColor(nordBg)}
	for i, hex := range nordANSI {
		p.ANSI[i] = hexColor(hex)
	}
	return p
}

// nordHostArgs is the hostterm command line that plays the Nord host, or one
// that answers no colour question when mute is set.
func nordHostArgs(host string, mute bool) []string {
	if mute {
		return []string{host, "run", "-mute", "--"}
	}
	parts := make([]string, len(nordANSI))
	for i, hex := range nordANSI {
		parts[i] = fmt.Sprintf("%d=%s", i, hex)
	}
	return []string{host, "run", "-bg", nordBg, "-fg", nordFg, "-ansi", strings.Join(parts, ","), "--"}
}

// startNordPanes attaches to a daemon session from the Nord host with appearance
// set to body, opens a second pane, and prints two markers in each: one in the
// terminal's default colour, as a shell prompt is, and one in SGR 31, which the
// host resolves through its own palette. Pane A is left unfocused. The frame is
// saved under artifactDir as name, drawn with the host's palette.
func startNordPanes(t *testing.T, body string, mute bool, name string) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	host := buildHostTerm(t)
	useShippedLooks(base)
	writeConfig(t, base, "[startup]\ntiled = true\n\n[appearance]\n"+body+"\n")
	if out, err := tuiosCLI(t, base, "new", "nord", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{
		cols: 140, rows: 40,
		args:         []string{"attach", "nord"},
		shippedLooks: true,
		env:          []string{"COLORTERM=truecolor"},
		wrap:         nordHostArgs(host, mute),
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
	// The markers go in through the daemon, so the client's mode does not
	// matter. The printf format holds %s where the pane letter goes, so the
	// echoed command line never shows a marker.
	marker := func(pane string) string {
		return `clear; printf 'PLAIN%sX\n\033[31mRED%sX\033[0m\n' ` + pane + ` ` + pane + "\n"
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "nord", marker("A")); err != nil {
		t.Fatalf("type into pane A: %v\n%s", err, out)
	}
	paneB := newWindowIn(t, base, "nord")
	if err := paneSend(base, "nord", paneB, marker("B")); err != nil {
		t.Fatalf("type into pane B: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 2 && strings.Contains(s.Text(), "REDAX") && strings.Contains(s.Text(), "REDBX")
	}, uiTimeout); err != nil {
		t.Fatalf("the two panes and their markers never showed: %v\n%s", err, term.Snapshot())
	}
	// The unfocused pane's dim frame is drawn on the focus change; this gives
	// the settle of the host's colour answers time to land too.
	time.Sleep(time.Second)
	dir := artifactDir(t)
	saveArtifact(t, term, dir, name)
	savePNG(t, term.Screen(), nordPalette(), dir, name)
	return term
}

// cellRGB is a cell colour as the Nord host paints it, and whether the cell
// named one at all.
func cellRGB(c tuitest.Color) (color.Color, bool) {
	if c.Kind == tuitest.ColorDefault {
		return nil, false
	}
	s := resolveCell(c, nordPalette(), nordPalette().FG)
	return color.RGBA{R: s.R, G: s.G, B: s.B, A: 0xff}, true
}

// nearRGB reports whether two colours are within a few steps on every channel,
// which is what the 8-bit rounding of a blend can move it by.
func nearRGB(a, b color.Color) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	d := func(x, y uint32) int { return max(int(x>>8)-int(y>>8), int(y>>8)-int(x>>8)) }
	return d(ar, br) <= 3 && d(ag, bg) <= 3 && d(ab, bb) <= 3
}

// TestDimFollowsHostColors is discussion #850: with no tuios theme, the dim of
// an unfocused pane left text in the terminal's default colour alone, and with
// the pane background off it dimmed nothing at all, because tuios had no RGB
// for the host's colours. It has them now: the client asks the host terminal
// with OSC 10, 11 and 4.
//
// How this could pass wrongly, written down first:
//   - The dim could reach coloured text only. The default-coloured marker is
//     checked on its own.
//   - SGR 31 could be blended from xterm's red rather than the host's. The
//     expected colour is Nord's red carried toward Nord's ground, which xterm's
//     #cd0000 does not come near.
//   - The dim could paint a block of ground under the text. On a terminal with
//     a translucent background that is an opaque rectangle, so a cell with no
//     background of its own has to keep none.
//   - A host that answers nothing could still get a guessed colour. With every
//     question unanswered the default text has to take SGR 2 instead, and no
//     RGB at all.
//
// Frames: <subtest>/dim.png under artifactDir, drawn with the Nord palette.
func TestDimFollowsHostColors(t *testing.T) {
	const dim = 30
	ground := func(s string) color.Color { c := hexColor(s); return color.RGBA{R: c.R, G: c.G, B: c.B, A: 0xff} }
	check := func(t *testing.T, term *tuitest.Terminal, toward string) {
		t.Helper()
		s := term.Screen()
		plainA, okA := markerCell(s, "PLAINAX")
		plainB, okB := markerCell(s, "PLAINBX")
		redA, okR := markerCell(s, "REDAX")
		if !okA || !okB || !okR {
			t.Fatalf("the markers are not on screen\n%s", term.Snapshot())
		}
		if plainB.Fg.Kind != tuitest.ColorDefault {
			t.Errorf("the focused pane's default text is %+v, want the terminal's default", plainB.Fg)
		}
		want := overlay.MixColors(ground(nordFg), ground(toward), float64(dim)/100)
		if got, ok := cellRGB(plainA.Fg); !ok || !nearRGB(got, want) {
			t.Errorf("the unfocused pane's default text is %+v, want %v: the host's text carried toward %s",
				plainA.Fg, want, toward)
		}
		wantRed := overlay.MixColors(ground(nordANSI[1]), ground(toward), float64(dim)/100)
		if got, ok := cellRGB(redA.Fg); !ok || !nearRGB(got, wantRed) {
			t.Errorf("the unfocused pane's red text is %+v, want %v: the host's red carried toward %s",
				redA.Fg, wantRed, toward)
		}
	}

	t.Run("background-off", func(t *testing.T) {
		term := startNordPanes(t, fmt.Sprintf("theme = \"none\"\ndim_unfocused = %d\n", dim), false, "dim")
		check(t, term, nordBg)
		if a, _ := markerCell(term.Screen(), "PLAINAX"); a.Bg.Kind != tuitest.ColorDefault {
			t.Errorf("the unfocused pane's default text sits on %+v, want the terminal's own background", a.Bg)
		}
	})
	t.Run("background-theme", func(t *testing.T) {
		term := startNordPanes(t, fmt.Sprintf("theme = \"none\"\nbackground = \"theme\"\ndim_unfocused = %d\n", dim), false, "dim")
		check(t, term, nordBg)
	})
	t.Run("background-colour", func(t *testing.T) {
		term := startNordPanes(t, fmt.Sprintf("theme = \"none\"\nbackground = \"#3b4252\"\ndim_unfocused = %d\n", dim), false, "dim")
		check(t, term, "#3b4252")
	})
	t.Run("host-answers-nothing", func(t *testing.T) {
		term := startNordPanes(t, fmt.Sprintf("theme = \"none\"\ndim_unfocused = %d\n", dim), true, "dim")
		s := term.Screen()
		a, okA := markerCell(s, "PLAINAX")
		b, okB := markerCell(s, "PLAINBX")
		if !okA || !okB {
			t.Fatalf("the markers are not on screen\n%s", term.Snapshot())
		}
		if !a.Faint || a.Fg.Kind != tuitest.ColorDefault {
			t.Errorf("with no host colours the unfocused default text is %+v faint=%v, want faint in the default colour", a.Fg, a.Faint)
		}
		if b.Faint {
			t.Errorf("the focused pane's text is faint")
		}
	})
}
