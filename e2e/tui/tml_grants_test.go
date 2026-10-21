package tuie2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// tmlTypeDoc has a button that types into the pane named build.
const tmlTypeDoc = `<tml version="1" title="Typer">
  <state>{"run": {"window": "build", "text": "echo TYPED-BY-%s\n"}, "n": 0}</state>
  <col pad="1" gap="1">
    <text>Count: <b>{state.n}</b></text>
    <row gap="2"><button key="t" kind="primary" on-press="pane('send-text', state.run)">Type into build</button></row>
  </col>
</tml>
`

// tmlCapture reads a pane's screen as plain text.
func tmlCapture(t *testing.T, base, sess, window string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", window)
	if err != nil {
		t.Fatalf("capture-pane %s: %v\n%s", window, err, out)
	}
	return out
}

// TestTMLPaneActionsRunWithTheOwnersGrants is the grant rule from end to
// end. A pane that holds only write opens a surface whose button types into
// a pane that holds admin. The person presses the button and says yes to the
// question tuios asks, and the daemon still refuses, because the action runs
// with the opening pane's grants, never the person's. The same button on a
// surface the person opened types. The agent pane may not patch the person's
// surface, the window names who opened each surface, and a burst of patches
// past 100 a second is refused.
//
// Negative controls, in NEGATIVE_CONTROLS.md: the action run with the
// person's rights (the text lands in build); the owner check removed (the
// agent's patch lands); the rate limit removed (no patch is refused).
func TestTMLPaneActionsRunWithTheOwnersGrants(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-tmlgr"
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

	agentDoc := writeTMLDoc(t, base, "agent.tml", fmt.Sprintf(tmlTypeDoc, "AGENT"))
	agentOut := filepath.Join(base, "agent.out")
	line := fmt.Sprintf("%s tml show %s --pane --json > %s 2>&1\n", tuiosBin, agentDoc, agentOut)
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "agent", line); err != nil {
		t.Fatalf("type into the agent pane: %v\n%s", err, out)
	}
	if err := term.WaitForText("Type into build", shellTimeout); err != nil {
		data, _ := os.ReadFile(agentOut)
		t.Fatalf("the agent's surface never drew: %v\n%s\n%s", err, data, term.Snapshot())
	}
	var agentSurface struct {
		Surface string `json:"surface"`
	}
	data, _ := os.ReadFile(agentOut)
	if err := json.Unmarshal(data, &agentSurface); err != nil || agentSurface.Surface == "" {
		t.Fatalf("the agent's tml show printed %q (%v)", data, err)
	}

	// The pane holds write and not read, and reads its own surface.
	ownGet := filepath.Join(base, "own-get.out")
	tmlRunIn(t, base, sess, "agent", fmt.Sprintf("%s tml get %s > %s 2>&1", tuiosBin, agentSurface.Surface, ownGet))
	if data, _ := os.ReadFile(ownGet); !strings.Contains(string(data), "TYPED-BY-AGENT") {
		t.Fatalf("the pane that opened a surface could not read it:\n%s", data)
	}

	// A surface a pane opened does not take the keys, so the person clicks
	// its button. It asks the person first, under the surface, and names
	// the session.
	col, row := tmlLocate(t, term, "Type into build")
	// The first click only focuses a surface a pane opened; the second
	// presses the button.
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	time.Sleep(gestureGap)
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Type into e2e-tmlgr/build", uiTimeout); err != nil {
		t.Fatalf("no question before the surface typed: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "1-ask")
	if err := term.SendKeys("y"); err != nil {
		t.Fatalf("send y: %v", err)
	}
	if err := term.WaitForText("The surface could not run send-text", uiTimeout); err != nil {
		t.Fatalf("the refusal was not shown: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "2-refused")
	time.Sleep(500 * time.Millisecond)
	if got := tmlCapture(t, base, sess, "build"); strings.Contains(got, "TYPED-BY-AGENT") {
		t.Fatalf("a surface typed into a pane with more grants than its owner:\n%s", got)
	}

	// The person's own surface, the same button: it types.
	personDoc := writeTMLDoc(t, base, "person.tml", fmt.Sprintf(tmlTypeDoc, "PERSON"))
	out, err := tuiosCLI(t, base, "tml", "show", personDoc, "--pane", "--json", "-s", sess)
	if err != nil {
		t.Fatalf("tml show: %v\n%s", err, out)
	}
	var personSurface struct {
		Surface string `json:"surface"`
	}
	if err := json.Unmarshal([]byte(out), &personSurface); err != nil {
		t.Fatalf("tml show printed %q: %v", out, err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("send t: %v", err)
	}
	if err := term.WaitForText("Type into e2e-tmlgr/build", uiTimeout); err != nil {
		t.Fatalf("no question on the person's surface: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("y"); err != nil {
		t.Fatalf("send y: %v", err)
	}
	deadline := time.Now().Add(shellTimeout)
	for !strings.Contains(tmlCapture(t, base, sess, "build"), "TYPED-BY-PERSON") {
		if time.Now().After(deadline) {
			t.Fatalf("the person's surface never typed into build:\n%s", tmlCapture(t, base, sess, "build"))
		}
		time.Sleep(200 * time.Millisecond)
	}

	// The agent pane may not change the person's surface.
	patchOut := filepath.Join(base, "patch.out")
	line = fmt.Sprintf("%s tml patch %s '[{\"op\":\"replace\",\"path\":\"/n\",\"value\":7}]' > %s 2>&1\n", tuiosBin, personSurface.Surface, patchOut)
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "agent", line); err != nil {
		t.Fatalf("type into the agent pane: %v\n%s", err, out)
	}
	deadline = time.Now().Add(shellTimeout)
	for {
		data, _ := os.ReadFile(patchOut)
		if strings.Contains(string(data), "only for the pane that opened it") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent's patch of the person's surface was not refused:\n%s", data)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if got, _ := tuiosCLI(t, base, "tml", "get", personSurface.Surface); !strings.Contains(got, `"n":0`) {
		t.Fatalf("the person's surface changed:\n%s", got)
	}

	// The window names who opened each surface.
	listed, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	if err != nil || !strings.Contains(listed, "from agent · Typer") || !strings.Contains(listed, "from tuios tml show · Typer") {
		t.Fatalf("the windows do not name their owners: %v\n%s", err, listed)
	}

	// A burst past 100 patches a second is refused in part. One connection
	// sends them back to back, as a busy owner would.
	c := dialFileVerbs(t, base)
	refused := 0
	for i := range 250 {
		_, err := c.call("tml-patch", map[string]any{"surface": personSurface.Surface,
			"ops": []map[string]any{{"op": "replace", "path": "/n", "value": i}}})
		var fail *verbFailure
		if errors.As(err, &fail) && fail.Code == "rate_limited" {
			refused++
		} else if err != nil {
			t.Fatalf("patch %d: %v", i, err)
		}
	}
	if refused == 0 {
		t.Fatalf("250 patches back to back were all taken; the limit is 100 a second")
	}
	t.Logf("%d of 250 patches back to back were refused", refused)
	saveTMLFrame(t, term, "3-person-surface")
	alive(t, term, "after the grant checks")
}
