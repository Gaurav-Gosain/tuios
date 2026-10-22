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
