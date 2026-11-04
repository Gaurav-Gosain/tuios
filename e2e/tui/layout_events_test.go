package tuie2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// These tests hold what a client that mirrors a session's layout reads: the
// rectangles list-windows reports, the side split-window puts a new pane on,
// and the layout-changed event that says the rectangles moved. The phone app's
// Mirror view is built on the three.
//
// The command output of each test is saved under artifactDir.
//
// Negative controls (NEGATIVE_CONTROLS.md):
//   - TestSplitWindowTakesTheNamedSide: SplitToward maps "left" and "up" to
//     PreselectionRight and PreselectionDown. The new pane lands to the right
//     of and below the pane it was cut from, and both checks fail.
//   - TestLayoutChangedFollowsASplit: the layoutChanges call cut from
//     diffLifecycle. The subscription is live and never sees a
//     layout-changed, and the wait for it times out.
//   - TestAgentArrangesPanesWithNoClient: one control per layout verb, and
//     two for the attached client. See NEGATIVE_CONTROLS.md.

// layoutRect is one window's rectangle, as list-windows reports it.
type layoutRect struct {
	ID        string `json:"window_id"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Workspace int    `json:"workspace"`
}

// listRects reads the session's window rectangles.
func listRects(t *testing.T, base, sess string) map[string]layoutRect {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var res struct {
		Windows []layoutRect `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse list-windows: %v\n%s", err, out)
	}
	rects := map[string]layoutRect{}
	for _, w := range res.Windows {
		rects[w.ID] = w
	}
	return rects
}

// tileOn turns tiling on in the attached client and waits for the client to
// say so.
func tileOn(t *testing.T, base, sess string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "call", "set-layout", `{"session":"`+sess+`","tiling":true}`); err != nil {
		t.Fatalf("set-layout tiling: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, err := tuiosCLI(t, base, "call", "session-info", `{"session":"`+sess+`"}`)
		if err == nil && strings.Contains(out, `"tiling_mode":"tiling"`) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	out, _ := tuiosCLI(t, base, "call", "session-info", `{"session":"`+sess+`"}`)
	t.Fatalf("tiling never came on:\n%s", out)
}

// splitSide splits pane from toward side and returns the new pane's id, once
// both rectangles are in daemon state.
func splitSide(t *testing.T, base, sess, from, side string, log *strings.Builder) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "split-window", "-s", sess, "-w", from, side, "--json")
	log.WriteString("$ tuios split-window -w " + from + " " + side + " --json\n" + out + "\n")
	if err != nil {
		t.Fatalf("split-window %s: %v\n%s", side, err, out)
	}
	var res struct {
		WindowID string `json:"window_id"`
		Side     string `json:"side"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.WindowID == "" {
		t.Fatalf("split-window %s gave no pane id: %v\n%s", side, err, out)
	}
	if res.Side != side {
		t.Errorf("split-window %s answered side %q", side, res.Side)
	}
	return res.WindowID
}

func TestSplitWindowTakesTheNamedSide(t *testing.T) {
	term, base := attachClientBase(t)
	const sess = "e2e-ctrlp"
	dir := artifactDir(t)
	var log strings.Builder
	defer func() { _ = os.WriteFile(filepath.Join(dir, "commands.txt"), []byte(log.String()), 0o644) }()

	tileOn(t, base, sess)
	var first string
	for id := range listRects(t, base, sess) {
		first = id
	}

	// left: the new pane takes the left half, and the pane it was cut from
	// keeps the right half.
	left := splitSide(t, base, sess, first, "left", &log)
	waitWindowCount(t, term, 2, "after split-window left")
	rects := listRects(t, base, sess)
	log.WriteString(rectLine("after left", rects))
	n, o := rects[left], rects[first]
	if !(n.X+n.Width <= o.X+1 && n.Y == o.Y) {
		t.Errorf("split-window left put the new pane at %+v beside %+v, want it to the left", n, o)
	}
	saveArtifact(t, term, dir, "after-left")

	// up: the new pane takes the top half of the pane it was cut from.
	up := splitSide(t, base, sess, first, "up", &log)
	waitWindowCount(t, term, 3, "after split-window up")
	rects = listRects(t, base, sess)
	log.WriteString(rectLine("after up", rects))
	n, o = rects[up], rects[first]
	if !(n.Y+n.Height <= o.Y+1 && n.X == o.X) {
		t.Errorf("split-window up put the new pane at %+v over %+v, want it above", n, o)
	}
	saveArtifact(t, term, dir, "after-up")

	// A side the daemon does not know is refused by name.
	out, err := tuiosCLI(t, base, "call", "split-window", `{"session":"`+sess+`","side":"middle"}`)
	log.WriteString("$ tuios call split-window side=middle\n" + out + "\n")
	if err == nil || !strings.Contains(out, "unknown split side") {
		t.Errorf("side middle was not refused: %v\n%s", err, out)
	}
}

// rectLine is the rectangles in one line per pane, for the artifact.
func rectLine(label string, rects map[string]layoutRect) string {
	var b strings.Builder
	b.WriteString("# " + label + "\n")
	for id, r := range rects {
		fmt.Fprintf(&b, "%s x=%d y=%d w=%d h=%d\n", id[:8], r.X, r.Y, r.Width, r.Height)
	}
	return b.String()
}

func TestLayoutChangedFollowsASplit(t *testing.T) {
	term, base := attachClientBase(t)
	const sess = "e2e-ctrlp"
	dir := artifactDir(t)
	tileOn(t, base, sess)
	var first string
	for id := range listRects(t, base, sess) {
		first = id
	}

	// A layout-changed is not kept for replay, so one sent before the
	// subscription reached the daemon would be lost. The stream's first line
	// says the subscription is live.
	pinPreV080Looks(t, base)
	cmd := exec.Command(tuiosBin, "subscribe", "-s", sess, "--types", "layout-changed")
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		// A read error ends the stream as EOF does, and waitEvent reports it.
		_ = sc.Err()
		close(lines)
	}()
	var stream strings.Builder
	defer func() { _ = os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(stream.String()), 0o644) }()

	// waitEvent reads the stream until an event of type typ arrives.
	waitEvent := func(typ string, d time.Duration) map[string]any {
		deadline := time.After(d)
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("the subscription ended before a %s event", typ)
				}
				stream.WriteString(line + "\n")
				var ev map[string]any
				if json.Unmarshal([]byte(line), &ev) == nil && ev["type"] == typ {
					return ev
				}
			case <-deadline:
				t.Fatalf("no %s event within %s\n%s", typ, d, stream.String())
			}
		}
	}

	waitEvent("subscribed", uiTimeout)

	var log strings.Builder
	splitSide(t, base, sess, first, "right", &log)
	ev := waitEvent("layout-changed", uiTimeout)
	if ws, _ := ev["workspace"].(float64); ws != 1 {
		t.Errorf("layout-changed named workspace %v, want 1: %v", ev["workspace"], ev)
	}
	waitWindowCount(t, term, 2, "after the split")
	saveArtifact(t, term, dir, "after-split")
}

// layoutPane is one window of list-windows, with what the layout verbs change.
type layoutPane struct {
	layoutRect
	Name   string `json:"display_name"`
	Cwd    string `json:"cwd"`
	Zoomed bool   `json:"zoomed"`
}

// layoutPanes reads the session's windows by name.
func layoutPanes(t *testing.T, base, sess string) map[string]layoutPane {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var res struct {
		Windows []layoutPane `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse list-windows: %v\n%s", err, out)
	}
	rows := map[string]layoutPane{}
	for _, w := range res.Windows {
		rows[w.Name] = w
	}
	return rows
}

