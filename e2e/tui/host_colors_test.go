package tuie2e

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// The host terminal's own colours, told to the programs in the panes and used
// for the chrome, on a terminal tuitest cannot be: one with a light
// background. testdata/hostterm sits between tuitest and tuios and answers the
// colour questions itself, as a light terminal would, and switches to a dark
// scheme on SIGUSR1 the way a terminal following the system appearance does.
//
// How this could pass wrongly, written down first:
//   - The pane's answer could come from tuitest's emulator rather than from
//     tuios: hostterm swallows every colour question it answers, so tuitest
//     never sees one, and tuitest answers black, which the light and the dark
//     scheme here both differ from.
//   - The answer could be read from the command echo: the printed line is
//     HOSTTERM-<spec>=<colour>, and the typed command holds no "=".
//   - The dark answer could be the black the emulator gave before the fix:
//     the dark scheme's background is #1e1e2e, not black.
//   - The rail could be measured in a cell that carries no colour: a cell in
//     the terminal's default colour fails as "not measured".
//   - A switch could be missed and the old answer read again: each round
//     clears the pane first and waits for the new colour, and the host log
//     has to show tuios asked for the background after the switch.
//   - The daemon path could be skipped: the client is attached to a daemon
//     session, whose emulators answer the pane's questions, so the colours
//     have to travel from this client to the daemon.

// hostANSI is the stand-in host's own sixteen: Solarized, as a light
// terminal ships it. hostterm answers OSC 4 with them, and the frames at 16
// and 256 colours are resolved and drawn with them, since they are what the
// host would draw an index in.
var hostANSI = [16]string{
	"#073642", "#dc322f", "#859900", "#b58900", "#268bd2", "#d33682", "#2aa198", "#eee8d5",
	"#002b36", "#cb4b16", "#586e75", "#657b83", "#839496", "#6c71c4", "#93a1a1", "#fdf6e3",
}

// hostANSISpec is hostANSI as hostterm's -ansi flag.
func hostANSISpec() string {
	parts := make([]string, len(hostANSI))
	for i, hex := range hostANSI {
		parts[i] = fmt.Sprintf("%d=%s", i, hex)
	}
	return strings.Join(parts, ",")
}

// hexColor reads #rrggbb.
func hexColor(hex string) shot.Color {
	c, _ := shot.ParseHex(hex)
	return c
}

// hostGroundPalette is the palette the host draws with on a given ground.
func hostGroundPalette(fg, bg string) *shot.Palette {
	p := &shot.Palette{FG: hexColor(fg), BG: hexColor(bg)}
	for i, hex := range hostANSI {
		p.ANSI[i] = hexColor(hex)
	}
	return p
}

// tuitestColor turns a cell colour back into the colour value it was sent
// as, nil for the terminal's default.
func tuitestColor(c tuitest.Color) color.Color {
	switch c.Kind {
	case tuitest.ColorIndexed:
		return ansi.IndexedColor(c.Index)
	case tuitest.ColorRGB:
		return color.RGBA{R: c.R, G: c.G, B: c.B, A: 0xff}
	}
	return nil
}

// hostInkAt is the foreground of a cell as the host shows it: a 24-bit colour
// as it is, and an index through the host's palette. False for a cell in the
// terminal's default colour, which has nothing to measure.
func hostInkAt(s tuitest.Screen, col, row int) (color.Color, bool) {
	c := tuitestColor(s.Cell(col, row).Fg)
	if c == nil {
		return nil, false
	}
	p := hostGroundPalette("#000000", "#000000")
	return p.Resolve(c, p.FG), true
}

// renderScreenPNG draws the screen tuitest holds with tuios's own renderer,
// on the host's palette, and writes it to path. It is the frame the host
// terminal shows at the colour depth the client ran at, which a capture taken
// inside tuios cannot show: that one is drawn from tuios's own cells, before
// they are reduced to 256 or 16 colours.
func renderScreenPNG(t *testing.T, s tuitest.Screen, p *shot.Palette, path string) {
	t.Helper()
	cols, rows := s.Size()
	g := shot.NewGrid(cols, rows, p.FG, p.BG)
	for y := range rows {
		for x := range cols {
			c := s.Cell(x, y)
			var attrs uint8
			if c.Bold {
				attrs |= uv.AttrBold
			}
			if c.Faint {
				attrs |= uv.AttrFaint
			}
			if c.Italic {
				attrs |= uv.AttrItalic
			}
			if c.Reverse {
				attrs |= uv.AttrReverse
			}
			if c.Strikethrough {
				attrs |= uv.AttrStrikethrough
			}
			style := uv.Style{Fg: tuitestColor(c.Fg), Bg: tuitestColor(c.Bg), Attrs: attrs}
			if c.Underline {
				style.Underline = uv.UnderlineSingle
			}
			g.Cells[y][x] = shot.MakeCell(c.Content, c.Width, style, uv.Link{}, p)
		}
	}
	data, err := shot.RenderPNG(g, nil)
	if err != nil {
		t.Fatalf("render %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// buildHostTerm builds the stand-in host terminal.
func buildHostTerm(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "hostterm")
	build := exec.Command("go", "build", "-o", bin, "./testdata/hostterm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build hostterm: %v\n%s", err, out)
	}
	return bin
}

// askPane runs hostterm's query mode in the focused pane of session for each
// spec until every answer is the one wanted or the deadline passes, and
// returns the last answers by spec. It asks again rather than once, because
// what a pane is told can change a moment after the question: the host's
// switch reaches the daemon a round trip after the signal.
func askPane(t *testing.T, base, session, host string, want map[string]string) map[string]string {
	t.Helper()
	var cmd strings.Builder
	cmd.WriteString("clear")
	for spec := range want {
		cmd.WriteString("; " + host + " query '" + spec + "'")
	}
	cmd.WriteString("\r")
	got := map[string]string{}
	var pane string
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		if out, err := tuiosCLI(t, base, "send-keys", "-s", session, "-l", cmd.String()); err != nil {
			t.Fatalf("send the queries: %v\n%s", err, out)
		}
		time.Sleep(time.Second)
		pane, _ = tuiosCLI(t, base, "capture-pane", "-s", session)
		for spec := range want {
			re := regexp.MustCompile(`HOSTTERM-` + regexp.QuoteMeta(spec) + `=(\S+)`)
			if m := re.FindAllStringSubmatch(pane, -1); m != nil {
				got[spec] = m[len(m)-1][1]
			}
		}
		done := true
		for spec, w := range want {
			if got[spec] != w {
				done = false
			}
		}
		if done {
			return got
		}
	}
	t.Logf("pane at the deadline:\n%s", pane)
	return got
}

