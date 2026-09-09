package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The rail's polish gap, issues 165 to 178: each of these drives a real
// daemon and a real client and reads the behaviour off the screen, because a
// visual feature with only unit-level proof has burned this project before.

// railClient writes a config, creates a detached session named session with
// one window, attaches a client to it, and settles it in window-management
// mode. The rail is on and wide enough to read a sentence.
func railClient(t *testing.T, session, config string, o startOpts) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, config)
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create %s: %v: %s", session, err, out)
	}
	o.args = append([]string{"attach", session}, o.args...)
	term := startIn(t, base, o)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1 && strings.Contains(s.Text(), sidebarHeader)
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached with the rail up: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	return term, base
}

// railConfig is a rail on the left at width columns, with agent alerts not
// suppressed for the focused pane, so a test can read the toast for the pane
// it is typing into.
func railConfig(width int) string {
	return "[appearance.sidebar]\nenabled = true\nwidth = " + strconv.Itoa(width) + "\n\n[notifications.agent]\nsuppress_focused = false\nsettle_seconds = 0\n"
}

// TestBlockedAgentAlertCarriesTheQuestion (issue 165): a pane attributed to
// Claude Code paints a permission prompt and goes quiet. The screen rule reads
// the prompt line, and the toast and the rail's agent row both say what was
// asked, fronted by "approval:".
//
// Negative control: with the RulePrompt call in screenRuleMessage cut, the
// message is the manifest's fixed sentence and the first wait fails.
func TestBlockedAgentAlertCarriesTheQuestion(t *testing.T) {
	term, base := railClient(t, "e2e", railConfig(60), startOpts{cols: 140, rows: 40})
	renameWindow(t, term, "REVIEW")

	// The lowest-ranked claim there is, naming the harness: it gives the screen
	// tier rules to run and nothing that outranks it, which is the state an
	// unhooked harness sits in once the detector has seen its binary.
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "e2e", "-w", "REVIEW",
		"--source", "stall", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state failed: %v\n%s", err, out)
	}
	enterTerminalMode(t, term)
	runInShell(t, term,
		`printf 'Do you want to make this edit to main.go?\n\342\235\257 1. Yes\n  2. No, and tell Claude what to do differently (esc)\n'`,
		"2. No, and tell Claude", shellTimeout)

	// The dock cuts a long toast to its budget and the rail cuts the note to
	// its width, so both are matched on their opening words.
	const question = "approval: Do you want to make this edit"
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "REVIEW needs input · "+question)
	}, uiTimeout); err != nil {
		t.Fatalf("the toast never carried the question the agent asked: %v\n%s", err, term.Snapshot())
	}
	// The rail's two-line agent row carries it too, under the pane's name.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "claude · "+question)
	}, uiTimeout); err != nil {
		t.Fatalf("the agent row's note never carried the question: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "165-alert-question")
}

// TestFocusingAPaneScrollsTheRailToItsRow (issue 166): on a rail too short
// for its panes, selecting the last pane scrolls the terminals section so its
// row is on screen.
//
// Negative control: with the sidebarRevealFocus call cut from the render, the
// row stays below the fold and the wait fails.
func TestFocusingAPaneScrollsTheRailToItsRow(t *testing.T) {
	term, _ := railClient(t, "e2e", railConfig(28), startOpts{cols: 120, rows: 13})
	names := []string{"P1", "P2", "P3", "P4", "P5", "P6", "P7", "P8"}
	renameWindow(t, term, names[0])
	for _, name := range names[1:] {
		newWindow(t, term)
		renameWindow(t, term, name)
	}
	// Back to the first pane, so the section rests at its top.
	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("select the first pane: %v", err)
	}
	// The dock repeats every pane's name, so the rows are read off the rail's
	// own columns.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railRowOf(s, "P1") == railRowOf(s, "terminals")+1
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never settled on the first pane: %v\n%s", err, term.Snapshot())
	}
	if railRowOf(term.Screen(), "P8") >= 0 {
		t.Fatalf("the last pane's row is already on the rail; the rail is not short enough to test a reveal\n%s", term.Snapshot())
	}
	saveFrame(t, term, "166-before")

	if err := term.SendKeys("8"); err != nil {
		t.Fatalf("select the last pane: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := railRowOf(s, "P8")
		return r > railRowOf(s, "terminals")
	}, uiTimeout); err != nil {
		t.Fatalf("focusing the last pane did not scroll its row into the rail: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "166-reveal")
}

// TestAgentsHeaderShowsBlockedAndDone (issue 167): with one pane blocked and
// one finished unread, the expanded agents header says "1 blocked · 1 done".
//
// Negative control: with the count argument to sidebarAgentsControls cut to
// an empty readout, the header carries the tokens alone and the wait fails.
func TestAgentsHeaderShowsBlockedAndDone(t *testing.T) {
	term, base := railClient(t, "e2e", railConfig(50), startOpts{cols: 140, rows: 40})
	renameWindow(t, term, "REVIEW")
	newWindow(t, term)
	renameWindow(t, term, "BUILD")
	if out, err := tuiosCLI(t, base, "set-agent-state", "needs_input", "-s", "e2e", "-w", "REVIEW"); err != nil {
		t.Fatalf("set-agent-state failed: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "done", "-s", "e2e", "-w", "BUILD"); err != nil {
		t.Fatalf("set-agent-state failed: %v\n%s", err, out)
	}
	// BUILD is the focused pane, and focusing a finished pane is what marks it
	// seen; a done pane nobody has looked at is the one that counts, so move
	// to REVIEW, which is blocked and stays blocked when looked at.
	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("select the first pane: %v", err)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "e2e", "-w", "BUILD"); err != nil {
		t.Fatalf("set-agent-state failed: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "done", "-s", "e2e", "-w", "BUILD"); err != nil {
		t.Fatalf("set-agent-state failed: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		row := screenRowOf(s, "agents")
		return row >= 0 && strings.Contains(s.Line(row), "1 blocked · 1 done")
	}, uiTimeout); err != nil {
		t.Fatalf("the agents header never counted the blocked and done panes: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "167-header-count")
}

// TestAgentRowTokenTakesColourFromAValueRule (issue 175): a config rule on
// the name token colours the agent row whose name matches, and only that one.
//
// Negative control: with sidebarTokenStyle returning its base unchanged, the
// name draws in the rail's own ink and the colour wait fails.
func TestAgentRowTokenTakesColourFromAValueRule(t *testing.T) {
	cfg := railConfig(40) + `
[appearance.sidebar.agent_row]
tokens = ["name", "state"]

[[appearance.sidebar.agent_row.name.rule]]
contains = "REV"
fg = "#ff0000"
bold = true
`
	// A truecolor host, so the hex the rule names arrives as it was written
	// rather than stepped down to a 256-colour slot.
	term, base := railClient(t, "e2e", cfg, startOpts{cols: 140, rows: 40, env: []string{"COLORTERM=truecolor"}})
	renameWindow(t, term, "REVIEW")
	newWindow(t, term)
	renameWindow(t, term, "BUILD")
	// Both names on the rail before either is reported on, so the rows the
	// assertion reads are the rows the names were given.
	waitForAll(t, term, uiTimeout, "both renamed panes on the rail", "REVIEW", "BUILD")
	for _, w := range []string{"REVIEW", "BUILD"} {
		if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "e2e", "-w", w); err != nil {
			t.Fatalf("set-agent-state failed: %v\n%s", err, out)
		}
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "BUILD · working") && strings.Contains(s.Text(), "REVIEW · working")
	}, uiTimeout); err != nil {
		t.Fatalf("the agents section never listed both panes: %v\n%s", err, term.Snapshot())
	}
	red := tuitest.Color{Kind: tuitest.ColorRGB, R: 255}
	cellFg := func(s tuitest.Screen, name string, below int) (tuitest.Color, bool) {
		// The agents section lists the pane under its header; the terminals
		// section lists it too, higher up, so search below the agents header.
		agents := screenRowOf(s, "agents")
		if agents < 0 {
			return tuitest.Color{}, false
		}
		_, rows := s.Size()
		for r := agents + 1; r < rows; r++ {
			if c := strings.Index(s.Line(r), name); c >= 0 {
				return s.Cell(c, r).Fg, true
			}
		}
		return tuitest.Color{}, false
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		fg, ok := cellFg(s, "REVIEW", 0)
		return ok && fg == red && strings.Contains(s.Text(), "REVIEW · working")
	}, uiTimeout); err != nil {
		t.Fatalf("the rule never coloured the matching row's name red: %v\n%s", err, term.Snapshot())
	}
	if fg, ok := cellFg(term.Screen(), "BUILD", 0); !ok || fg == red {
		t.Fatalf("the rule coloured a row it does not match (found %v, fg %v)\n%s", ok, fg, term.Snapshot())
	}
	saveFrame(t, term, "175-token-rule")
}

