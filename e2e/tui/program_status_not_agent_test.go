package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestIdleProgramStatusIsNotAnAgent: a program that reports only an idle
// status line over OSC 7501, as an editor shows its file and mode, is a
// program and not an agent. Its record stays on the pane for the rail, and it
// gets no agent state, so list-agents and the Agents view leave it out. Two
// panes beside it keep what the protocol promises: an idle report whose app
// names a harness (app=codex) is an agent that waits, and the same plain
// program reporting working is shown like an agent.
//
// How this could pass wrongly, written down first:
//   - The editor pane could be left out because it never reported: the test
//     waits for its record in get-agent-state's program_status first.
//   - The pane could be left out of the Agents view because the view had not
//     drawn yet: the view must show the codex pane, which reported at the
//     same time, before the editor pane is checked for.
//   - The idle state could be dropped for every pane: the codex pane must
//     read idle with source program.
//
// Negative control: with the idle gate taken out of applyProgramStatus, the
// editor pane reads idle and list-agents names it.
func TestIdleProgramStatusIsNotAnAgent(t *testing.T) {
	const session = "psna"
	base := t.TempDir()
	killDaemon(t, base)
	work := workDirIn(t, base)
	scripts := map[string]string{
		// The status line of a program that is no agent: a file, a mode and
		// a branch, as an idle record with a message.
		"editor.sh": "printf '\\033]7501;state=idle:app=myedit:msg=%s\\033\\\\' \"$(printf 'main.go \\302\\267 normal \\302\\267 main' | base64 | tr -d '\\n')\"\necho EDITOR\"\"-ON\nsleep 120\n",
		// An agent that says it waits, before anything detects its process.
		"codex.sh": "printf '\\033]7501;state=idle:app=codex\\033\\\\'\necho CODEX\"\"-ON\nsleep 120\n",
		// A plain program at work: the protocol's own case.
		"build.sh": "printf '\\033]7501;state=working:app=build:msg=%s\\033\\\\' \"$(printf 'Compiling' | base64 | tr -d '\\n')\"\necho BUILD\"\"-ON\nsleep 120\n",
	}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	for _, p := range []string{"editor", "codex", "build"} {
		if out, err := tuiosCLI(t, base, "new-window", p, "-s", session, "--no-focus", "--", "sh", p+".sh"); err != nil {
			t.Fatalf("open %s: %v\n%s", p, err, out)
		}
		waitCapture(t, base, session, p, strings.ToUpper(p)+"-ON")
	}

	ed := waitPSState(t, base, session, "editor", "the editor's record", func(s psState) bool { return s.records() == "-=idle" })
	cx := waitPSState(t, base, session, "codex", "the codex pane's state", func(s psState) bool { return s.State == "idle" })
	bd := waitPSState(t, base, session, "build", "the build's state", func(s psState) bool { return s.State == "working" })
	// Past two detector readings, so a late claim would have landed.
	stayTrue(t, "the editor pane has no agent state and keeps its idle record", 5*time.Second, func() (bool, string) {
		ed = readPSState(t, base, session, "editor")
		return ed.State == "none" && ed.records() == "-=idle", ed.State + " " + ed.records()
	})
	if cx.Source != "program" {
		t.Errorf("ASSERTION: the codex pane = %+v, want idle from its program report", cx)
	}
	if bd.Source != "program" || bd.Message != "Compiling" {
		t.Errorf("ASSERTION: the build pane = %+v, want working from its program report", bd)
	}

	names := listedAgentNames(t, base, session)
	if names["editor"] || !names["codex"] || !names["build"] {
		t.Errorf("ASSERTION: list-agents names %v, want codex and build and not editor", names)
	}

	term := attachIn(t, base, session, startOpts{cols: 140, rows: 40, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Agents view: %v", err)
	}
	// The pane titles on the desktop name codex and build too, so the wait is
	// for the view's own count, which only the drawn view shows.
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, "2 agents", "codex", "build") }, uiTimeout); err != nil {
		t.Fatalf("the Agents view never listed the agents: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "agents-without-the-editor")
	if text := term.Screen().Text(); strings.Contains(text, "editor") || strings.Contains(text, "normal") {
		t.Errorf("ASSERTION: the Agents view shows the editor pane\n%s", term.Snapshot())
	}
	alive(t, term, "after the Agents view")
}

// listedAgentNames is the set of pane names list-agents gives for session.
func listedAgentNames(t *testing.T, base, session string) map[string]bool {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-agents", "-s", session, "--json")
	if err != nil {
		t.Fatalf("list-agents: %v\n%s", err, out)
	}
	var listing struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("list-agents JSON: %v\n%s", err, out)
	}
	got := map[string]bool{}
	for _, a := range listing.Agents {
		got[a.Name] = true
	}
	return got
}
