package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// windowNames is the display name of every window of a session.
func windowNames(t *testing.T, base, sessionName string) []string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", sessionName, "--json")
	if err != nil {
		t.Fatalf("list-windows -s %s: %v\n%s", sessionName, err, out)
	}
	var res struct {
		Windows []struct {
			DisplayName string `json:"display_name"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("list-windows did not print JSON: %v\n%s", err, out)
	}
	var names []string
	for _, w := range res.Windows {
		names = append(names, w.DisplayName)
	}
	return names
}

// TestNewAgentIsNamedFromItsBranchOrTask starts agents whose program sets its
// own terminal title ("✳ Claude Code"), the way Claude Code does, and checks
// the name each one is known by. A worktree agent is named after its branch:
// fan --name agent/add-json-flag gives "add-json-flag". An agent the New
// agent sheet starts in this folder is named after its task. The agents view
// shows that name on the row and in the focus, and the focus header does not
// repeat the branch as the session's name.
//
// Negative control: on main both windows are named by the program's title,
// "✳ Claude Code", and the focus header shows the session
// "repo-agent-add-json-flag" beside the branch.
func TestNewAgentIsNamedFromItsBranchOrTask(t *testing.T) {
	base, repo := fanFixture(t)
	work := t.TempDir()
	if out, err := tuiosCLI(t, base, "new", "home", "--detach", "--cwd", work); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "fan", "1", "--agent", "claude", "--repo", repo, "--name", "agent/add-json-flag", "Add a json flag."); err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}
	waitUntil(t, "the worktree agent is named after its branch", uiTimeout, func() (bool, string) {
		names := windowNames(t, base, "repo-agent-add-json-flag")
		return len(names) == 1 && names[0] == "add-json-flag", strings.Join(names, ", ")
	})

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 120, rows: 40})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "the agents view", "Agents", "New agent")

	// The sheet starts an agent in this folder, which is no repository.
	sendKeys(t, term, "n")
	waitText(t, term, "the sheet", "Task", "Where it works", "Start agent")
	sendKeys(t, term, "Add a json flag to main.go")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "claude ready") && strings.Contains(s.Text(), "Add a json flag to main.go")
	}, uiTimeout); err != nil {
		t.Fatalf("the sheet never offered claude: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Enter)
	waitUntil(t, "the sheet's agent is named after its task", 30*time.Second, func() (bool, string) {
		names := windowNames(t, base, "home")
		for _, n := range names {
			if n == "add-json-flag" {
				return true, ""
			}
		}
		return false, strings.Join(names, ", ")
	})

	// Both rows name the agents, never by the program's title.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "2 agents") && !strings.Contains(s.Text(), "Claude Code")
	}, 20*time.Second); err != nil {
		t.Fatalf("ASSERTION: the agents view does not list both agents by name: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "agent-names")
	// The worktree agent's focus names its branch once: the session, named
	// after the branch, is not beside it.
	// The view drops a key in the moment after it opens, so a move can
	// be lost: wait for the focus after each one, then move again.
	found := false
	for range 5 {
		if term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(s.Text(), "agent/add-json-flag")
		}, 2*time.Second) == nil {
			found = true
			break
		}
		sendKeys(t, term, "j")
	}
	if !found {
		t.Fatalf("the worktree agent's focus never showed its branch:\n%s", term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "repo-agent-add-json-flag") {
		t.Errorf("ASSERTION: the focus header repeats the branch as the session's name:\n%s", term.Snapshot())
	}
	saveFrame(t, term, "agent-names-worktree")
	savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), "agent-names")
	alive(t, term, "after starting two named agents")
}
