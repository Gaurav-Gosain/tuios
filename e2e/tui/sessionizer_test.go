package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The sessionizer flow (#452): start a session in a directory, and switch the
// attached client to a session, here or on another machine, from a pane or
// from outside tuios.

// projectDir makes a directory under base for a session to start in, with
// symlinks resolved, since the daemon reports the shell's directory as the
// kernel has it.
func projectDir(t *testing.T, base, name string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	return real
}

// firstWindowCwd waits for the first window of session to report want as its
// directory, and fails with what it reported instead.
func firstWindowCwd(t *testing.T, base, session, want string) {
	t.Helper()
	var got, out string
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		var err error
		out, err = tuiosCLI(t, base, "list-windows", "-s", session, "--json")
		if err == nil {
			var res struct {
				Windows []struct {
					Cwd string `json:"cwd"`
				} `json:"windows"`
			}
			if json.Unmarshal([]byte(out), &res) == nil && len(res.Windows) > 0 {
				got = res.Windows[0].Cwd
				if got == want {
					return
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: the first window of %s is in %q, want %q\n%s", session, got, want, out)
}

// clientShows waits until list-clients has a client on session.
func clientShows(t *testing.T, base, session string) {
	t.Helper()
	var out string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, _ = tuiosCLI(t, base, "list-clients", "--json")
		var rows []struct {
			Session string `json:"session"`
		}
		if json.Unmarshal([]byte(out), &rows) == nil {
			for _, r := range rows {
				if r.Session == session {
					return
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: no client shows session %s\n%s", session, out)
}

// TestNewStartsInTheCallersDirectory: tuios new starts the session's first
// window in the directory it runs from, and --cwd names another one. This
// holds for a detached session and for one the command attaches to.
//
// On origin/main the daemon starts every first window in its own directory,
// which is where it was started, and --cwd is an unknown flag.
func TestNewStartsInTheCallersDirectory(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[startup]\nopen_default_window = true\n")
	// The daemon starts here, so its own directory is base/cwd.
	if out, err := tuiosCLI(t, base, "new", "first", "--detach"); err != nil {
		t.Fatalf("create first: %v\n%s", err, out)
	}

	here := projectDir(t, base, "here")
	if out, err := tuiosCLIInDir(t, base, here, nil, "new", "plain", "--detach"); err != nil {
		t.Fatalf("tuios new from %s: %v\n%s", here, err, out)
	}
	firstWindowCwd(t, base, "plain", here)

	named := projectDir(t, base, "named")
	if out, err := tuiosCLI(t, base, "new", "named", "--detach", "--cwd", named); err != nil {
		t.Fatalf("tuios new --cwd: %v\n%s", err, out)
	}
	firstWindowCwd(t, base, "named", named)

	if out, err := tuiosCLI(t, base, "new", "bad", "--detach", "--cwd", filepath.Join(base, "missing")); err == nil {
		t.Fatalf("ASSERTION: tuios new --cwd with a missing directory succeeded\n%s", out)
	} else if !strings.Contains(out, "does not exist") {
		t.Fatalf("ASSERTION: the refusal does not say the directory is missing\n%s", out)
	}

	// The attached form: the client opens the first window, and the
	// session's start directory is where it starts.
	attached := projectDir(t, base, "attached")
	term := startIn(t, base, startOpts{args: []string{"new", "live", "--cwd", attached}})
	waitWindowCount(t, term, 1, "the attached session's first window")
	firstWindowCwd(t, base, "live", attached)
	saveFrame(t, term, "new-cwd-attached")
}

// TestSwitchSessionMovesTheClient: tuios switch-session moves the attached
// client in place. From outside tuios it takes the client by the session it
// shows, and --create --cwd makes the session in a directory first. From a
// pane or a popup it moves the client that shows the pane's session.
//
// On origin/main there is no switch-session command.
func TestSwitchSessionMovesTheClient(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("create home: %v\n%s", err, out)
	}
	term := attachIn(t, base, "home", startOpts{})
	clientShows(t, base, "home")

	// A missing session is refused, and the refusal says how to make it.
	out, err := tuiosCLI(t, base, "switch-session", "-s", "home", "nowhere")
	if err == nil {
		t.Fatalf("ASSERTION: switch-session to a missing session succeeded\n%s", out)
	}
	if !strings.Contains(out, "--create") {
		t.Fatalf("ASSERTION: the refusal does not name --create\n%s", out)
	}

	proj := projectDir(t, base, "proj")
	out, err = tuiosCLI(t, base, "switch-session", "-s", "home", "--create", "--cwd", proj, "proj")
	if err != nil {
		t.Fatalf("switch-session --create: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Created session proj") {
		t.Fatalf("ASSERTION: switch-session did not say it created proj\n%s", out)
	}
	if err := term.WaitForText("Session: proj", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client never showed proj: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "proj")
	firstWindowCwd(t, base, "proj", proj)
	saveFrame(t, term, "switch-session-outside")

	// From a pane: the shell in proj's pane runs switch-session, and the
	// client that shows proj moves.
	if out, err := tuiosCLI(t, base, "send-text", "-s", "proj", tuiosBin+" switch-session --create from-pane\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	if err := term.WaitForText("Session: from-pane", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: switch-session from a pane did not move the client: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "from-pane")
	saveFrame(t, term, "switch-session-pane")

	// From a popup, which is what the sessionizer command key runs in. The
	// popup is a pane of the session the client shows.
	if out, err := tuiosCLI(t, base, "popup", "-s", "from-pane", "--", tuiosBin, "switch-session", "--create", "from-popup"); err != nil {
		t.Fatalf("popup: %v\n%s", err, out)
	}
	if err := term.WaitForText("Session: from-popup", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: switch-session from a popup did not move the client: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "from-popup")
	alive(t, term, "after switching sessions from the command line")
}

// TestSwitchSessionReachesAHost: switch-session HOST:NAME moves the client
// to a session on a machine from the [hosts] table, and --create --cwd make
// it there first, with a first window in that directory. The machine is the
// ssh stand-in the host tests use.
//
// On origin/main there is no switch-session command.
func TestSwitchSessionReachesAHost(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	// The rail lists build's sessions once the link is up.
	toggleSidebarViaPalette(t, term)
	railShows(t, term, "far-shell")
	waitRailCurrent(t, term, "home")

	farDir := projectDir(t, remote, "far-proj")
	out, err := tuiosCLIEnv(t, base, env, "switch-session", "--create", "--cwd", farDir, "build:far-new")
	if err != nil {
		t.Fatalf("switch-session build:far-new: %v\n%s", err, out)
	}
	waitRailCurrent(t, term, "far-new")
	noSwitchFailure(t, term, "going to build,")
	if ls, _ := tuiosCLI(t, remote, "ls"); !strings.Contains(ls, "far-new") {
		t.Fatalf("ASSERTION: build has no session far-new\n%s", ls)
	}
	if ls, _ := tuiosCLI(t, base, "ls"); strings.Contains(ls, "far-new") {
		t.Fatalf("ASSERTION: this machine made far-new, so the switch never left it\n%s", ls)
	}
	firstWindowCwd(t, remote, "far-new", farDir)
	saveFrame(t, term, "switch-session-host")
	alive(t, term, "after switching to a session on build")
}

// TestSwitchToAnUnarrangedSessionAppliesStartup: with [startup] tiled on, a
// session made with tuios new --detach and then switched to in place comes
// up tiled. Nobody had arranged it, so [startup] applies, as it does on a
// first attach.
//
// Negative control: make applyStartupToUnarranged in
// internal/app/host_attach.go do nothing. The pane then stays in the
// 80x24 box the daemon made it with.
func TestSwitchToAnUnarrangedSessionAppliesStartup(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\n")
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("create home: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "later", "--detach"); err != nil {
		t.Fatalf("create later: %v\n%s", err, out)
	}
	term := attachIn(t, base, "home", startOpts{cols: 120, rows: 40})
	clientShows(t, base, "home")

	if out, err := tuiosCLI(t, base, "switch-session", "-s", "home", "later"); err != nil {
		t.Fatalf("switch-session later: %v\n%s", err, out)
	}
	if err := term.WaitForText("Session: later", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client never showed later: %v\n%s", err, term.Snapshot())
	}
	pane := waitForSettledGeometryIn(t, base, "later", 1)[0]
	t.Logf("later's pane: (%d,%d) %dx%d", pane.X, pane.Y, pane.Width, pane.Height)
	if pane.Width < 120-4 {
		t.Fatalf("ASSERTION: later's pane is %d wide, want it filling the 120 columns: it came up floating\n%s",
			pane.Width, term.Snapshot())
	}
	saveFrame(t, term, "switch-session-startup-tiled")
	alive(t, term, "after switching to an unarranged session")
}