// captureScreenTo takes a full screen capture through the client's capture
// mode and copies the file to the artifact directory under name.
func captureScreenTo(t *testing.T, term *tuitest.Terminal, shots, artifacts, name string) {
	t.Helper()
	before := len(shotFiles(t, shots))
	if err := term.SendKeys(tuitest.Ctrl('b'), "C"); err != nil {
		t.Fatalf("send leader+C: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, "full screen") }, uiTimeout); err != nil {
		t.Fatalf("capture mode never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("f"); err != nil {
		t.Fatalf("send f: %v", err)
	}
	if err := term.WaitFor(func(tuitest.Screen) bool { return len(shotFiles(t, shots)) > before }, uiTimeout); err != nil {
		t.Fatalf("the capture never landed: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, ".png") && !screenHas(s, "Saving the image")
	}, uiTimeout); err != nil {
		t.Fatalf("the capture never finished: %v\n%s", err, term.Snapshot())
	}
	files := shotFiles(t, shots)
	data, err := os.ReadFile(files[len(files)-1])
	if err != nil {
		t.Fatalf("read the capture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(artifacts, name+".png"), data, 0o644); err != nil {
		t.Fatalf("save the capture: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("close the preview: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "Saving the image", ".png") }, uiTimeout); err != nil {
		t.Fatalf("the preview did not close: %v\n%s", err, term.Snapshot())
	}
}

// checkRailReads measures the rail's two session names and the dock's notice
// against ground, and fails any under the text floor.
func checkRailReads(t *testing.T, term *tuitest.Terminal, ground color.Color, when string, withNotice bool) {
	t.Helper()
	s := term.Screen()
	railX := railHeaderColumn(s)
	if railX < 0 {
		t.Fatalf("%s: no rail on screen\n%s", when, term.Snapshot())
	}
	for _, probe := range []struct {
		what, text string
		from       int
	}{
		{"the rail's attached session", "e2e-light", railX},
		{"the rail's other session", "e2e-other", railX},
		{"the dock's notice", "Window management mode", 0},
	} {
		if probe.from == 0 && !withNotice {
			continue
		}
		row, col, ok := textAt(s, probe.text, probe.from)
		if !ok {
			t.Fatalf("%s: %s (%q) is not on screen\n%s", when, probe.what, probe.text, term.Snapshot())
		}
		ink, measured := hostInkAt(s, col, row)
		if !measured {
			t.Fatalf("%s: %s at (%d,%d) carries no colour to measure\n%s", when, probe.what, col, row, term.SnapshotStyled())
		}
		if ratio := overlay.ContrastRatio(ink, ground); ratio < overlay.ContrastFloor {
			t.Errorf("%s: %s at (%d,%d) is %v on the host's ground %v, %.2f:1, under the %.1f:1 floor",
				when, probe.what, col, row, ink, ground, ratio, overlay.ContrastFloor)
		}
	}
	// Each session row's dot burns that session's colour, lifted until it
	// reads on the ground under the row. With no theme that ground is the
	// host's, not the black a theme-less tuios used to assume: measured
	// against black, bright cyan needs no lift and all but vanishes on a
	// light terminal. The dot is a mark, held to the mark floor.
	for _, name := range []string{"e2e-light", "e2e-other"} {
		row, col, ok := textAt(s, name, railX)
		if !ok {
			t.Fatalf("%s: the session row %q is not on screen\n%s", when, name, term.Snapshot())
		}
		dotCol := -1
		for c := col - 1; c >= railX && c >= col-3; c-- {
			if content := s.Cell(c, row).Content; content != "" && content != " " {
				dotCol = c
				break
			}
		}
		if dotCol < 0 {
			t.Fatalf("%s: no dot before the session row %q\n%s", when, name, term.Snapshot())
		}
		ink, measured := hostInkAt(s, dotCol, row)
		if !measured {
			t.Fatalf("%s: the dot of %q at (%d,%d) carries no colour to measure\n%s", when, name, dotCol, row, term.SnapshotStyled())
		}
		if ratio := overlay.ContrastRatio(ink, ground); ratio < overlay.MarkFloor {
			t.Errorf("%s: the dot of %q at (%d,%d) is %v on the host's ground %v, %.2f:1, under the %.1f:1 mark floor",
				when, name, dotCol, row, ink, ground, ratio, overlay.MarkFloor)
		}
	}
	// The pane's frame is a shape, held to the mark floor. With no theme its
	// colour is a fixed pale cyan picked for a dark terminal.
	row, col, ok := textAt(s, "╭", 0)
	if !ok {
		t.Fatalf("%s: no pane frame on screen\n%s", when, term.Snapshot())
	}
	ink, measured := hostInkAt(s, col, row)
	if !measured {
		t.Fatalf("%s: the pane frame at (%d,%d) carries no colour to measure\n%s", when, col, row, term.SnapshotStyled())
	}
	if ratio := overlay.ContrastRatio(ink, ground); ratio < overlay.MarkFloor {
		t.Errorf("%s: the pane frame at (%d,%d) is %v on the host's ground %v, %.2f:1, under the %.1f:1 mark floor",
			when, col, row, ink, ground, ratio, overlay.MarkFloor)
	}
}

// TestHostColorsReachPanesAndChrome attaches to a daemon session from a light
// terminal with no tuios theme and the pane background off, which is how
// tuios ships. A pane program asking OSC 11, OSC 10 and OSC 4 for slot 1 is
// told the host's own colours, the rail and the dock are drawn to read on the
// light ground, and after the host switches to its dark scheme the pane is
// told the dark colours and the chrome follows. Each state is kept as a frame
// and a full screen capture under artifactDir.
//
// Negative control: on a build without the host colour work every pane
// answer is rgb:0000/0000/0000 for OSC 11 and rgb:ffff/ffff/ffff for OSC 10,
// the rail's names measure about 1.1:1 on the light ground, and the test
// fails on both.
func TestHostColorsReachPanesAndChrome(t *testing.T) {
	for _, depth := range []hostDepth{
		{name: "truecolor", env: []string{"COLORTERM=truecolor"}, contrast: true, ramp: true},
		{name: "256", contrast: true},
		// At 16 colours every chrome ink is one of the host's sixteen, so
		// neither the floors nor the ramp can be measured here: which index an
		// ink falls to is the per-depth chrome's business (PLAN Wave 1 item 1).
		// The panes' answers do not depend on depth and are held here too.
		{name: "16", env: []string{"TERM=xterm"}},
	} {
		t.Run(depth.name, func(t *testing.T) { hostColorsAt(t, depth) })
	}
}

// hostDepth is one colour depth the client runs at, and what can be measured
// there.
type hostDepth struct {
	name string
	env  []string
	// contrast holds the rail, the dock and the pane frame to their floors.
	contrast bool
	// ramp checks which chrome ramp is drawn on the mid grey. It needs
	// 24-bit cells: at 256 colours both ramps' fills fall on the grey ramp.
	ramp bool
}

// hostColorsAt is TestHostColorsReachPanesAndChrome at one colour depth,
// given as the client's environment. Every frame is also drawn with tuios's
// own renderer on the host's palette as <name>-terminal.png, which is the
// screen as the host shows it at that depth.
func hostColorsAt(t *testing.T, depth hostDepth) {
	base := t.TempDir()
	killDaemon(t, base)
	host := buildHostTerm(t)
	artifacts := artifactDir(t)
	shots := shotDir(t, base)
	hostLog := filepath.Join(t.TempDir(), "host.log")

	useShippedLooks(base)
	writeConfig(t, base, "[screenshot]\ndirectory = \""+shots+"\"\nformat = \"png\"\n")
	for _, name := range []string{"e2e-light", "e2e-other"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", name, err, out)
		}
	}

	const (
		lightBg, lightFg = "#fdf6e3", "#2a2a2a"
		darkBg, darkFg   = "#1e1e2e", "#e0e0e0"
		red              = "#dc322f"
		// A grey whose 8-bit luminance, 112, is inside the 110 to 140
		// hysteresis band and below the plain light test's crossover.
		midBg, midFg = "#707070", "#101010"
	)
	term := startIn(t, base, startOpts{
		args:         []string{"attach", "e2e-light"},
		shippedLooks: true,
		env:          depth.env,
		wrap: []string{host, "run", "-bg", lightBg, "-fg", lightFg,
			"-alt-bg", midBg + "," + darkBg + "," + midBg, "-alt-fg", midFg + "," + darkFg + "," + midFg,
			"-ansi", hostANSISpec(), "-log", hostLog, "--"},
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1 && strings.Contains(s.Text(), "e2e-other")
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached with the rail listing both sessions: %v\n%s", err, term.Snapshot())
	}

	xrgb := func(hex string) string {
		h := strings.TrimPrefix(hex, "#")
		return "rgb:" + h[0:2] + h[0:2] + "/" + h[2:4] + h[2:4] + "/" + h[4:6] + h[4:6]
	}
	rgb := func(hex string) color.Color {
		var c color.RGBA
		h := strings.TrimPrefix(hex, "#")
		for i, p := range []*uint8{&c.R, &c.G, &c.B} {
			var v uint8
			for _, d := range h[2*i : 2*i+2] {
				v <<= 4
				switch {
				case d >= '0' && d <= '9':
					v |= uint8(d - '0')
				default:
					v |= uint8(d-'a') + 10
				}
			}
			*p = v
		}
		c.A = 0xff
		return c
	}

	light := map[string]string{"11": xrgb(lightBg), "10": xrgb(lightFg), "4;1": xrgb(red)}
	got := askPane(t, base, "e2e-light", host, light)
	for spec, want := range light {
		if got[spec] != want {
			t.Errorf("light host: a pane's OSC %s query was answered %q, want the host's %q", spec, got[spec], want)
		}
	}
	windowManagementMode(t, term)
	saveArtifact(t, term, artifacts, "light-host")
	renderScreenPNG(t, term.Screen(), hostGroundPalette(lightFg, lightBg), filepath.Join(artifacts, "light-host-terminal.png"))
	if depth.contrast {
		checkRailReads(t, term, rgb(lightBg), "light host", true)
	}
	captureScreenTo(t, term, shots, artifacts, "light-host")

	// switchTo has the host move to its next scheme, and waits until a pane
	// is told its background.
	switchTo := func(bg, fg string) {
		t.Helper()
		if err := syscall.Kill(term.Pid(), syscall.SIGUSR1); err != nil {
			t.Fatalf("signal hostterm: %v", err)
		}
		want := map[string]string{"11": xrgb(bg), "10": xrgb(fg)}
		got := askPane(t, base, "e2e-light", host, want)
		for spec, w := range want {
			if got[spec] != w {
				t.Errorf("after the switch to %s: a pane's OSC %s query was answered %q, want the host's %q", bg, spec, got[spec], w)
			}
		}
		logged, _ := os.ReadFile(hostLog)
		_, after, switched := strings.Cut(string(logged), "switch to "+bg)
		if !switched || !strings.Contains(after[:strings.Index(after, "\n")], "subscribed=true") {
			t.Errorf("tuios had not turned on mode 2031 when the host switched to %s:\n%s", bg, logged)
		} else if !strings.Contains(after, "answer 11 "+bg) {
			t.Errorf("tuios did not ask for the background again after the switch to %s:\n%s", bg, logged)
		}
	}
	// pillGround is the fill of the dock's workspace pill, which tells the
	// two ramps apart: the light ramp is built from the host's own ground, so
	// on a neutral grey its fill is a neutral grey, and the dark ramp is the
	// constant one, whose fill is not.
	pillGround := func() tuitest.Color {
		t.Helper()
		s := term.Screen()
		row, col, ok := textAt(s, "1", 5)
		if !ok || row != 0 {
			t.Fatalf("the dock's workspace pill is not on the top row\n%s", term.Snapshot())
		}
		return s.Cell(col, row).Bg
	}
	neutral := func(c tuitest.Color) bool {
		return c.Kind == tuitest.ColorRGB && c.R == c.G && c.G == c.B
	}

	// A mid grey after the light scheme: the chrome keeps its light ramp,
	// because a grey this close to the middle is inside the hysteresis band.
	// Judged afresh it would read as dark.
	switchTo(midBg, midFg)
	saveArtifact(t, term, artifacts, "grey-after-light")
	renderScreenPNG(t, term.Screen(), hostGroundPalette(midFg, midBg), filepath.Join(artifacts, "grey-after-light-terminal.png"))
	if pill := pillGround(); depth.ramp && !neutral(pill) {
		t.Errorf("on the mid grey %s after a light host the pill's fill is %+v, the dark ramp's; the light ramp should be held\n%s",
			midBg, pill, term.SnapshotStyled())
	}

	// The dark scheme.
	switchTo(darkBg, darkFg)
	// The client is still in window management mode, and the dock's notice
	// is not shown again for a mode it is already in, so only the rail is
	// measured here.
	saveArtifact(t, term, artifacts, "dark-host")
	renderScreenPNG(t, term.Screen(), hostGroundPalette(darkFg, darkBg), filepath.Join(artifacts, "dark-host-terminal.png"))
	if depth.contrast {
		checkRailReads(t, term, rgb(darkBg), "dark host", false)
	}
	darkPill := pillGround()
	captureScreenTo(t, term, shots, artifacts, "dark-host")

	// The same grey after the dark scheme: now the dark ramp is held.
	switchTo(midBg, midFg)
	saveArtifact(t, term, artifacts, "grey-after-dark")
	renderScreenPNG(t, term.Screen(), hostGroundPalette(midFg, midBg), filepath.Join(artifacts, "grey-after-dark-terminal.png"))
	if pill := pillGround(); depth.ramp && pill != darkPill {
		t.Errorf("on the mid grey %s after a dark host the pill's fill is %+v, not the dark ramp's %+v; the dark ramp should be held\n%s",
			midBg, pill, darkPill, term.SnapshotStyled())
	}
}

// TestHostColorsSilentTerminal attaches from a terminal that answers no colour
// question, which is what mosh does. tuios has to start as fast as it does on
// any other terminal, and with nothing learned it must invent nothing: a pane
// is told the emulator's own defaults, as before the host colour work.
//
// How this could pass wrongly: the questions could never be asked, so there is
// nothing to go unanswered. The host log has to show the background question
// arriving and going unanswered.
//
// This guards a fallback rather than a fix: a build without the host colour
// work passes it too, and that is the point.
func TestHostColorsSilentTerminal(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	host := buildHostTerm(t)
	artifacts := artifactDir(t)
	hostLog := filepath.Join(t.TempDir(), "host.log")

	useShippedLooks(base)
	if out, err := tuiosCLI(t, base, "new", "e2e-mute", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	began := time.Now()
	term := startIn(t, base, startOpts{
		args:         []string{"attach", "e2e-mute"},
		shippedLooks: true,
		env:          []string{"COLORTERM=truecolor"},
		wrap:         []string{host, "run", "-mute", "-log", hostLog, "--"},
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached behind a silent terminal: %v\n%s", err, term.Snapshot())
	}
	t.Logf("attached %v after start", time.Since(began).Round(time.Millisecond))

	want := map[string]string{"11": "rgb:0000/0000/0000", "10": "rgb:ffff/ffff/ffff"}
	got := askPane(t, base, "e2e-mute", host, want)
	for spec, w := range want {
		if got[spec] != w {
			t.Errorf("silent host: a pane's OSC %s query was answered %q, want the emulator's own %q", spec, got[spec], w)
		}
	}
	saveArtifact(t, term, artifacts, "silent-host")
	logged, _ := os.ReadFile(hostLog)
	if !strings.Contains(string(logged), "unanswered 11;?") {
		t.Errorf("tuios never asked the silent host for its background:\n%s", logged)
	}
}

// TestThemedDaemonPaneReportsTheTheme attaches to a daemon session with a
// theme set and the pane background off, behind the light stand-in host. A
// pane program asking OSC 11, OSC 10 and OSC 4 for slot 1 is told the
// theme's colours: the panes are drawn in the theme, so that is what they are
// on. The daemon's emulators answer these questions, and the daemon never
// loads a theme, so the client has to push the theme's colours.
//
// How this could pass wrongly: the answer could be the host's colours, or the
// emulator's own black and white. The host here is light and the theme is
// dark, so all three differ.
//
// Negative control: on a build where a themed client pushes nothing, every
// answer is the emulator's own (rgb:0000/0000/0000 for OSC 11), and the test
// fails.
func TestThemedDaemonPaneReportsTheTheme(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	host := buildHostTerm(t)
	artifacts := artifactDir(t)

	const themeID = "dracula"
	pal, fg, bg, _, ok := theme.Colors(themeID)
	if !ok {
		t.Fatalf("theme %s is not built in", themeID)
	}
	xrgb := func(c color.Color) string {
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("rgb:%04x/%04x/%04x", (r>>8)*0x101, (g>>8)*0x101, (b>>8)*0x101)
	}

	useShippedLooks(base)
	writeConfig(t, base, "[appearance]\ntheme = \""+themeID+"\"\n")
	if out, err := tuiosCLI(t, base, "new", "e2e-themed", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{
		args:         []string{"attach", "e2e-themed"},
		shippedLooks: true,
		env:          []string{"COLORTERM=truecolor"},
		wrap:         []string{host, "run", "-bg", "#fdf6e3", "-fg", "#2a2a2a", "-ansi", hostANSISpec(), "--"},
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}

	want := map[string]string{"11": xrgb(bg), "10": xrgb(fg), "4;1": xrgb(pal[1])}
	got := askPane(t, base, "e2e-themed", host, want)
	for spec, w := range want {
		if got[spec] != w {
			t.Errorf("themed daemon session: a pane's OSC %s query was answered %q, want the theme's %q", spec, got[spec], w)
		}
	}
	saveArtifact(t, term, artifacts, "themed-daemon-pane")

	// A second client on another theme attaches. The person last gave input
	// at the first one, so the second client's pushes leave the panes'
	// colours alone: two clients on two themes must not take turns, which
	// would send a pane with mode 2031 on a notice for every push.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("give the first client input: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	second := startIn(t, base, startOpts{
		args:         []string{"attach", "--theme", "catppuccin_latte", "e2e-themed"},
		shippedLooks: true,
		env:          []string{"COLORTERM=truecolor"},
		wrap:         []string{host, "run", "-bg", "#fdf6e3", "-fg", "#2a2a2a", "-ansi", hostANSISpec(), "--"},
	})
	if err := second.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the second client never attached: %v\n%s", err, second.Snapshot())
	}
	time.Sleep(time.Second)
	if got := askPane(t, base, "e2e-themed", host, map[string]string{"11": xrgb(bg)}); got["11"] != xrgb(bg) {
		t.Errorf("after a second client on Latte attached, a pane's OSC 11 was answered %q, want the first client's Dracula %q", got["11"], xrgb(bg))
	}
	// The positive half: once the person gives input at the second client
	// and its theme changes, its colours are the panes'.
	_, _, nordBg, _, ok := theme.Colors("nord")
	if !ok {
		t.Fatal("theme nord is not built in")
	}
	pickTheme(t, second, "nord")
	if got := askPane(t, base, "e2e-themed", host, map[string]string{"11": xrgb(nordBg)}); got["11"] != xrgb(nordBg) {
		t.Errorf("after the second client picked Nord, a pane's OSC 11 was answered %q, want Nord's %q", got["11"], xrgb(nordBg))
	}
	saveArtifact(t, second, artifacts, "second-client-nord")
}

// omarchySwitch applies an Omarchy theme under the test's HOME the way
// omarchy-theme-set does: the new theme is staged in next-theme, the old
// theme directory is removed, the staged one renamed into its place, and
// theme.generation and theme.name are written last. The palette is the
// shipped theme's colors.toml from swatch's fixtures.
//
// With TUIOS_E2E_OMARCHY_SRC naming an Omarchy checkout, the real
// omarchy-theme-set does the switch instead, headless.
func omarchySwitch(t *testing.T, base, id string) {
	t.Helper()
	home := xdgDir(base, "HOME")
	if src := os.Getenv("TUIOS_E2E_OMARCHY_SRC"); src != "" {
		run := filepath.Join(base, "omarchy-run")
		mustMkdir(run)
		cmd := exec.Command(filepath.Join(src, "bin", "omarchy-theme-set"), id)
		cmd.Env = append(os.Environ(), "HOME="+home, "OMARCHY_PATH="+src,
			"PATH="+filepath.Join(src, "bin")+":"+os.Getenv("PATH"),
			"OMARCHY_THEME_HEADLESS=1", "OMARCHY_THEME_SKIP_BACKGROUND=1", "XDG_RUNTIME_DIR="+run)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("omarchy-theme-set %s: %v\n%s", id, err, out)
		}
		return
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "swatch", "omarchy", "testdata", "themes", id, "colors.toml"))
	if err != nil {
		t.Fatalf("read the %s fixture: %v", id, err)
	}
	current := filepath.Join(home, ".local", "state", "omarchy", "current")
	next := filepath.Join(current, "next-theme")
	mustMkdir(next)
	if err := os.WriteFile(filepath.Join(next, "colors.toml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(current, "theme")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, filepath.Join(current, "theme")); err != nil {
		t.Fatal(err)
	}
	gen := fmt.Sprintf("%d-%d\n", os.Getpid(), time.Now().UnixNano())
	for name, body := range map[string]string{"theme.generation": gen, "theme.name": id + "\n"} {
		if err := os.WriteFile(filepath.Join(current, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestOmarchyThemeSwitchIsFollowed runs a daemon session on a machine with
// Omarchy and no tuios theme set, so tuios follows the Omarchy theme. It
// starts on Tokyo Night, then Omarchy switches to Gruvbox, another dark
// theme, and then to Catppuccin Latte, while the client runs.
//
// After each switch, with no restart and no key pressed:
//   - a program that turned on mode 2031 gets one CSI ? 997 n notice: dark
//     for Gruvbox, although the background stayed dark, and light for Latte.
//     For Gruvbox it gets exactly one, although the background, the text
//     colour and the palette all changed;
//   - a pane's OSC 11 is answered with the new background, and after Gruvbox
//     OSC 4 slot 4 with Gruvbox's blue, through the daemon's emulator, so the
//     colours went from the client to the daemon;
//   - the pane frame is drawn in a new colour.
//
// Then the theme picker passes several dark themes on the way to Dracula.
// The program gets no notice while the picker shows them, and one dark notice
// for the pick.
//
// Each state is kept as a frame drawn on the theme's ground, which is what
// the Omarchy terminal around tuios shows.
//
// How this could pass wrongly: the answer could be the first theme read
// again, but the three themes differ in every colour checked; the notice
// could come from somewhere else, but the program asks nothing, and before
// the change to every palette change tuios sent nothing for a dark to dark
// switch.
//
// Negative controls: with the watcher hub not started (startOmarchyFollow
// returning nil), the pane keeps answering Tokyo Night's background and no
// notice arrives. With the notice sent only on a dark and light flip, the
// Gruvbox step gets no notice.
func TestOmarchyThemeSwitchIsFollowed(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	host := buildHostTerm(t)
	artifacts := artifactDir(t)

	omarchySwitch(t, base, "tokyo-night")
	if out, err := tuiosCLI(t, base, "new", "e2e-omarchy", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{
		args: []string{"attach", "e2e-omarchy"},
		env:  []string{"COLORTERM=truecolor"},
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}

	const tokyoBg, latteBg = "rgb:1a1a/1b1b/2626", "rgb:efef/f1f1/f5f5"
	if got := askPane(t, base, "e2e-omarchy", host, map[string]string{"11": tokyoBg}); got["11"] != tokyoBg {
		t.Fatalf("on Tokyo Night a pane's OSC 11 was answered %q, want %q", got["11"], tokyoBg)
	}
	// frameInk is the colour of the pane frame's corner, and false when no
	// frame is on screen.
	frameInk := func(s tuitest.Screen) (tuitest.Color, bool) {
		row, col, ok := textAt(s, "╭", 0)
		if !ok {
			return tuitest.Color{}, false
		}
		return s.Cell(col, row).Fg, true
	}
	before, ok := frameInk(term.Screen())
	if !ok {
		t.Fatalf("no pane frame on screen\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifacts, "tokyo-night")
	renderScreenPNG(t, term.Screen(), hostGroundPalette("#a9b1d6", "#1a1b26"), filepath.Join(artifacts, "tokyo-night.png"))

	// A program that asks to be told when the colours change, waiting.
	waitPane := func(want string) string {
		t.Helper()
		var pane string
		deadline := time.Now().Add(shellTimeout)
		for time.Now().Before(deadline) {
			pane, _ = tuiosCLI(t, base, "capture-pane", "-s", "e2e-omarchy")
			if strings.Contains(pane, want) {
				return pane
			}
			time.Sleep(200 * time.Millisecond)
		}
		return pane
	}
	listen := func() {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "e2e-omarchy", "-l", "clear; "+host+" scheme\r"); err != nil {
			t.Fatalf("start the scheme listener: %v\n%s", err, out)
		}
		if pane := waitPane("HOSTTERM-SCHEME-WAITING"); !strings.Contains(pane, "HOSTTERM-SCHEME-WAITING") {
			t.Fatalf("the scheme listener never started:\n%s", pane)
		}
	}
	listen()

	// Dark to dark: the background stays dark, and the program is still
	// told, so it can ask for the new colours.
	omarchySwitch(t, base, "gruvbox")
	const gruvboxBg, gruvboxBlue = "rgb:2828/2828/2828", "rgb:7d7d/aeae/a3a3"
	if pane := waitPane("HOSTTERM-SCHEME-NOTICES="); !strings.Contains(pane, "HOSTTERM-SCHEME=dark") {
		t.Fatalf("after the switch to Gruvbox the program that turned on mode 2031 was not told dark:\n%s", pane)
	} else if m := regexp.MustCompile(`HOSTTERM-SCHEME-NOTICES=(\d+)`).FindStringSubmatch(pane); m == nil || m[1] != "1" {
		t.Errorf("the switch to Gruvbox did not send the program exactly one notice:\n%s", pane)
	}
	want := map[string]string{"11": gruvboxBg, "4;4": gruvboxBlue}
	got := askPane(t, base, "e2e-omarchy", host, want)
	for spec, w := range want {
		if got[spec] != w {
			t.Errorf("after the switch to Gruvbox a pane's OSC %s was answered %q, want Gruvbox's %q", spec, got[spec], w)
		}
	}
	saveArtifact(t, term, artifacts, "gruvbox")
	renderScreenPNG(t, term.Screen(), hostGroundPalette("#d4be98", "#282828"), filepath.Join(artifacts, "gruvbox.png"))
	listen()

	omarchySwitch(t, base, "catppuccin-latte")

	if pane := waitPane("HOSTTERM-SCHEME-NOTICES="); !strings.Contains(pane, "HOSTTERM-SCHEME=light") {
		t.Errorf("after the switch to Catppuccin Latte the program that turned on mode 2031 was not told light:\n%s", pane)
	}
	if got := askPane(t, base, "e2e-omarchy", host, map[string]string{"11": latteBg}); got["11"] != latteBg {
		t.Errorf("after the switch a pane's OSC 11 was answered %q, want Latte's %q", got["11"], latteBg)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		ink, ok := frameInk(s)
		return ok && ink != before
	}, configWatchTimeout); err != nil {
		t.Errorf("the pane frame kept its Tokyo Night colour %+v after the switch\n%s", before, term.SnapshotStyled())
	}
	saveArtifact(t, term, artifacts, "catppuccin-latte")
	renderScreenPNG(t, term.Screen(), hostGroundPalette("#4c4f69", "#eff1f5"), filepath.Join(artifacts, "catppuccin-latte.png"))

	// The theme picker shows each theme it passes, and a pane is not told
	// about a theme that is only shown. Typing "dracula" passes dark themes,
	// any of which would send a dark notice from Latte. Only the pick does.
	listen()
	name := filterThemePicker(t, term, "dracula")
	time.Sleep(1500 * time.Millisecond)
	if pane, _ := tuiosCLI(t, base, "capture-pane", "-s", "e2e-omarchy"); strings.Contains(pane, "HOSTTERM-SCHEME=") {
		t.Errorf("moving through the theme picker sent the program a notice:\n%s", pane)
	}
	closeThemePicker(t, term, name, tuitest.Enter)
	if pane := waitPane("HOSTTERM-SCHEME-NOTICES="); !strings.Contains(pane, "HOSTTERM-SCHEME=dark") {
		t.Errorf("picking Dracula did not tell the program dark:\n%s", pane)
	} else if m := regexp.MustCompile(`HOSTTERM-SCHEME-NOTICES=(\d+)`).FindStringSubmatch(pane); m == nil || m[1] != "1" {
		t.Errorf("picking Dracula did not send the program exactly one notice:\n%s", pane)
	}
}

// TestThemeNoneMatchesTheTerminal: with theme = "none", the dialogs are drawn
// on a ramp built from the terminal's own background, so a terminal themed
// elsewhere gets dialogs in its own greys. An empty theme keeps the constant
// ramp, as it did before.
//
// How this could pass wrongly: the constant ramp is a dark violet grey, and
// the host's ground here is a dark green no tuios ramp is near, so a green
// dialog surface came from the host. The empty-theme run is the control in
// the same test: the same host, the same dialog, and no green.
func TestThemeNoneMatchesTheTerminal(t *testing.T) {
	const hostBg, hostFg = "#0f2a14", "#d0e8d0"
	green := func(c tuitest.Color) bool {
		return c.Kind == tuitest.ColorRGB && int(c.G) > int(c.R)+8 && int(c.G) > int(c.B)+8
	}
	for _, tc := range []struct {
		name, theme string
		wantGreen   bool
	}{
		{"none", "none", true},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			host := buildHostTerm(t)
			writeConfig(t, base, "[appearance]\ntheme = \""+tc.theme+"\"\n")
			term := startIn(t, base, startOpts{
				cols: 120, rows: 40,
				env:  []string{"COLORTERM=truecolor"},
				wrap: []string{host, "run", "-bg", hostBg, "-fg", hostFg, "-ansi", hostANSISpec(), "--"},
			})
			waitBoot(t, term)
			sendKeys(t, term, legacyCtrlP)
			waitScreen(t, term, "the palette never opened", paletteTitle, "Run a program")
			s := term.Screen()
			row, col, ok := textAt(s, paletteTitle, 0)
			if !ok {
				t.Fatalf("no palette title on screen\n%s", term.Snapshot())
			}
			// The title sits in an accent chip. Two cells past its end is
			// the dialog's surface.
			surface := s.Cell(col+len(paletteTitle)+2, row).Bg
			saveArtifact(t, term, artifactDir(t), "palette")
			if green(surface) != tc.wantGreen {
				t.Fatalf("theme %q on a %s terminal: the palette's ground is %+v, want green %v\n%s",
					tc.theme, hostBg, surface, tc.wantGreen, term.SnapshotStyled())
			}
		})
	}
}

// TestThemePairFollowsTheTerminal: with appearance.theme_light set, a light
// terminal gets that theme and a dark one gets appearance.theme, and a switch
// of the terminal's scheme switches the theme. A pick in the theme picker then
// asks whether it is for the dark theme, the light one or both, and writes
// only the key asked for.
//
// How this could pass wrongly: the two reds are read off the host terminal
// for a probe printed after each switch, and each is checked against the red
// tuios theme export reports for that theme, so a red from the wrong theme,
// or the terminal's own, fails.
func TestThemePairFollowsTheTerminal(t *testing.T) {
	base := t.TempDir()
	host := buildHostTerm(t)
	writeConfig(t, base, "[appearance]\ntheme = \"nord\"\ntheme_light = \"catppuccin_latte\"\n")
	redOf := func(id string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, "theme", "export", id)
		if err != nil {
			t.Fatalf("export %s: %v\n%s", id, err, out)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatalf("export %s is not JSON: %v\n%s", id, err, out)
		}
		red, _ := m["red"].(string)
		return strings.ToLower(red)
	}
	lightRed, darkRed := redOf("catppuccin_latte"), redOf("nord")
	if lightRed == "" || lightRed == darkRed {
		t.Fatalf("the two themes' reds %q and %q cannot tell them apart", lightRed, darkRed)
	}

	term := startIn(t, base, startOpts{
		cols: 120, rows: 40,
		env: []string{"COLORTERM=truecolor"},
		wrap: []string{host, "run", "-bg", "#fdf6e3", "-fg", "#2a2a2a",
			"-alt-bg", "#1e1e2e", "-alt-fg", "#e0e0e0", "-ansi", hostANSISpec(), "--"},
	})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	hexOf := func(c tuitest.Color) string {
		if c.Kind != tuitest.ColorRGB {
			return fmt.Sprintf("%+v", c)
		}
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	// waitRed prints the probe until its red is want, within the usual wait.
	waitRed := func(when, want string) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for i := 0; ; i++ {
			marker := fmt.Sprintf("INK%s%d", strings.ToUpper(when[:1]), i)
			runInShell(t, term, probeCmd(marker[3:]), marker, shellTimeout)
			got := hexOf(probeInk(t, term, marker))
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: a pane's red reached the host as %s, want %s\n%s", when, got, want, term.Snapshot())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	waitRed("light", lightRed)
	saveArtifact(t, term, artifactDir(t), "light")

	if err := syscall.Kill(term.Pid(), syscall.SIGUSR1); err != nil {
		t.Fatalf("signal hostterm: %v", err)
	}
	waitRed("dark", darkRed)
	saveArtifact(t, term, artifactDir(t), "dark")

	// A pick with the pair set asks before it writes.
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('p'))
	waitScreen(t, term, "the palette never opened", paletteTitle)
	sendKeys(t, term, "Theme picker")
	waitScreen(t, term, "the palette never filtered", "Theme picker")
	sendKeys(t, term, tuitest.Enter)
	waitScreen(t, term, "the theme picker never opened", "themes")
	sendKeys(t, term, "dracula")
	waitScreen(t, term, "the picker never filtered", "dracula")
	sendKeys(t, term, tuitest.Enter)
	waitScreen(t, term, "the picker never asked which theme", "Use dracula as: d dark, l light, b both")
	saveArtifact(t, term, artifactDir(t), "pair-question")
	sendKeys(t, term, "l")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "themes") }, uiTimeout); err != nil {
		t.Fatalf("the picker did not close after l: %v\n%s", err, term.Snapshot())
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		cfg, _ := os.ReadFile(configPathIn(base))
		text := string(cfg)
		if strings.Contains(text, `theme_light = "dracula"`) && strings.Contains(text, `theme = "nord"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after l, config.toml does not hold theme_light = dracula and theme = nord:\n%s", text)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// An explicit --theme wins over the pair on a light terminal. The config
	// above now holds theme_light = dracula, so the flag names a third theme.
	//
	// Negative control, recorded in NEGATIVE_CONTROLS.md: dropping the
	// FlagTheme check in followSchemeTheme fails this with dracula's red.
	flagRed := redOf("gruvbox_dark")
	if flagRed == "" || flagRed == redOf("dracula") {
		t.Fatalf("the flag theme's red %q cannot be told from dracula's", flagRed)
	}
	term = startIn(t, base, startOpts{
		cols: 120, rows: 40,
		args: []string{"--theme", "gruvbox_dark"},
		env:  []string{"COLORTERM=truecolor"},
		wrap: []string{host, "run", "-bg", "#fdf6e3", "-fg", "#2a2a2a",
			"-alt-bg", "#1e1e2e", "-alt-fg", "#e0e0e0", "-ansi", hostANSISpec(), "--"},
	})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	waitRed("flag", flagRed)
	// The pair must not take over once the host's colours have all landed.
	time.Sleep(500 * time.Millisecond)
	waitRed("flag-later", flagRed)
	saveArtifact(t, term, artifactDir(t), "flag")
}
