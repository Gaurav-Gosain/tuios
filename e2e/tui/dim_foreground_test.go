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

func dimUnfocusedConfig(extra string) string {
	return "[appearance]\ntheme = \"catppuccin_mocha\"\ndim_unfocused = 60\n" + extra +
		"\n[startup]\ntiled = true\n"
}

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

func dimPair(t *testing.T, body string) (unfocused, focused tuitest.Cell) {
	t.Helper()
	base := spotlightConfigFile(t, body)
	term := startIn(t, base, startOpts{cols: 140, rows: 40, env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)

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

type hostScheme struct {
	name   string
	bg, fg string
	ansi   [16]string
}

var nordScheme = hostScheme{"nord", "#2e3440", "#d8dee9", [16]string{
	"#3b4252", "#bf616a", "#a3be8c", "#ebcb8b", "#81a1c1", "#b48ead", "#88c0d0", "#e5e9f0",
	"#4c566a", "#bf616a", "#a3be8c", "#ebcb8b", "#81a1c1", "#b48ead", "#8fbcbb", "#eceff4",
}}

var (
	nordLightScheme = hostScheme{"nord-light", "#e5e9f0", "#414858", [16]string{
		"#3b4252", "#bf616a", "#96b17f", "#c5a565", "#81a1c1", "#b48ead", "#7bb3c3", "#a5abb6",
		"#4c566a", "#bf616a", "#96b17f", "#c5a565", "#81a1c1", "#b48ead", "#82afae", "#eceff4",
	}}
	mochaScheme = hostScheme{"catppuccin-mocha", "#1e1e2e", "#cdd6f4", [16]string{
		"#45475a", "#f38ba8", "#a6e3a1", "#f9e2af", "#89b4fa", "#f5c2e7", "#94e2d5", "#bac2de",
		"#585b70", "#f38ba8", "#a6e3a1", "#f9e2af", "#89b4fa", "#f5c2e7", "#94e2d5", "#a6adc8",
	}}
	latteScheme = hostScheme{"catppuccin-latte", "#eff1f5", "#4c4f69", [16]string{
		"#5c5f77", "#d20f39", "#40a02b", "#df8e1d", "#1e66f5", "#ea76cb", "#179299", "#acb0be",
		"#6c6f85", "#d20f39", "#40a02b", "#df8e1d", "#1e66f5", "#ea76cb", "#179299", "#bcc0cc",
	}}
	tokyoNightScheme = hostScheme{"tokyonight", "#1a1b26", "#c0caf5", [16]string{
		"#15161e", "#f7768e", "#9ece6a", "#e0af68", "#7aa2f7", "#bb9af7", "#7dcfff", "#a9b1d6",
		"#414868", "#f7768e", "#9ece6a", "#e0af68", "#7aa2f7", "#bb9af7", "#7dcfff", "#c0caf5",
	}}
	tokyoDayScheme = hostScheme{"tokyonight-day", "#e1e2e7", "#3760bf", [16]string{
		"#e9e9ed", "#f52a65", "#587539", "#8c6c3e", "#2e7de9", "#9854f1", "#007197", "#6172b0",
		"#a1a6c5", "#f52a65", "#587539", "#8c6c3e", "#2e7de9", "#9854f1", "#007197", "#3760bf",
	}}
)

func (h hostScheme) palette() *shot.Palette {
	p := &shot.Palette{FG: hexColor(h.fg), BG: hexColor(h.bg)}
	for i, hex := range h.ansi {
		p.ANSI[i] = hexColor(hex)
	}
	return p
}

func (h hostScheme) ground() color.Color {
	c := hexColor(h.bg)
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: 0xff}
}

func (h hostScheme) hostArgs(host string, mute bool) []string {
	if mute {
		return []string{host, "run", "-mute", "--"}
	}
	parts := make([]string, len(h.ansi))
	for i, hex := range h.ansi {
		parts[i] = fmt.Sprintf("%d=%s", i, hex)
	}
	return []string{host, "run", "-bg", h.bg, "-fg", h.fg, "-ansi", strings.Join(parts, ","), "--"}
}

var nordANSI = nordScheme.ansi

const nordBg, nordFg = "#2e3440", "#d8dee9"

func startNordPanes(t *testing.T, body string, mute bool, name string) *tuitest.Terminal {
	t.Helper()
	return startHostPanes(t, nordScheme, body, mute, name)
}

func startHostPanes(t *testing.T, scheme hostScheme, body string, mute bool, name string) *tuitest.Terminal {
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
		wrap:         scheme.hostArgs(host, mute),
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
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
	time.Sleep(time.Second)
	dir := artifactDir(t)
	saveArtifact(t, term, dir, name)
	savePNG(t, term.Screen(), scheme.palette(), dir, name)
	return term
}

func cellRGB(c tuitest.Color) (color.Color, bool) { return cellRGBOn(c, nordScheme) }

func cellRGBOn(c tuitest.Color, h hostScheme) (color.Color, bool) {
	if c.Kind == tuitest.ColorDefault {
		return nil, false
	}
	p := h.palette()
	s := resolveCell(c, p, p.FG)
	return color.RGBA{R: s.R, G: s.G, B: s.B, A: 0xff}, true
}

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