// TestDividerDragMovesTheSplitAndPersists (issue 178): dragging the divider
// above the agents block down hides agent rows, the share lands in
// sidebar.json as section_split, and a double-click puts it back.
//
// Negative control: with the sidebarRowDivider case cut from SidebarClick,
// the press arms nothing, the divider stays where it was and the first wait
// fails.
func TestDividerDragMovesTheSplitAndPersists(t *testing.T) {
	term, base := railClient(t, "e2e", railConfig(28), startOpts{cols: 120, rows: 40})
	names := []string{"A1", "A2", "A3", "A4", "A5", "A6"}
	renameWindow(t, term, names[0])
	for _, name := range names[1:] {
		newWindow(t, term)
		renameWindow(t, term, name)
	}
	for _, name := range names {
		if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "e2e", "-w", name); err != nil {
			t.Fatalf("set-agent-state failed: %v\n%s", err, out)
		}
	}
	// The grip is three cells of the border glyph, which every pane's title
	// bar also draws, so the search is confined to the rail's columns. The six
	// reports land one at a time and the block grows with each, so the grip's
	// row is read only once the last pane is listed under the header.
	const grip = "───"
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := railRowOf(s, grip)
		agents := screenRowOf(s, "agents")
		return r >= 0 && agents == r+1 && railRowAfter(s, "A6", agents) > agents
	}, uiTimeout); err != nil {
		t.Fatalf("the divider never appeared above a full agents block: %v\n%s", err, term.Snapshot())
	}
	row := railRowOf(term.Screen(), grip)
	col := strings.Index(railLine(term.Screen(), row), grip)
	agentsBefore := screenRowOf(term.Screen(), "agents")
	saveFrame(t, term, "178-before")

	// Down four rows: the block gives up lines and its header moves down.
	mouseDrag(t, term, col+1, row, col+1, row+4, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenRowOf(s, "agents") > agentsBefore
	}, uiTimeout); err != nil {
		t.Fatalf("dragging the divider down did not move the agents block: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "178-dragged")

	statePath := filepath.Join(base, "XDG_STATE_HOME", "tuios", "sidebar.json")
	var st struct {
		SectionSplit int `json:"section_split"`
	}
	if err := term.WaitFor(func(tuitest.Screen) bool {
		data, err := os.ReadFile(statePath)
		return err == nil && json.Unmarshal(data, &st) == nil && st.SectionSplit > 0
	}, uiTimeout); err != nil {
		t.Fatalf("sidebar.json never recorded section_split: %v", err)
	}

	// A double-click on the divider resets it: the block comes back up and
	// the file forgets the share.
	row = railRowOf(term.Screen(), grip)
	col = strings.Index(railLine(term.Screen(), row), grip)
	mousePress(t, term, col+1, row, tuitest.MouseLeft, 0)
	mouseRelease(t, term, col+1, row, tuitest.MouseLeft, 0)
	mousePress(t, term, col+1, row, tuitest.MouseLeft, 0)
	mouseRelease(t, term, col+1, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenRowOf(s, "agents") == agentsBefore
	}, uiTimeout); err != nil {
		t.Fatalf("a double-click on the divider did not reset the split: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(tuitest.Screen) bool {
		data, err := os.ReadFile(statePath)
		return err == nil && !strings.Contains(string(data), "section_split")
	}, uiTimeout); err != nil {
		t.Fatalf("sidebar.json still carries section_split after the reset: %v", err)
	}
	saveFrame(t, term, "178-reset")
	alive(t, term, "after dragging the divider")
}

// railWidth is the rail's columns in the divider test: the configured 28,
// which the 120-column screen honours in full.
const railWidth = 28

// railLine is the rail's part of screen row r.
func railLine(s tuitest.Screen, r int) string {
	runes := []rune(s.Line(r))
	if len(runes) > railWidth {
		runes = runes[:railWidth]
	}
	return string(runes)
}

// railRowAfter is railRowOf starting below screen row after, or -1.
func railRowAfter(s tuitest.Screen, needle string, after int) int {
	_, rows := s.Size()
	for r := after + 1; r < rows; r++ {
		if strings.Contains(railLine(s, r), needle) {
			return r
		}
	}
	return -1
}

// railRowOf is screenRowOf confined to the rail's columns, or -1.
func railRowOf(s tuitest.Screen, needle string) int {
	_, rows := s.Size()
	for r := 0; r < rows; r++ {
		if strings.Contains(railLine(s, r), needle) {
			return r
		}
	}
	return -1
}
