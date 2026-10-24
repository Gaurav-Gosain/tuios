package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// cockpitFormDoc is the question an agent puts to the person: choices that
// set the state, and a button that closes the surface with them.
const cockpitFormDoc = `<tml version="1" title="Release check">
  <state>{"target": "none", "smoke": "off"}</state>
  <col pad="1" gap="1">
    <text>Ship <b>v0.11.0</b>? Target: <b>{state.target}</b>, smoke run: <b>{state.smoke}</b></text>
    <row gap="2">
      <button key="s" on-press="set('state.target', 'staging')">Staging</button>
      <button key="p" on-press="set('state.target', 'prod')">Prod</button>
      <button key="m" on-press="set('state.smoke', 'on')">Smoke run</button>
    </row>
    <button key="y" kind="primary" on-press="close(state.target + ' smoke ' + state.smoke)">Ship it</button>
  </col>
</tml>
`

// cockpitOtherDoc is a second agent's question, for the chip.
const cockpitOtherDoc = `<tml version="1" title="Pick a base">
  <col pad="1"><text>Rebase on main?</text><button key="y" on-press="close('yes')">Yes</button></col>
</tml>
`

// TestCockpitArtifacts is one TML form in two places. An agent's pane shows
// a form as a popup. Opening the agents view moves the form into the view,
// selected, with the keys, and the person makes choices in it there. A
// second agent's popup that arrives while the view is open puts a "new
// artifact" chip on its row and moves the row to Needs you; a click on the
// chip opens it in the view, where the person answers it. Closing the view
// lets the first form float again with its choices. The float toggle docks
// it as a pane and floats it again, the choices kept. A second client at 16
// colours shows the form in its view. The person submits the form from the
// view, the agent's --wait prints the answer, and the view lists the form as
// answered.
//
// Frames at 160x45, truecolor and 16 colours: the popup floating, the form
// in the view, the chip, floating again, docked, floating, the 16-colour
// view, and answered.
//
// Negative controls, in NEGATIVE_CONTROLS.md: upstream main (the popup draws
// under the view); the view not adopting the popup; the view not giving the
// popup its focus back as it closes; the float toggle refusing TML windows.
func TestCockpitArtifacts(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	const sess = "e2e-arts"
	const picked = "Target: staging, smoke run: on"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("new session: %v: %s", err, out)
	}
	for _, name := range []string{"planner", "reviewer", "docs"} {
		if out, err := tuiosCLI(t, base, "new-window", "-s", sess, name, "--grants", "write"); err != nil {
			t.Fatalf("open %s: %v\n%s", name, err, out)
		}
		if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", sess, "-w", name, "--harness", "claude-code", "-m", "Bash go test ./..."); err != nil {
			t.Fatalf("report %s: %v\n%s", name, err, out)
		}
	}
	truecolor := []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
	term := startIn(t, base, startOpts{args: []string{"attach", sess}, cols: 160, rows: 45, shippedLooks: true, env: truecolor})
	waitWindowCount(t, term, 4, "the four panes")

	form := writeTMLDoc(t, base, "form.tml", cockpitFormDoc)
	answer := filepath.Join(base, "answer.json")
	done := filepath.Join(base, "answer.done")
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "planner",
		fmt.Sprintf("%s tml show %s --wait --width 70 --height 14 > %s; echo $? > %s\n", tuiosBin, form, answer, done)); err != nil {
		t.Fatalf("send the show line: %v\n%s", err, out)
	}
	waitText(t, term, "the floating form", "Ship v0.11.0? Target: none", "dock")
	saveTMLFrame(t, term, "1-popup-floating")

	// The view opens on the form: the agent is selected and the form has
	// the keys.
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the form moved into the agents view", "You are in this surface.", "Ship v0.11.0? Target: none", "Artifacts 1")
	if err := term.SendKeys("s", "m"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the choices made in the view", picked)
	saveTMLFrame(t, term, "2-form-in-cockpit")

	// A second agent's popup, while the view is open on the first: a chip on
	// its row.
	other := writeTMLDoc(t, base, "other.tml", cockpitOtherDoc)
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "reviewer",
		fmt.Sprintf("%s tml show %s > /dev/null 2>&1\n", tuiosBin, other)); err != nil {
		t.Fatalf("send the second show: %v\n%s", err, out)
	}
	waitText(t, term, "the chip on the second agent's row", "new artifact")
	if row := lineWith(term.Screen(), "new artifact"); row < 0 || !chipAfter(term.Screen().Line(row), "reviewer", "planner") {
		t.Fatalf("ASSERTION: the chip is not on the reviewer's row\n%s", term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "Rebase on main?") {
		t.Fatalf("ASSERTION: the second agent's popup shows over the view\n%s", term.Snapshot())
	}
	// The popup waits for the person, so its agent moves to Needs you.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		chip, needs, working := lineWith(s, "new artifact"), lineWith(s, "Needs you  1"), lineWith(s, "Working  2")
		return needs >= 0 && chip > needs && chip < working
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the agent with the new popup is not in Needs you: %v\n%s", err, term.Snapshot())
	}
	// The row says why it needs the person: a form, not its working state.
	if row := lineWith(term.Screen(), "new artifact"); row < 0 || !strings.Contains(term.Screen().Line(row+1), "Form waiting") {
		t.Fatalf("ASSERTION: the row in Needs you does not say a form waits\n%s", term.Snapshot())
	}
	saveTMLFrame(t, term, "3-chip")

	// The chip opens the second agent's question in the view, and the
	// person answers it there.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the keys back in the view", "or a click to use it")
	cockpitClick(t, term, "new artifact")
	waitText(t, term, "the second question in the view", "Rebase on main?", "You are in this surface.")
	if err := term.SendKeys("y"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the second question answered", "You answered")

	// Back to the first agent, then out of the view: its form floats again
	// with the choices, in front.
	cockpitClick(t, term, "planner")
	waitText(t, term, "the first form selected again", picked, "or a click to use it")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, picked, "dock as a pane") && !strings.Contains(s.Text(), "or a click to use it")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the form did not float again with the choices: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "4-floating-again")

	// Dock it as a pane, then float it again: the choices stay, on the
	// daemon and on the screen.
	for i, docked := range []bool{true, false} {
		if err := term.SendKeys(tuitest.Ctrl('b'), "t", "f"); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			text := s.Text()
			if docked {
				return strings.Contains(text, "Surface: docked as a pane") && !strings.Contains(text, "dock as a pane ─") && strings.Contains(text, "Ship it")
			}
			return strings.Contains(text, "Surface: floating") && strings.Contains(text, "dock as a pane") && strings.Contains(text, picked)
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: toggle %d did not move the form (docked %v): %v\n%s", i+1, docked, err, term.Snapshot())
		}
		if got, err := tuiosCLI(t, base, "tml", "list", "-s", sess); err != nil || !strings.Contains(got, "Release check") {
			t.Fatalf("ASSERTION: the form is not open after toggle %d: %v\n%s", i+1, err, got)
		}
		name := "5-docked"
		if !docked {
			name = "6-floating"
		}
		saveTMLFrame(t, term, name)
	}

	// A second client at 16 colours shows the form in its view too.
	small := startIn(t, base, startOpts{args: []string{"attach", sess}, cols: 160, rows: 45, shippedLooks: true, env: []string{"TERM=xterm", "COLORTERM="}})
	waitText(t, small, "the second client", picked)
	// The leader works in either mode, and the focused popup is the
	// surface, which would take a mode key as its own.
	if err := small.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, small, "the form in the 16-colour view", "You are in this surface.", picked)
	// The form is seen now, so its agent goes back to Working.
	if err := small.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Needs you") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the seen form keeps its agent in Needs you: %v\n%s", err, small.Snapshot())
	}
	saveTMLFrame(t, small, "7-form-in-cockpit-16")
	if err := small.SendKeys(tuitest.Esc, tuitest.Esc); err != nil {
		t.Fatal(err)
	}

	// Submit from the view: the agent gets the choices.
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the form in the view again", "You are in this surface.", picked)
	if err := term.SendKeys("y"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		if _, err := os.Stat(done); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the agent never got an answer\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	got, _ := os.ReadFile(answer)
	code, _ := os.ReadFile(done)
	if strings.TrimSpace(string(code)) != "0" || !strings.Contains(string(got), "staging smoke on") {
		t.Fatalf("ASSERTION: the agent's --wait printed %q with status %q, want the choices and 0", got, code)
	}
	waitText(t, term, "the answered form in the list", "answered", "You answered")
	saveTMLFrame(t, term, "8-answered")
	alive(t, term, "after the artifacts")
}

// lineWith is the first screen row that holds want, -1 when none does.
func lineWith(s tuitest.Screen, want string) int {
	_, rows := s.Size()
	for y := range rows {
		if strings.Contains(s.Line(y), want) {
			return y
		}
	}
	return -1
}

// chipAfter reports whether the chip follows name on the line, with no
// other agent's name between them.
func chipAfter(line, name, other string) bool {
	at, chip := strings.Index(line, name), strings.Index(line, "new artifact")
	if at < 0 || chip < at {
		return false
	}
	return !strings.Contains(line[at:chip], other)
}

// cockpitClick clicks the first place text is drawn.
func cockpitClick(t *testing.T, term *tuitest.Terminal, text string) {
	t.Helper()
	col, row := tmlLocate(t, term, text)
	for _, a := range []tuitest.MouseAction{tuitest.MousePress, tuitest.MouseRelease} {
		if err := term.SendMouse(tuitest.MouseEvent{Col: col + 1, Row: row, Button: tuitest.MouseLeft, Action: a}); err != nil {
			t.Fatal(err)
		}
	}
}