// An agent in a pane of a detached session splits its pane twice, swaps the
// two new panes, moves a border and zooms a pane, each by id, with no client
// attached. A client then attaches and shows that layout, and the zoom ends
// from the command line while the client watches.
//
// The layout the five calls make: the agent's pane on the left at 60
// percent, tests above logs on the right, tests zoomed.
//
// How this could pass wrongly, written down first:
//   - A client could be attached while the agent works, and do the layout
//     itself. Nothing is attached until the script is done, and the test
//     checks list-clients before the script starts.
//   - The rectangles could be the daemon's placeholder. The test checks
//     where each pane is relative to the others, and the share of the left
//     pane, which only a laid-out tree gives.
//   - The client could build its own layout on attach that happens to look
//     the same. The swap and the resize make a layout the client's own
//     insertion order does not: logs below tests and a 60 percent split.
//   - A step could fail and the next still pass. The script stops at the
//     first failure (set -e), and its log is checked.
func TestAgentArrangesPanesWithNoClient(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := artifactDir(t)
	const sess = "lay"
	if out, err := tuiosCLI(t, base, "new", "-d", sess); err != nil {
		t.Fatalf("new -d: %v\n%s", err, out)
	}
	var agentID string
	for id := range listRects(t, base, sess) {
		agentID = id
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", sess, "-w", agentID, "--name", "agent"); err != nil {
		t.Fatalf("name the agent's pane: %v\n%s", err, out)
	}
	agentDir := filepath.Join(base, "agent-dir")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// What an agent's shell tool runs. Each step names the pane by the id the
	// split printed.
	id := `sed -n 's/.*"window_id": *"\([^"]*\)".*/\1/p'`
	script := `set -e
T=` + tuiosBin + `
L=$($T split-window right --name logs --ratio 30 --no-focus --json -- sh -c 'echo LOGS-PANE; exec sleep 600' | ` + id + `)
X=$($T split-window down -w "$L" --name tests --no-focus --json -- sh -c 'echo TESTS-PANE; exec sleep 600' | ` + id + `)
$T swap-window -w "$L" "$X"
$T resize-window left 10 -w "$X"
$T zoom-window -w "$X" --on
`
	scriptPath := filepath.Join(base, "arrange.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "list-clients", "--json"); err != nil || strings.Contains(out, `"`+sess+`"`) {
		t.Fatalf("a client shows the session before the agent works: %v\n%s", err, out)
	}
	done, logPath := filepath.Join(base, "done"), filepath.Join(base, "arrange.log")
	line := "cd " + agentDir + " && sh " + scriptPath + " > " + logPath + " 2>&1; echo $? > " + done + "\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", agentID, line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	status := waitFile(t, done, func(s string) bool { return strings.HasSuffix(s, "\n") }, "the script never finished")
	logged, _ := os.ReadFile(logPath)
	_ = os.WriteFile(filepath.Join(dir, "arrange.log"), logged, 0o644)
	if strings.TrimSpace(status) != "0" {
		t.Fatalf("the script failed with %s:\n%s", status, logged)
	}

	// Headless: the daemon laid the panes out itself.
	rows := layoutPanes(t, base, sess)
	a, l, x := rows["agent"], rows["logs"], rows["tests"]
	var log strings.Builder
	fmt.Fprintf(&log, "# no client\nagent %+v\nlogs %+v\ntests %+v\n", a, l, x)
	_ = os.WriteFile(filepath.Join(dir, "headless.txt"), []byte(log.String()), 0o644)
	if l.ID == "" || x.ID == "" {
		t.Fatalf("the splits made no logs and tests panes: %v", rows)
	}
	if l.Cwd != agentDir {
		t.Errorf("ASSERTION: the split started in %q, want the agent's folder %s", l.Cwd, agentDir)
	}
	if !x.Zoomed {
		t.Errorf("ASSERTION: tests is not zoomed with no client attached: %+v", x)
	}
	if !(l.X >= a.X+a.Width && l.Y > a.Y) {
		t.Errorf("ASSERTION: with no client, logs is at %+v, want it right of the agent %+v and in the lower half (the swap)", l.layoutRect, a.layoutRect)
	}
	if share := float64(a.Width) / float64(a.Width+l.Width); share < 0.55 || share > 0.65 {
		t.Errorf("ASSERTION: with no client, the agent's pane has %.2f of the width, want 0.60 (0.70 less the resize)", share)
	}

	// A client attaches and shows the zoomed pane.
	term := attachIn(t, base, sess, startOpts{cols: 120, rows: 40})
	if err := term.WaitForText("TESTS-PANE", uiTimeout); err != nil {
		t.Fatalf("the attached client does not show the zoomed tests pane: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "attached-zoomed")
	if strings.Contains(term.Snapshot(), "LOGS-PANE") {
		t.Errorf("ASSERTION: the client shows logs beside a zoomed pane:\n%s", term.Snapshot())
	}

	// The zoom ends from outside, and the client shows the layout.
	if out, err := tuiosCLI(t, base, "zoom-window", "-s", sess, "-w", x.ID, "--off"); err != nil {
		t.Fatalf("zoom-window --off: %v\n%s", err, out)
	}
	var ly, lx, ty, tx int
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		var lok, tok bool
		ly, lx, lok = textAt(s, "LOGS-PANE", 0)
		ty, tx, tok = textAt(s, "TESTS-PANE", 0)
		return lok && tok
	}, uiTimeout); err != nil {
		t.Fatalf("the client never showed both panes after the zoom ended: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "attached")
	t.Logf("on screen: tests at row %d col %d, logs at row %d col %d", ty, tx, ly, lx)
	if !(ly > ty && lx > 50 && tx > 50) {
		t.Errorf("ASSERTION: the client does not show tests above logs on the right:\n%s", term.Snapshot())
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		rows = layoutPanes(t, base, sess)
		a, l, x = rows["agent"], rows["logs"], rows["tests"]
		share := float64(a.Width) / float64(a.Width+l.Width)
		if !x.Zoomed && l.Y > x.Y && l.X == x.X && share > 0.55 && share < 0.65 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the client's layout is not the agent's: agent %+v, logs %+v, tests %+v (share %.2f)", a, l, x, share)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
