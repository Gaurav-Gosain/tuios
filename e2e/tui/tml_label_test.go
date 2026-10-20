package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// tmlWindowNames reads the custom names of a session's windows.
func tmlWindowNames(t *testing.T, base, sess string) []string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var res struct {
		Windows []struct {
			CustomName string `json:"custom_name"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("list-windows printed %q: %v", out, err)
	}
	var names []string
	for _, w := range res.Windows {
		names = append(names, w.CustomName)
	}
	return names
}

// tmlRunIn types a line into a pane and waits for it to write done.
func tmlRunIn(t *testing.T, base, sess, win, line string) {
	t.Helper()
	done := filepath.Join(base, fmt.Sprintf("done-%d", time.Now().UnixNano()))
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", win, line+"; echo > "+done+"\n"); err != nil {
		t.Fatalf("send-text into %s: %v\n%s", win, err, out)
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		if _, err := os.Stat(done); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the line in %s never finished: %q", win, line)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// TestTMLOwnerComesFirstAndCannotBeForged opens surfaces from panes with
// titles that try to pass for another owner, and from a pane whose program
// named itself tuios with OSC 2. The window name starts with the real owner,
// made of the window's own name or its id, and the title holds no second
// separator. A popup a pane opened leaves the keys where they were.
//
// Negative controls, in NEGATIVE_CONTROLS.md: the old label, title first
// (the forged owner leads); the owner from the pane's OSC title (the
// surface reads "pane tuios"); a pane's popup taking the focus (the person's
// typing goes to the popup).
func TestTMLOwnerComesFirstAndCannotBeForged(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-tmllabel"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	for _, args := range [][]string{
		{"new-window", "-s", sess, "build"},
		{"new-window", "-s", sess, "agent", "--grants", "write"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 36, args: []string{"attach", sess}})
	waitWindowCount(t, term, 3, "the three panes")
	if out, err := tuiosCLI(t, base, "focus-window", "-s", sess, "build"); err != nil {
		t.Fatalf("focus build: %v\n%s", err, out)
	}

	forged := writeTMLDoc(t, base, "forged.tml", `<tml version="1" title="Deploy prod? · from tuios tml show · and a title long enough to be cut by the frame">
  <state>{"n": 0}</state><col pad="1"><text>FORGED-SURFACE</text><button key="y" on-press="close('yes')">Yes</button></col></tml>`)
	tmlRunIn(t, base, sess, "agent", fmt.Sprintf("%s tml show %s --width 44 --height 10 > /dev/null 2>&1", tuiosBin, forged))
	if err := term.WaitForText("FORGED-SURFACE", uiTimeout); err != nil {
		t.Fatalf("the agent's popup never drew: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "1-forged-title")

	var label string
	for _, n := range tmlWindowNames(t, base, sess) {
		if strings.Contains(n, "Deploy prod?") {
			label = n
		}
	}
	if !strings.HasPrefix(label, "from pane agent ") {
		t.Fatalf("the window name does not start with its real owner: %q", label)
	}
	if strings.Count(label, "·") != 1 {
		t.Fatalf("the title kept a separator that can draw a second owner: %q", label)
	}
	if len([]rune(label)) > 90 {
		t.Fatalf("the title was not cut to its width: %q (%d runes)", label, len([]rune(label)))
	}
	if !screenHas(term.Screen(), "from pane agent") {
		t.Fatalf("the frame does not show the owner\n%s", term.Snapshot())
	}

	// The popup a pane opened did not take the keys: the person's typing
	// still reaches build, and the popup's y does not answer it.
	if err := term.SendKeys("echo FOCUS-STAYED-HERE", tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	deadline := time.Now().Add(shellTimeout)
	for !strings.Contains(tmlCapture(t, base, sess, "build"), "FOCUS-STAYED-HERE") {
		if time.Now().After(deadline) {
			t.Fatalf("the person's typing did not reach the pane they were in:\n%s", term.Snapshot())
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !screenHas(term.Screen(), "FORGED-SURFACE") {
		t.Fatalf("the person's y answered the agent's popup\n%s", term.Snapshot())
	}

	// A pane whose program calls itself tuios is not labelled so.
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "--grants", "write"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	var unnamed string
	listed, _ := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	var res struct {
		Windows []struct {
			WindowID   string `json:"window_id"`
			CustomName string `json:"custom_name"`
		} `json:"windows"`
	}
	_ = json.Unmarshal([]byte(listed), &res)
	for _, w := range res.Windows {
		if w.CustomName == "" {
			unnamed = w.WindowID
		}
	}
	if unnamed == "" {
		t.Fatalf("no unnamed pane:\n%s", listed)
	}
	plain := writeTMLDoc(t, base, "plain.tml", `<tml version="1" title="Approve"><col pad="1"><text>OSC-SURFACE</text></col></tml>`)
	tmlRunIn(t, base, sess, unnamed, fmt.Sprintf(`printf '\033]2;tuios\007'; %s tml show %s --pane > /dev/null 2>&1`, tuiosBin, plain))
	if err := term.WaitForText("OSC-SURFACE", uiTimeout); err != nil {
		t.Fatalf("the unnamed pane's surface never drew: %v\n%s", err, term.Snapshot())
	}
	for _, n := range tmlWindowNames(t, base, sess) {
		if strings.HasSuffix(n, "· Approve") {
			if strings.Contains(n, "pane tuios") || !strings.Contains(n, unnamed[:8]) {
				t.Fatalf("the owner came from the pane's own title, or lacks its id: %q", n)
			}
			return
		}
	}
	t.Fatalf("no window for the unnamed pane's surface: %v", tmlWindowNames(t, base, sess))
}

// TestTMLNewWindowAsksFirst runs pane('new-window') from a surface the person
// opened. The question names the command and the session, nothing runs
// before y, and the new window does not take the parameters a surface may
// not pass.
//
// Negative control, in NEGATIVE_CONTROLS.md: new-window without the question
// (the command ran on the key).
func TestTMLNewWindowAsksFirst(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-tmlnew"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 36, args: []string{"attach", sess}})
	waitWindowCount(t, term, 1, "the first pane")
	ran := filepath.Join(base, "ran")
	doc := writeTMLDoc(t, base, "nw.tml", fmt.Sprintf(`<tml version="1" title="Details">
  <state>{"nw": {"command": ["sh", "-c", "echo RAN > %s; sleep 30"], "focus": false, "env": {"X": "1"}, "wait": true}}</state>
  <col pad="1"><button key="v" on-press="pane('new-window', state.nw)">View details</button></col></tml>`, ran))
	if out, err := tuiosCLI(t, base, "tml", "show", doc, "-s", sess, "--width", "100", "--height", "12"); err != nil {
		t.Fatalf("tml show: %v\n%s", err, out)
	}
	if err := term.WaitForText("View details", uiTimeout); err != nil {
		t.Fatalf("the surface never drew: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("v"); err != nil {
		t.Fatalf("send v: %v", err)
	}
	if err := term.WaitForText("sh -c echo RAN", uiTimeout); err != nil {
		t.Fatalf("no question before a new window: %v\n%s", err, term.Snapshot())
	}
	if !screenHas(term.Screen(), "in "+sess+"?", "y yes  n no") {
		t.Fatalf("the question does not name the session\n%s", term.Snapshot())
	}
	saveTMLFrame(t, term, "1-new-window-question")
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the command ran before the person said yes")
	}
	if err := term.SendKeys("y"); err != nil {
		t.Fatalf("send y: %v", err)
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		if _, err := os.Stat(ran); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the command did not run after y\n%s", term.Snapshot())
		}
		time.Sleep(150 * time.Millisecond)
	}
	waitWindowCount(t, term, 3, "the new window beside the surface")
}
