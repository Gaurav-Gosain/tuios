package tuie2e

import (
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
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuitest"
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

// hostPalette is the palette the host draws with on a given ground.
func hostPalette(fg, bg string) *shot.Palette {
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
	p := hostPalette("#000000", "#000000")
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
	data, err := shot.RenderPNG(g, nil, nil)
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
	renderScreenPNG(t, term.Screen(), hostPalette(lightFg, lightBg), filepath.Join(artifacts, "light-host-terminal.png"))
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
	renderScreenPNG(t, term.Screen(), hostPalette(midFg, midBg), filepath.Join(artifacts, "grey-after-light-terminal.png"))
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
	renderScreenPNG(t, term.Screen(), hostPalette(darkFg, darkBg), filepath.Join(artifacts, "dark-host-terminal.png"))
	if depth.contrast {
		checkRailReads(t, term, rgb(darkBg), "dark host", false)
	}
	darkPill := pillGround()
	captureScreenTo(t, term, shots, artifacts, "dark-host")

	// The same grey after the dark scheme: now the dark ramp is held.
	switchTo(midBg, midFg)
	saveArtifact(t, term, artifacts, "grey-after-dark")
	renderScreenPNG(t, term.Screen(), hostPalette(midFg, midBg), filepath.Join(artifacts, "grey-after-dark-terminal.png"))
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
