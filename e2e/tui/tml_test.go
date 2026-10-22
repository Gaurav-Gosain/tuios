package tuie2e

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// tmlRunDoc is the document the TML tests show: tabs, a table whose enter
// writes the picked row into the state, a button that sets a number, and one
// that closes with a value.
const tmlRunDoc = `<tml version="1" title="Test run">
  <state>{"tab": "sum", "picked": "none", "passed": 412, "failed": 4, "failures": [
    {"pkg": "internal/session", "test": "TestAttachRace", "ms": 2210},
    {"pkg": "internal/session", "test": "TestAttachReplay", "ms": 1950},
    {"pkg": "internal/app", "test": "TestDockClick", "ms": 840},
    {"pkg": "vt", "test": "TestKittyPlaceholder", "ms": 120}]}</state>
  <col pad="1" gap="1">
    <tabs id="tabs" value="state.tab">
      <tab id="sum" title="Summary">
        <col gap="1">
          <row gap="3">
            <stat label="passed" value="{state.passed}" role="success"/>
            <stat label="failed" value="{state.failed}" role="error"/>
            <spacer/>
            <gauge value="{pct(state.passed, state.passed + state.failed)}" role="success" w="24"/>
          </row>
          <card title="Failures" meta="{len(state.failures)}" role="error">
            <table id="failures" rows="state.failures" key="test" sort="ms:desc" select="single" on-select="set('state.picked', event.row.test)">
              <column field="pkg" title="Package" w="fill"/>
              <column field="test" title="Test" w="fill:2"/>
              <column field="ms" title="Time" align="right" w="8" format="dur(value, 'ms')"/>
            </table>
          </card>
          <text>Picked: <b>{state.picked}</b></text>
        </col>
      </tab>
      <tab id="log" title="Log"><code>ok   tuios.dev/vt          12.1s
FAIL internal/session    LOGLINE-TestAttachRace</code></tab>
    </tabs>
    <spacer/>
    <row gap="2">
      <button key="f" on-press="set('state.failed', 0)">Mark fixed</button>
      <button key="q" kind="primary" on-press="close('rerun')">Rerun and close</button>
      <spacer/>
      <text role="mute"><kbd>enter</kbd> pick   <kbd>tab</kbd> next   <kbd>esc</kbd> close</text>
    </row>
  </col>
</tml>
`

