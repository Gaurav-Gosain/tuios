package tuie2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestAgentsViewTextMeetsItsFloor opens the agents view's reply area, closed
// and open with a draft, and the New agent sheet, under three looks at 256
// colours and truecolor, and measures every glyph against the ground it is
// drawn on, from the screen's own cells:
//
//   - in the reply area, a key of a hint ("@", "ctrl+v", "esc") clears 3:1,
//     any other text 4.5:1, and the draft's words 7:1;
//   - in the sheet, every glyph clears 3:1 and the task's placeholder 4.5:1.
//
// The person's frame showed the hint words "mention", "image" and "new line"
// grey on the grey field. They were drawn in the quiet ink, which the palette
// holds to 3:1 only, as text.
//
// How this could pass wrongly, written down first:
//   - The rows read could be the thread's, above the area. The area is found
//     by "Reply to" and the row of the Send button, and only the focus
//     column from "Reply to" right is read.
//   - A cell in the default colour cannot be measured honestly. Each run
//     resolves it in the host palette the PNG uses: the xterm defaults for
//     the dark look and the theme's own colours for a themed one.
//   - At 256 colours the ink is a palette entry, not the truecolor value
//     Derive measured. The cells hold the entry, and the entry's RGB is what
//     is measured.
//   - Which agents are installed changes what the sheet draws. The client
//     runs with a PATH that holds none, as CI's runner has none.
//   - At 16 colours there is no RGB to measure. Those runs are not in the
//     matrix; chrome_depth_test holds the 16-colour chrome.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: on main every run
// fails; with the reply area's labels drawn in FgMute again, the run fails on
// "Reply to" and the hint words.
func TestAgentsViewTextMeetsItsFloor(t *testing.T) {
	looks := []chromeLook{lookDark, {name: "rose_pine", theme: "rose_pine"}, lookLatte}
	for _, look := range looks {
		for _, d := range chromeDepths[1:] {
			run := chromeRun{look: look, depth: d, cols: 120, rows: 36}
			t.Run(run.name(), func(t *testing.T) { agentsViewContrast(t, run) })
		}
	}
}

