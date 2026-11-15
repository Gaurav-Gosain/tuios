package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// screenLineFinds is the first line of s that holds every marker, "" when no
// line does.
func screenLineFinds(s tuitest.Screen, markers ...string) string {
	for line := range strings.SplitSeq(s.Text(), "\n") {
		ok := true
		for _, m := range markers {
			ok = ok && strings.Contains(line, m)
		}
		if ok {
			return line
		}
	}
	return ""
}

// stopHook runs Claude Code's Stop hook for a pane with what the agent said
// last.
func stopHook(t *testing.T, base, sessionName, window, said string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"hook_event_name": "Stop", "session_id": "e2e-" + window, "last_assistant_message": said})
	if err != nil {
		t.Fatal(err)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", sessionName, "--window", window)
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = strings.NewReader(string(payload))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook Stop for %s: %v\n%s", window, err, out)
	}
}

// TestCockpitSearchFindsByNameAndKeepsTheAgent searches the agents view.
//
//   - "fib" finds fib-memo by its name and ask-first by a word of what it
//     asks, and not pusher, whose message only holds the letters f, i, b
//     ("The push failed ... because"). The cursor goes to fib-memo, the name
//     match, though ask-first is above it in Needs you.
//   - The search box keeps its place in the header with every count chip
//     drawn.
//   - esc clears the search and the cursor stays on fib-memo.
//   - A finished agent's preview goes past a line that ends with a colon,
//     and shows no Markdown marks.
//
// Negative control: on main "fib" lists pusher, the cursor lands on
// ask-first, and the preview is "The command printed one line:".
func TestCockpitSearchFindsByNameAndKeepsTheAgent(t *testing.T) {
	term, base, _, _ := startCockpitFleetWith(t, 120, 40, nil, "")
	agents := []struct{ name, state, msg string }{
		{"fib-memo", "idle", ""},
		{"ask-first", "needs_input", "Should fib use int64 or math/big?"},
		{"pusher", "working", "The push failed with exit code 128 because of a bad ref"},
	}
	for _, a := range agents {
		if out, err := tuiosCLI(t, base, "new-window", a.name, "-s", "misc", "--", "/bin/sh", "-c", "sleep 900"); err != nil {
			t.Fatalf("open %s: %v\n%s", a.name, err, out)
		}
		args := []string{"set-agent-state", a.state, "-s", "misc", "-w", a.name, "--harness", "claude-code"}
		if a.msg != "" {
			args = append(args, "-m", a.msg)
		}
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("report %s: %v\n%s", a.name, err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "new-window", "printer", "-s", "misc", "--", "/bin/sh", "-c", "sleep 900"); err != nil {
		t.Fatalf("open printer: %v\n%s", err, out)
	}
	stopHook(t, base, "misc", "printer", "The command printed one line:\n\n```\nhello from printer\n```\n")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "fib-memo", "ask-first", "pusher", "printer")
	}, uiTimeout); err != nil {
		t.Fatalf("the agents view never listed the new agents: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "before-search")

	sendKeys(t, term, "/")
	sendKeys(t, term, "fib")
	sendKeys(t, term, tuitest.Enter)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "ask-first") && !strings.Contains(s.Text(), "release-not")
	}, uiTimeout); err != nil {
		t.Fatalf("the search never narrowed the list: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveFrame(t, term, "search-fib")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "search-fib")
	s := term.Screen()
	if strings.Contains(s.Text(), "pusher") {
		t.Errorf("ASSERTION: the search finds pusher by letters spread over its message:\n%s", term.Snapshot())
	}
	if !strings.Contains(s.Text(), "Reply to fib-memo") {
		t.Errorf("ASSERTION: the cursor is not on fib-memo, the agent the name matches:\n%s", term.Snapshot())
	}
	if line := screenLineFinds(s, "Agents", "/  fib"); line == "" {
		t.Errorf("ASSERTION: the header does not show the search box with the search:\n%s", term.Snapshot())
	}

	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "pusher") && strings.Contains(s.Text(), "release-not")
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not clear the search: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "Reply to fib-memo") {
		t.Errorf("ASSERTION: clearing the search moved the cursor off fib-memo:\n%s", term.Snapshot())
	}
	saveFrame(t, term, "search-cleared")

	// The finished agent's preview, in its focus.
	sendKeys(t, term, "/")
	sendKeys(t, term, "printer")
	sendKeys(t, term, tuitest.Enter)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "The command printed one line: hello from printer")
	}, uiTimeout); err != nil {
		t.Errorf("ASSERTION: the preview stops at the line that ends with a colon: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "preview")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "preview")
	alive(t, term, "after a search")
}