// writeTMLDoc writes a document under base and returns its path.
func writeTMLDoc(t *testing.T, base, name, doc string) string {
	t.Helper()
	path := filepath.Join(base, name)
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// saveTMLFrame keeps a frame of the screen, as text and as PNG, in the test's
// artifact folder.
func saveTMLFrame(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	dir := artifactDir(t)
	saveArtifact(t, term, dir, name)
	savePNG(t, term.Screen(), hostPalette(t, ""), dir, name)
}

// tmlLocate finds text on the screen and returns its column and row.
func tmlLocate(t *testing.T, term *tuitest.Terminal, text string) (int, int) {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for y := range rows {
		if x := strings.Index(s.Line(y), text); x >= 0 {
			return len([]rune(s.Line(y)[:x])), y
		}
	}
	t.Fatalf("%q is not on the screen:\n%s", text, term.Snapshot())
	return 0, 0
}

// TestTMLShowPopup is tuios tml show from end to end: the CLI opens a popup,
// the client draws the surface with loom, keys and a click reach it, a local
// set() reaches the daemon and survives a detach and a reattach, and a
// close() value comes back to the waiting CLI.
//
// Negative controls, in NEGATIVE_CONTROLS.md: without the render hook the
// popup shows the placeholder line; without the key routing the picked row
// stays "none"; without the attach push the reattached popup waits for the
// surface.
func TestTMLShowPopup(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-tml", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	opts := startOpts{cols: 120, rows: 36, args: []string{"attach", "e2e-tml"}}
	term := startIn(t, base, opts)
	waitWindowCount(t, term, 1, "the first pane")

	doc := writeTMLDoc(t, base, "run.tml", tmlRunDoc)
	shown := startCLIBackground(t, base, "tml", "show", doc, "--wait", "-s", "e2e-tml", "--width", "90", "--height", "24")

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Summary", "412", "TestAttachRace", "Picked: none", "Rerun and close")
	}, uiTimeout); err != nil {
		t.Fatalf("the popup never drew the surface: %v\n%s", err, term.Snapshot())
	}
	waitWindowCount(t, term, 2, "with the popup open")
	saveTMLFrame(t, term, "1-popup")

	// The keys reach the surface: the table has the focus, down moves its
	// cursor to the second row by time, and enter runs its on-select, a
	// set() of the picked row.
	for _, key := range []tuitest.Key{tuitest.Down, tuitest.Enter} {
		if err := term.SendKeys(key); err != nil {
			t.Fatalf("send %q: %v", key, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := term.WaitForText("Picked: TestAttachReplay", uiTimeout); err != nil {
		t.Fatalf("the keys never picked the second row: %v\n%s", err, term.Snapshot())
	}
	// A button's key works from anywhere on the surface.
	if err := term.SendKeys("f"); err != nil {
		t.Fatalf("send f: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, "0 failed") }, uiTimeout); err != nil {
		t.Fatalf("the f button never set failed to 0: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "2-picked")

	// A click on a tab title switches the tab.
	col, row := tmlLocate(t, term, "Log")
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("LOGLINE-TestAttachRace", uiTimeout); err != nil {
		t.Fatalf("a click on the Log tab never showed the log: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "3-log-tab")
	col, row = tmlLocate(t, term, "Summary")
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Picked: TestAttachReplay", uiTimeout); err != nil {
		t.Fatalf("a click on the Summary tab never came back: %v\n%s", err, term.Snapshot())
	}

	// Detach. The daemon keeps the surface, with the state the person set.
	if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("send leader d: %v", err)
	}
	waitExit(t, term, "after leader d")

	again := startIn(t, base, opts)
	if err := again.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Picked: TestAttachReplay", "TestAttachRace", "0 failed", "Rerun and close")
	}, bootTimeout); err != nil {
		t.Fatalf("the reattached client did not draw the surface with its state: %v\n%s", err, again.Snapshot())
	}
	saveTMLFrame(t, again, "4-reattached")

	// The primary button closes the surface with its value, which the
	// waiting command prints.
	if err := again.SendKeys("q"); err != nil {
		t.Fatalf("send q: %v", err)
	}
	select {
	case run := <-shown:
		if run.err != nil {
			t.Fatalf("tuios tml show --wait failed: %v\n%s", run.err, run.out)
		}
		if got := strings.TrimSpace(run.out); got != `"rerun"` {
			t.Fatalf("tuios tml show --wait printed %q, want \"rerun\"", got)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("tuios tml show --wait never returned after q\n%s", again.Snapshot())
	}
	waitWindowCount(t, again, 1, "after the surface closed")
	alive(t, again, "after the surface closed")
}

// TestTMLShowPaneAndClose opens a surface as a tiled pane, which list-windows
// reports as kind tml, and closes it from the command line with a value. An
// esc in a pane does not close it, since only a popup closes on esc.
func TestTMLShowPaneAndClose(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-tmlpane", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 36, args: []string{"attach", "e2e-tmlpane"}})
	waitWindowCount(t, term, 1, "the first pane")

	doc := writeTMLDoc(t, base, "run.tml", tmlRunDoc)
	out, err := tuiosCLI(t, base, "tml", "show", doc, "--pane", "--json", "-s", "e2e-tmlpane")
	if err != nil {
		t.Fatalf("tuios tml show --pane: %v\n%s", err, out)
	}
	var opened struct {
		Surface  string `json:"surface"`
		WindowID string `json:"window_id"`
		Target   string `json:"target"`
	}
	if err := json.Unmarshal([]byte(out), &opened); err != nil || opened.Surface == "" || opened.Target != "pane" {
		t.Fatalf("tuios tml show --pane printed %q (%v)", out, err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Summary", "TestAttachRace", "Picked: none")
	}, uiTimeout); err != nil {
		t.Fatalf("the pane never drew the surface: %v\n%s", err, term.Snapshot())
	}
	waitWindowCount(t, term, 2, "with the pane open")
	saveTMLFrame(t, term, "1-pane")

	listed, err := tuiosCLI(t, base, "list-windows", "-s", "e2e-tmlpane", "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, listed)
	}
	if !strings.Contains(listed, `"kind":"tml"`) && !strings.Contains(listed, `"kind": "tml"`) {
		t.Fatalf("list-windows does not report the pane as kind tml:\n%s", listed)
	}

	// The keys reach the pane's surface, and an esc that follows them does
	// not close it.
	for _, key := range []tuitest.Key{tuitest.Down, tuitest.Enter} {
		if err := term.SendKeys(key); err != nil {
			t.Fatalf("send %q: %v", key, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := term.WaitForText("Picked: TestAttachReplay", uiTimeout); err != nil {
		t.Fatalf("the keys never reached the pane's surface: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if n := countWindows(term.Screen()); n != 2 {
		t.Fatalf("esc closed the pane (%d windows)\n%s", n, term.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "tml", "close", opened.Surface, `"done"`); err != nil {
		t.Fatalf("tuios tml close: %v\n%s", err, out)
	}
	waitWindowCount(t, term, 1, "after tuios tml close")
	if out, err := tuiosCLI(t, base, "tml", "close", opened.Surface); err == nil {
		t.Fatalf("a second close of the same surface succeeded:\n%s", out)
	} else if !strings.Contains(out, "no open surface") {
		t.Fatalf("a second close did not say the surface is gone:\n%s", out)
	}
	alive(t, term, "after the pane closed")
}

// TestTMLShowRefusesABadDocument checks that the command names each problem
// and the fix, and that nothing opens.
func TestTMLShowRefusesABadDocument(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	doc := writeTMLDoc(t, base, "bad.tml", `<tml><chart-bar data="state.x"/><text role="#ff0000">x</text></tml>`)
	out, err := tuiosCLI(t, base, "tml", "show", doc)
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		t.Fatalf("tuios tml show of a bad document did not fail: %v\n%s", err, out)
	}
	for _, want := range []string{`Did you mean "bar"?`, "unknown_component", "hex_color"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
}

// tmlSurfaceOf waits for list-windows to name a surface in session and
// returns it with its window.
func tmlSurfaceOf(t *testing.T, base, sess string) (string, string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
		var res struct {
			Windows []struct {
				WindowID string `json:"window_id"`
				Surface  string `json:"surface"`
			} `json:"windows"`
		}
		if err == nil && json.Unmarshal([]byte(out), &res) == nil {
			for _, w := range res.Windows {
				if w.Surface != "" {
					return w.Surface, w.WindowID
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("list-windows of %s never named a surface", sess)
	return "", ""
}

// TestTMLProgramCloseIsNotThePersons checks that a waiting tml show can tell
// a program's tml close from the person's answer: it prints the value and
// exits with status 2. It also checks that a name with escapes is stored
// without them, and that a TML pane cannot move to another session, where
// its surface would not follow it.
func TestTMLProgramCloseIsNotThePersons(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, name := range []string{"e2e-tmlprog", "e2e-tmlother"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}
	doc := writeTMLDoc(t, base, "run.tml", tmlRunDoc)
	shown := startCLIBackground(t, base, "tml", "show", doc, "--pane", "--wait", "-s", "e2e-tmlprog",
		"--name", "Run\x1b]0;PWNED\x07\x1b[41mName")
	surface, window := tmlSurfaceOf(t, base, "e2e-tmlprog")

	listed, err := tuiosCLI(t, base, "list-windows", "-s", "e2e-tmlprog", "--json")
	if err != nil || strings.Contains(listed, `\u001b`) || strings.Contains(listed, `\u0007`) || !strings.Contains(listed, `"custom_name": "from tuios tml show · Run]0;PWNED[41mName"`) {
		t.Fatalf("the window name kept its control characters, or lost its text: %v\n%s", err, listed)
	}

	out, err := tuiosCLI(t, base, "move-window", "-s", "e2e-tmlprog", "-w", window, "--to-session", "e2e-tmlother")
	if err == nil || !strings.Contains(out, "TML surface") {
		t.Fatalf("a TML pane moved to another session: %v\n%s", err, out)
	}

	if out, err := tuiosCLI(t, base, "tml", "close", surface, `"yes"`); err != nil {
		t.Fatalf("tml close: %v\n%s", err, out)
	}
	select {
	case run := <-shown:
		var exit *exec.ExitError
		if !errors.As(run.err, &exit) || exit.ExitCode() != 2 {
			t.Fatalf("tml show --wait ended with %v after a program's close, want exit status 2\n%s", run.err, run.out)
		}
		if got := strings.TrimSpace(run.out); got != `"yes"` {
			t.Fatalf("tml show --wait printed %q, want \"yes\"", got)
		}
	case <-time.After(uiTimeout):
		t.Fatal("tml show --wait never returned after tml close")
	}
}

// TestTMLSurfaceThroughAHostShowsThePlaceholder attaches through another
// machine, where the person's input on a surface could not reach the
// surface's daemon. The client must not draw the surface or take its keys: it
// shows the placeholder, which says what to do, and q on it dismisses the
// surface on the far machine.
func TestTMLSurfaceThroughAHostShowsThePlaceholder(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	env := hubWithBuild(t, base, remote)
	term := startIn(t, base, startOpts{cols: 120, rows: 36, args: []string{"attach", "--host", "build", "far"}, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("never attached through build: %v\n%s", err, term.Snapshot())
	}
	doc := writeTMLDoc(t, remote, "run.tml", tmlRunDoc)
	shown := startCLIBackground(t, remote, "tml", "show", doc, "--wait", "-s", "far", "--width", "90", "--height", "24")
	if err := term.WaitForText("Open this session on its own machine to use it.", uiTimeout); err != nil {
		t.Fatalf("the far surface did not show its placeholder line: %v\n%s", err, term.Snapshot())
	}
	if screenHas(term.Screen(), "Picked: none") {
		t.Fatalf("a client attached through a host drew a surface it cannot answer for\n%s", term.Snapshot())
	}
	saveTMLFrame(t, term, "1-through-a-host")
	// q on the placeholder closes the window, which dismisses the surface
	// on the far machine.
	if err := term.SendKeys("q"); err != nil {
		t.Fatalf("send q: %v", err)
	}
	select {
	case run := <-shown:
		var exit *exec.ExitError
		if !errors.As(run.err, &exit) || exit.ExitCode() != 1 || strings.TrimSpace(run.out) != "null" {
			t.Fatalf("the far tml show --wait ended with %v %q, want null and exit status 1", run.err, run.out)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("q on the placeholder did not close the far surface\n%s", term.Snapshot())
	}

	// esc does the same, and a click does not: the mouse report that starts
	// with an esc is told apart.
	again := startCLIBackground(t, remote, "tml", "show", doc, "--wait", "-s", "far", "--width", "90", "--height", "24")
	if err := term.WaitForText("Press q to close it.", uiTimeout); err != nil {
		t.Fatalf("the second far surface did not show its placeholder: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	col, row := tmlLocate(t, term, "Press q to close it.")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	time.Sleep(500 * time.Millisecond)
	select {
	case run := <-again:
		t.Fatalf("a click closed the far surface: %v %q", run.err, run.out)
	default:
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	select {
	case run := <-again:
		if strings.TrimSpace(run.out) != "null" {
			t.Fatalf("esc on the placeholder ended the wait with %q", run.out)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("esc on the placeholder did not close the far surface\n%s", term.Snapshot())
	}
}