func agentsViewContrast(t *testing.T, run chromeRun) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	conf := "[screenshot]\nfont_family = \"JetBrainsMono Nerd Font Mono\"\n"
	if run.look.theme != "" {
		conf = "[appearance]\ntheme = \"" + run.look.theme + "\"\n\n" + conf
	}
	writeConfig(t, base, conf)
	for _, s := range []string{"home", "docs"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	for _, args := range [][]string{
		{"set-window", "-s", "docs", "-w", "0", "--name", "docs-cli"},
		{"set-agent-state", "idle", "-s", "docs", "-w", "docs-cli", "--harness", "claude-code"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: run.cols, rows: run.rows, shippedLooks: true,
		// No agent on the PATH: the sheet draws an agent that is not
		// installed in its quiet ink, and the machine row warns. A machine
		// with claude installed showed neither, and CI, with none, did.
		env: append([]string{"TUIOS_KITTY_GRAPHICS=0", "PATH=" + agentFreePath("/usr/bin", "/bin")}, run.depth.env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "the closed reply area", "Reply to docs-cli", "Write a reply.", "Send")
	_ = term.WaitStable(uiTimeout)

	host := hostPalette(t, run.look.theme)
	dir := artifactDir(t)
	keys := []string{"@", "ctrl+v", "ctrl+j", "alt+" + overlay.EnterGlyph, "ctrl+s", "esc", "tab", overlay.EnterKey()}

	shoot := func(name string) tuitest.Screen {
		s := term.Screen()
		saveArtifact(t, term, dir, name)
		savePNG(t, s, host, dir, name)
		return s
	}

	s := shoot("reply-closed")
	checkReplyArea(t, term, s, run, host, keys, "")

	row, col := findText(t, term, "Write a reply.")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitText(t, term, "the open reply editor", "Type your reply.")
	_ = term.WaitStable(uiTimeout)
	s = shoot("reply-placeholder")
	checkReplyArea(t, term, s, run, host, keys, "")

	draft := "please rebase on main and rerun the tests"
	sendKeys(t, term, draft)
	waitText(t, term, "the typed draft", draft)
	_ = term.WaitStable(uiTimeout)
	s = shoot("reply-draft")
	checkReplyArea(t, term, s, run, host, keys, draft)

	// The draft is kept; the sheet opens from the list.
	sendKeys(t, term, tuitest.Esc)
	waitText(t, term, "the closed area with the draft", "Draft: ")
	sendKeys(t, term, "n")
	waitText(t, term, "the New agent sheet", "New agent", "What should the agent do?", "Start")
	_ = term.WaitStable(uiTimeout)
	s = shoot("new-agent")
	checkSheet(t, term, s, run, host)
	t.Logf("frames in %s", dir)
}

// inkFails gathers the glyphs under their floor, one line for each role,
// ink and ground, so the message names each place to fix once.
type inkFails struct {
	seen  map[string]bool
	lines []string
	n     int
}

func (f *inkFails) add(role string, c inkCell, floor float64, line string) {
	f.n++
	k := fmt.Sprint(role, c.fg, c.bg)
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[k] {
		return
	}
	f.seen[k] = true
	f.lines = append(f.lines, fmt.Sprintf("%s %q at (%d,%d) %.2f:1 under %.1f, %v on %v, in %q", role, c.text, c.x, c.y, c.ratio, floor, c.fg, c.bg, strings.TrimSpace(line)))
}

func (f *inkFails) report(t *testing.T, term *tuitest.Terminal, where string) {
	t.Helper()
	if f.n > 0 {
		t.Errorf("ASSERTION: %d glyphs in %s miss their floor, %d kinds:\n  %s\n%s", f.n, where, len(f.lines), strings.Join(f.lines, "\n  "), term.SnapshotStyled())
	}
}

// inkCell is one measured glyph.
type inkCell struct {
	x, y  int
	text  string
	ratio float64
	fg    any
	bg    any
}

// measureRow measures every glyph of row y from x0 to x1, and returns them
// with the row's text, one rune per cell, so a string's index is its column
// offset from x0.
func measureRow(s tuitest.Screen, y, x0, x1 int, run chromeRun, host *shot.Palette) ([]inkCell, string) {
	var cells []inkCell
	var line strings.Builder
	for x := x0; x <= x1; x++ {
		c := s.Cell(x, y)
		content := c.Content
		if c.Width == 0 || content == "" {
			content = " "
		}
		line.WriteString(firstRune(content))
		if strings.TrimSpace(c.Content) == "" || inkExempt(c.Content) {
			continue
		}
		fg, bg := cellColour(c.Fg, host, host.FG), cellColour(c.Bg, host, host.BG)
		if c.Reverse {
			fg, bg = bg, fg
		}
		if structureInk(fg, bg, run.depth.name) {
			continue
		}
		cells = append(cells, inkCell{x: x, y: y, text: c.Content, ratio: overlay.ContrastRatio(fg, bg), fg: fg, bg: bg})
	}
	return cells, line.String()
}

// firstRune is the first rune of a cell's cluster, so a row reads one rune a
// cell.
func firstRune(s string) string {
	for _, r := range s {
		return string(r)
	}
	return " "
}

// spansOf marks the columns, as offsets into line, that any of words covers
// where it stands as a word of its own.
func spansOf(line string, words []string) map[int]bool {
	out := map[int]bool{}
	runes := []rune(line)
	for _, w := range words {
		wr := []rune(w)
		for i := 0; i+len(wr) <= len(runes); i++ {
			if string(runes[i:i+len(wr)]) != w {
				continue
			}
			if i > 0 && runes[i-1] != ' ' {
				continue
			}
			if end := i + len(wr); end < len(runes) && runes[end] != ' ' {
				continue
			}
			for j := i; j < i+len(wr); j++ {
				out[j] = true
			}
		}
	}
	return out
}

// checkReplyArea measures the reply area: from the "Reply to" row down to
// the row of the Send button, from "Reply to" to the right edge.
func checkReplyArea(t *testing.T, term *tuitest.Terminal, s tuitest.Screen, run chromeRun, host *shot.Palette, keys []string, draft string) {
	t.Helper()
	top := rowWith(s, "Reply to docs-cli")
	bottom := -1
	_, rows := s.Size()
	for y := max(top, 0); y < rows; y++ {
		if strings.Contains(s.Line(y), "Send") {
			bottom = y
		}
	}
	if top < 0 || bottom < 0 {
		t.Fatalf("ASSERTION: no reply area on screen\n%s", term.Snapshot())
	}
	x0 := strings.Index(s.Line(top), "Reply to")
	x0 = len([]rune(s.Line(top)[:x0]))
	cols, _ := s.Size()
	var fails inkFails
	for y := top; y <= bottom; y++ {
		cells, line := measureRow(s, y, x0, cols-1, run, host)
		keyAt := spansOf(line, keys)
		draftAt := map[int]bool{}
		if draft != "" {
			if before, _, ok := strings.Cut(line, draft); ok {
				for j := range len([]rune(draft)) {
					draftAt[len([]rune(before))+j] = true
				}
			}
		}
		for _, c := range cells {
			floor, role := overlay.ContrastFloor, "text"
			switch {
			case keyAt[c.x-x0]:
				floor, role = overlay.MarkFloor, "key"
			case draftAt[c.x-x0]:
				floor, role = 7, "draft"
			}
			if c.ratio < floor {
				fails.add(role, c, floor, line)
			}
		}
	}
	fails.report(t, term, "the reply area")
}

// checkSheet measures the New agent sheet: every glyph from the title row to
// the Start row clears 3:1, and the task's placeholder 4.5:1.
func checkSheet(t *testing.T, term *tuitest.Terminal, s tuitest.Screen, run chromeRun, host *shot.Palette) {
	t.Helper()
	// The header's "+ New agent" button is above the sheet: the sheet's
	// title is the row that also holds its way out.
	top, bottom := -1, rowWith(s, "Start agent")
	_, rows := s.Size()
	for y := range rows {
		if l := s.Line(y); strings.Contains(l, "New agent") && strings.Contains(l, "esc close") {
			top = y
			break
		}
	}
	if top < 0 || bottom < 0 {
		t.Fatalf("ASSERTION: no sheet on screen\n%s", term.Snapshot())
	}
	line := s.Line(top)
	x0 := len([]rune(line[:strings.Index(line, "New agent")]))
	end := s.Line(bottom)
	x1 := len([]rune(end[:strings.LastIndex(end, "Start agent")])) + len("Start agent") + 2
	var fails inkFails
	for y := top; y <= bottom; y++ {
		cells, text := measureRow(s, y, x0, x1, run, host)
		ph := spansOf(text, []string{"What should the agent do?"})
		for _, c := range cells {
			floor, role := overlay.MarkFloor, "glyph"
			if ph[c.x-x0] {
				floor, role = overlay.ContrastFloor, "placeholder"
			}
			if c.ratio < floor {
				fails.add(role, c, floor, text)
			}
		}
	}
	fails.report(t, term, "the sheet")
}
