package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
// the window's own name or else its id, and the title holds no second
// separator. The popup's frame shows the owner beside the title. A popup a pane opened leaves the keys where they were.
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
	agentID := ""
	listedAll, _ := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	var all struct {
		Windows []struct {
			WindowID   string `json:"window_id"`
			CustomName string `json:"custom_name"`
		} `json:"windows"`
	}
	_ = json.Unmarshal([]byte(listedAll), &all)
	for _, w := range all.Windows {
		if w.CustomName == "agent" {
			agentID = w.WindowID
		}
	}
	if agentID == "" || !strings.HasPrefix(label, "from agent · ") {
		t.Fatalf("the window name does not start with its real owner: %q", label)
	}
	if strings.Count(label, "·") != 1 {
		t.Fatalf("the title kept a separator that can draw a second owner: %q", label)
	}
	if len([]rune(label)) > 90 {
		t.Fatalf("the title was not cut to its width: %q (%d runes)", label, len([]rune(label)))
	}
	// The popup's frame reads the real owner first, then the cleaned title,
	// which holds no second separator. A title drawn first would let
	// "from tuios tml show" be read as the owner.
	frameRow := ""
	for line := range strings.SplitSeq(term.Screen().Text(), "\n") {
		if strings.Contains(line, "Deploy prod?") {
			frameRow = line
			break
		}
	}
	const wantFrame = "╭─ from agent · Deploy prod? - from tuios"
	if !strings.Contains(frameRow, wantFrame) || strings.Count(frameRow, "·") != 1 {
		t.Fatalf("the frame does not read the owner first: %q, want it to contain %q and one separator\n%s", frameRow, wantFrame, term.Snapshot())
	}
	// One click on the popup's button only focuses the popup: the button
	// is not pressed, and the popup stays.
	col, row := tmlLocate(t, term, "Yes")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	time.Sleep(500 * time.Millisecond)
	if !screenHas(term.Screen(), "FORGED-SURFACE") {
		t.Fatalf("the first click on a pane's popup pressed its button\n%s", term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "focus-window", "-s", sess, "build"); err != nil {
		t.Fatalf("focus build again: %v\n%s", err, out)
	}
	time.Sleep(300 * time.Millisecond)

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
	if err := term.WaitForText("Let this surface run this in a new window in", uiTimeout); err != nil {
		t.Fatalf("no question before a new window: %v\n%s", err, term.Snapshot())
	}
	if !screenHas(term.Screen(), sess+"?", `"echo RAN >`, "y yes  n no") {
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

// TestTMLOwnerIsNotWhatTheProgramCallsItself runs tml show from unnamed
// panes whose programs set argv[0] to "prod", and to "prod" plus the real
// prod pane's id. The label is the opening pane's own id, then a name only
// when the person or an admin action gave the window one, so neither
// surface can pass for the pane named prod.
//
// Negative control, in NEGATIVE_CONTROLS.md: the label takes the program's
// name (the surface reads "from pane prod ...").
func TestTMLOwnerIsNotWhatTheProgramCallsItself(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-tmlargv0"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("new: %v %s", err, out)
	}
	newWin := func(args ...string) string {
		out, err := tuiosCLI(t, base, append(append([]string{"new-window", "-s", sess}, args...), "--json")...)
		if err != nil {
			t.Fatalf("new-window %v: %v\n%s", args, err, out)
		}
		var w struct {
			WindowID string `json:"window_id"`
		}
		_ = json.Unmarshal([]byte(out), &w)
		return w.WindowID
	}
	prod := newWin("prod")
	agent := newWin("--grants", "write")
	agent2 := newWin("--grants", "write")
	term := startIn(t, base, startOpts{cols: 140, rows: 36, args: []string{"attach", sess}})
	waitWindowCount(t, term, 4, "four panes")
	doc := writeTMLDoc(t, base, "a.tml", `<tml version="1" title="Approve deploy"><col pad="1"><text>ARGV0-SURFACE</text></col></tml>`)
	o1, o2 := filepath.Join(base, "o1"), filepath.Join(base, "o2")
	line1 := fmt.Sprintf(`bash -c 'exec -a prod bash -c "sleep 3; %s tml show %s --pane --json > %s 2>&1; sleep 60"'`+"\n", tuiosBin, doc, o1)
	fake := "prod " + prod[:8]
	line2 := fmt.Sprintf(`bash -c 'exec -a "%s" bash -c "sleep 3; %s tml show %s --pane --json > %s 2>&1; sleep 60"'`+"\n", fake, tuiosBin, doc, o2)
	for win, line := range map[string]string{agent: line1, agent2: line2} {
		if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", win, line); err != nil {
			t.Fatalf("send-text: %v %s", err, out)
		}
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		_, e1 := os.Stat(o1)
		_, e2 := os.Stat(o2)
		if e1 == nil && e2 == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tml show did not run in the two panes")
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(time.Second)
	saveTMLFrame(t, term, "1-argv0")
	found := 0
	for _, n := range tmlWindowNames(t, base, sess) {
		if !strings.HasSuffix(n, "· Approve deploy") {
			continue
		}
		found++
		if strings.HasPrefix(n, "from pane prod") {
			t.Errorf("the program's argv[0] named the owner: %q", n)
		}
		if !strings.HasPrefix(n, "from pane "+agent[:8]+" · ") && !strings.HasPrefix(n, "from pane "+agent2[:8]+" · ") {
			t.Errorf("the owner is not the opening pane's id: %q", n)
		}
	}
	if found != 2 {
		t.Fatalf("found %d surfaces, want 2: %v", found, tmlWindowNames(t, base, sess))
	}
}

// TestTMLNewWindowYesCoversOneCommand gives a yes to one new-window command
// and checks that it covers neither a second command that starts the same
// way nor the same command in another folder. The question shows each whole.
//
// Negative control, in NEGATIVE_CONTROLS.md: the yes keyed on the command cut
// to 40 cells (the second command ran with no question).
func TestTMLNewWindowYesCoversOneCommand(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-tmlnw2"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("new: %v %s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 140, rows: 40, args: []string{"attach", sess}})
	waitWindowCount(t, term, 1, "first")
	ma, mb, mc := filepath.Join(base, "MA"), filepath.Join(base, "MB"), filepath.Join(base, "MC")
	cwd := filepath.Join(base, "elsewhere")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := writeTMLDoc(t, base, "nw.tml", fmt.Sprintf(`<tml version="1" title="Build report">
  <state>{"a": {"command": ["sh", "-c", "echo opening the build report viewer now; echo A > %s"]},
          "b": {"command": ["sh", "-c", "echo opening the build report viewer now; echo B > %s"]},
          "c": {"command": ["sh", "-c", "echo opening the build report viewer now; echo A > %s"], "cwd": "%s"},
          "d": {"command": ["sh", "-c", "echo opening the build report viewer now; echo A > %s"], "name": "prod"}}</state>
  <col pad="1"><text>NW-SURFACE</text>
    <button key="a" on-press="pane('new-window', state.a)">Open report</button>
    <button key="b" on-press="pane('new-window', state.b)">Open again</button>
    <button key="c" on-press="pane('new-window', state.c)">Open elsewhere</button>
    <button key="d" on-press="pane('new-window', state.d)">Open as prod</button>
  </col></tml>`, ma, mb, mc, cwd, ma))
	if out, err := tuiosCLI(t, base, "tml", "show", doc, "-s", sess, "--width", "120", "--height", "20"); err != nil {
		t.Fatalf("tml show: %v %s", err, out)
	}
	if err := term.WaitForText("NW-SURFACE", uiTimeout); err != nil {
		t.Fatalf("no surface: %v", err)
	}
	ask := func(key, wantInQuestion string) {
		t.Helper()
		if err := term.SendKeys(key); err != nil {
			t.Fatalf("send %s: %v", key, err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return screenHas(s, "Let this surface run this in a new window", wantInQuestion, "y yes  n no")
		}, uiTimeout); err != nil {
			t.Fatalf("%s asked no question showing %q: %v\n%s", key, wantInQuestion, err, term.Snapshot())
		}
	}
	waitFile := func(path string) {
		t.Helper()
		deadline := time.Now().Add(shellTimeout)
		for {
			if _, err := os.Stat(path); err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s was not made after y\n%s", path, term.Snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	refocus := func() {
		t.Helper()
		col, row := tmlLocate(t, term, "NW-SURFACE")
		mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
		time.Sleep(300 * time.Millisecond)
	}

	ask("a", "echo A > "+ma[:20])
	saveTMLFrame(t, term, "1-question-a")
	if err := term.SendKeys("y"); err != nil {
		t.Fatal(err)
	}
	waitFile(ma)
	refocus()

	ask("b", "echo B >")
	if _, err := os.Stat(mb); err == nil {
		t.Fatal("the yes for one command ran another")
	}
	if err := term.SendKeys("n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	ask("c", "in the folder "+cwd[:20])
	saveTMLFrame(t, term, "2-question-c")
	if _, err := os.Stat(mc); err == nil {
		t.Fatal("the yes for one folder ran the command in another")
	}
	if err := term.SendKeys("n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	// The same command with a window name the document picked asks again,
	// and shows the name.
	before := len(tmlWindowNames(t, base, sess))
	ask("d", "with the window name prod")
	time.Sleep(500 * time.Millisecond)
	if n := len(tmlWindowNames(t, base, sess)); n != before {
		t.Fatal("the yes for a command ran it again under a window name the person did not see")
	}
	if err := term.SendKeys("n"); err != nil {
		t.Fatal(err)
	}
}

// TestTMLQuestionWaitsForRoom asks about a command whose question fits the
// surface, then makes the surface smaller with more tiled windows. The
// question is not drawn in part: a line asks for room, and y does nothing
// until the whole question shows again.
//
// Negative control, in NEGATIVE_CONTROLS.md: the fit checked only at the
// press (the shrunk question hid the tail, and y ran it).
func TestTMLQuestionWaitsForRoom(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-tmlroom"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("new: %v %s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 140, rows: 40, args: []string{"attach", sess}})
	waitWindowCount(t, term, 1, "first")
	mark := filepath.Join(base, "SHRUNK")
	mid := "echo START; " + strings.Repeat("echo harmless-step; ", 18) + "echo TAIL-DANGER > " + mark + "; sleep 30"
	doc := writeTMLDoc(t, base, "l.tml", fmt.Sprintf(`<tml version="1" title="Long">
  <state>{"mid": {"command": ["sh", "-c", %q]}}</state>
  <col pad="1"><text>LONG-SURFACE</text>
  <button key="m" on-press="pane('new-window', state.mid)">M</button>
  </col></tml>`, mid))
	if out, err := tuiosCLI(t, base, "tml", "show", doc, "-s", sess, "--pane", "--name", "Long"); err != nil {
		t.Fatalf("tml show: %v %s", err, out)
	}
	if err := term.WaitForText("LONG-SURFACE", uiTimeout); err != nil {
		t.Fatalf("no surface: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := term.SendKeys("m"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, "TAIL-DANGER", "y yes  n no") }, uiTimeout); err != nil {
		t.Fatalf("the whole question was not shown: %v\n%s", err, term.Snapshot())
	}
	var extra []string
	for range 3 {
		out, err := tuiosCLI(t, base, "new-window", "-s", sess, "--no-focus", "--json")
		if err != nil {
			t.Fatalf("new-window: %v %s", err, out)
		}
		var w struct {
			WindowID string `json:"window_id"`
		}
		_ = json.Unmarshal([]byte(out), &w)
		extra = append(extra, w.WindowID)
	}
	if err := term.WaitForText("Make this pane bigger", uiTimeout); err != nil {
		t.Fatalf("a smaller surface did not hide the cut question: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "1-too-small")
	for _, n := range tmlWindowNames(t, base, sess) {
		if strings.Contains(n, "Long") {
			_, _ = tuiosCLI(t, base, "focus-window", "-s", sess, n)
		}
	}
	listed, _ := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	var all struct {
		Windows []struct {
			WindowID   string `json:"window_id"`
			CustomName string `json:"custom_name"`
		} `json:"windows"`
	}
	_ = json.Unmarshal([]byte(listed), &all)
	for _, w := range all.Windows {
		if strings.Contains(w.CustomName, "Long") {
			_, _ = tuiosCLI(t, base, "focus-window", "-s", sess, w.WindowID)
		}
	}
	time.Sleep(500 * time.Millisecond)
	if err := term.SendKeys("y"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("y ran a command whose question the person could not see whole")
	}
	for _, id := range extra {
		if out, err := tuiosCLI(t, base, "close-window", "-s", sess, id); err != nil {
			t.Fatalf("close-window: %v %s", err, out)
		}
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, "TAIL-DANGER", "y yes  n no") }, uiTimeout); err != nil {
		t.Fatalf("the question did not come back with the room: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("n"); err != nil {
		t.Fatal(err)
	}
}

// TestTMLOwnerNameMustBeUnique opens surfaces from a pane whose name another
// pane also has, and from a pane named like the label of a pane with no
// name. A pane with the fan grant names the agents it starts, so neither
// name can stand alone as the owner: both surfaces read their pane's id, with
// the name in brackets. A pane with a name of its own still reads the name
// alone. A pane with no name that runs an agent reads the agent's name, such
// as "claude", with the folder it started in when another pane runs the same
// agent.
//
// Negative control, in NEGATIVE_CONTROLS.md: tmlNameIsOwnersAlone always
// true (the copied names read "from api" and "from pane 0123abcd").
func TestTMLOwnerNameMustBeUnique(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-tmlname"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("new: %v %s", err, out)
	}
	newWin := func(args ...string) string {
		out, err := tuiosCLI(t, base, append(append([]string{"new-window", "-s", sess}, args...), "--json")...)
		if err != nil {
			t.Fatalf("new-window %v: %v\n%s", args, err, out)
		}
		var w struct {
			WindowID string `json:"window_id"`
		}
		_ = json.Unmarshal([]byte(out), &w)
		return w.WindowID
	}
	newWin("api")
	copied := newWin("api", "--grants", "write")
	idLike := newWin("pane 0123abcd", "--grants", "write")
	own := newWin("deployer", "--grants", "write")
	// Panes with no name that run an agent read the agent's name, and the
	// folder they started in when two run the same agent.
	alpha, beta := filepath.Join(base, "alpha"), filepath.Join(base, "beta")
	for _, dir := range []string{alpha, beta} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	claude := newWin("--grants", "write")
	codexA := newWin("--grants", "write", "--cwd", alpha)
	codexB := newWin("--grants", "write", "--cwd", beta)
	term := startIn(t, base, startOpts{cols: 160, rows: 40, args: []string{"attach", sess}})
	waitWindowCount(t, term, 8, "eight panes")
	// Every agent reports before any surface opens, so the two codex panes
	// see each other.
	for win, h := range map[string]string{claude: "claude-code", codexA: "codex", codexB: "codex"} {
		tmlRunIn(t, base, sess, win, fmt.Sprintf("%s set-agent-state working --harness %s", tuiosBin, h))
	}
	for win, title := range map[string]string{copied: "Copied", idLike: "IdLike", own: "Own", claude: "Agent", codexA: "CodexA", codexB: "CodexB"} {
		doc := writeTMLDoc(t, base, title+".tml", `<tml version="1" title="`+title+`"><col pad="1"><text>`+title+`</text></col></tml>`)
		tmlRunIn(t, base, sess, win, fmt.Sprintf("%s tml show %s --pane > /dev/null 2>&1", tuiosBin, doc))
	}
	want := map[string]string{
		"Copied": "from pane " + copied[:8] + " (api) · Copied",
		"IdLike": "from pane " + idLike[:8] + " (pane 0123abcd) · IdLike",
		"Own":    "from deployer · Own",
		"Agent":  "from claude · Agent",
		"CodexA": "from codex in alpha · CodexA",
		"CodexB": "from codex in beta · CodexB",
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		names := tmlWindowNames(t, base, sess)
		missing := ""
		for title, label := range want {
			if !slices.Contains(names, label) {
				missing = title + ": want " + label
			}
		}
		if missing == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s\nwindow names: %q", missing, names)
		}
		time.Sleep(150 * time.Millisecond)
	}
	saveTMLFrame(t, term, "1-owner-names")
}
