package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestVerbNewSessionGivesItsPaneTheCallersTerm creates a session with the
// new-session verb from a caller that has a real terminal, and reads the TERM
// and COLORTERM its pane's shell was given.
//
// The binary protocol's hello carries the caller's terminal, and the attach
// and new paths give it to the session they make. The JSON verb connection's
// hello carried no terminal, and new-session built its session from an empty
// config, so a session made by tuios call new-session, or by tuios
// switch-session --create, got the daemon's fallback (xterm-256color) whatever
// terminal the caller had.
//
// TestSessionMadeWithoutATerminalGivesItsPanesARealTerm
// (pane_term_headless_test.go) is the other half: a caller with no terminal of
// its own still gets the fallback.
//
// How this could pass wrongly, written down first:
//   - The caller's TERM could equal the daemon's fallback by coincidence. The
//     test's TERM, screen-256color, is not a fallback this daemon has, so a
//     match is the caller's value. Every ncurses ships its entry, so PaneTerm
//     keeps it on any runner.
//   - A daemon started earlier with another environment could answer. The
//     test has its own base, and starts its own daemon with start-server.
//   - The daemon could take TERM from its own environment, which start-server
//     gave it. A pane's TERM comes only from its session's config, which only
//     a client's hello fills, and is the fallback otherwise, so the fallback
//     is what an ignored hello shows.
//   - The line read could be the shell's echo of the typed command. The shell
//     prints the value through a format, so only its output has a TERM= line
//     without %s in it, and only a line ending in the marker is read.
//
// The pane's text is saved under artifactDir.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "The new-session verb gives
// its session the caller's terminal").
func TestVerbNewSessionGivesItsPaneTheCallersTerm(t *testing.T) {
	const session = "e2e-verb-term"
	base := t.TempDir()
	killDaemon(t, base)
	callerEnv := []string{"TERM=screen-256color", "COLORTERM=truecolor"}

	if out, err := tuiosCLIEnv(t, base, callerEnv, "start-server"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, base, callerEnv, "call", "new-session", `{"name":"`+session+`"}`); err != nil {
		t.Fatalf("create the session with the new-session verb: %v\n%s", err, out)
	}
	pane, got := askPaneTerm(t, base, callerEnv, session)
	if err := os.WriteFile(filepath.Join(artifactDir(t), "pane.txt"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}

	if want := "TERM=screen-256color COLORTERM=truecolor"; got != want {
		t.Errorf("a session made by the new-session verb, from a caller with a real terminal, gave its pane %q, want %q\n%s", got, want, pane)
	}
}

// TestAttachedNewGivesItsPaneTheCallersTerm is the same check for an
// attached tuios new. The TUI client's hello carried no terminal, so the
// session it made got the daemon's fallback (xterm-256color), while tuios new
// -d and the new-session verb gave their sessions the caller's terminal.
//
// How this could pass wrongly: the caller's TERM is screen-256color, which is
// not a fallback, for the reasons TestVerbNewSessionGivesItsPaneTheCallersTerm
// gives. The daemon is started by start-server with no TERM of its own, so
// only the attached client's hello can give the pane screen-256color.
//
// The pane's text is saved under artifactDir.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "An attached tuios new
// gives its session the caller's terminal").
func TestAttachedNewGivesItsPaneTheCallersTerm(t *testing.T) {
	const session = "e2e-attach-term"
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, []string{"TERM=", "COLORTERM="}, "start-server"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	callerEnv := []string{"TERM=screen-256color", "COLORTERM=truecolor"}
	term := startIn(t, base, startOpts{args: []string{"new", session}, env: callerEnv})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first window")

	pane, got := askPaneTerm(t, base, callerEnv, session)
	if err := os.WriteFile(filepath.Join(artifactDir(t), "attached-pane.txt"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}
	if want := "TERM=screen-256color COLORTERM=truecolor"; got != want {
		t.Errorf("a session made by an attached tuios new gave its pane %q, want %q\n%s", got, want, pane)
	}
}

// askPaneTerm has the shell of session's pane print its TERM and COLORTERM,
// and returns the pane's text and the printed line.
func askPaneTerm(t *testing.T, base string, env []string, session string) (pane, got string) {
	t.Helper()
	if out, err := tuiosCLIEnv(t, base, env, "send-keys", "-s", session, "-l",
		`printf 'TERM=%s COLORTERM=%s END\n' "$TERM" "$COLORTERM"`+"\r"); err != nil {
		t.Fatalf("ask the shell for its TERM: %v\n%s", err, out)
	}
	return readPaneTerm(t, base, env, session)
}

// readPaneTerm waits for the line a shell printed with TERM= and END, and
// returns the pane's text and the line without END. The shell's echo of the
// command holds %s, so it is skipped.
func readPaneTerm(t *testing.T, base string, env []string, session string) (pane, got string) {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	for got == "" {
		pane, _ = tuiosCLIEnv(t, base, env, "capture-pane", "-s", session)
		for _, line := range strings.Split(pane, "\n") {
			if line = strings.TrimSpace(line); strings.HasPrefix(line, "TERM=") && strings.HasSuffix(line, " END") &&
				!strings.Contains(line, "%s") {
				got = strings.TrimSuffix(line, " END")
			}
		}
		if got == "" {
			if time.Now().After(deadline) {
				t.Fatalf("the shell never printed its TERM:\n%s", pane)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return pane, got
}

// TestStartAgentGivesItsSessionTheCallersTerm starts an agent with
// start-agent into a session that does not exist yet, from a caller with a
// real terminal, and reads the TERM and COLORTERM the agent was given.
//
// start-agent makes the session it names when there is none, as new-session
// does. It made it from an empty config, so the agent got the daemon's
// fallback (xterm-256color) whatever terminal the caller had, while a session
// made by new-session got the caller's.
//
// How this could pass wrongly, written down first:
//   - The caller's TERM could equal the daemon's fallback. screen-256color is
//     not a fallback this daemon has, and every ncurses ships its entry.
//   - The value read could be the daemon's own environment. The daemon is
//     started by start-server with the same environment, but a pane's TERM
//     comes only from its session's config, so an ignored hello shows the
//     fallback.
//   - The agent could inherit a TERM from a shell. start-agent execs the agent
//     with no shell, and the agent prints its own environment.
//   - The line read could be another program's. Only the agent prints a line
//     ending in the marker.
//
// The pane's text is saved under artifactDir.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "The new-session verb gives
// its session the caller's terminal").
func TestStartAgentGivesItsSessionTheCallersTerm(t *testing.T) {
	const session = "e2e-agent-term"
	base := t.TempDir()
	killDaemon(t, base)
	callerEnv := []string{"TERM=screen-256color", "COLORTERM=truecolor"}

	// A program no harness manifest knows is ready once it reports a state,
	// so the agent prints its terminal and then says it is idle.
	agent := filepath.Join(t.TempDir(), "termagent")
	script := "#!/bin/sh\nprintf 'TERM=%s COLORTERM=%s END\\n' \"$TERM\" \"$COLORTERM\"\n'" + tuiosBin + "' set-agent-state idle >/dev/null 2>&1\nexec sleep 600\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	if out, err := tuiosCLIEnv(t, base, callerEnv, "start-server"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, base, callerEnv, "start-agent", agent, "-s", session); err != nil {
		t.Fatalf("start the agent: %v\n%s", err, out)
	}

	var pane, got string
	deadline := time.Now().Add(shellTimeout)
	for got == "" {
		pane, _ = tuiosCLIEnv(t, base, callerEnv, "capture-pane", "-s", session)
		for _, line := range strings.Split(pane, "\n") {
			if line = strings.TrimSpace(line); strings.HasPrefix(line, "TERM=") && strings.HasSuffix(line, " END") {
				got = strings.TrimSuffix(line, " END")
			}
		}
		if got == "" {
			if time.Now().After(deadline) {
				t.Fatalf("the agent never printed its TERM:\n%s", pane)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "agent-pane.txt"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}

	if want := "TERM=screen-256color COLORTERM=truecolor"; got != want {
		t.Errorf("ASSERTION: an agent start-agent started in a session it made, from a caller with a real terminal, got %q, want %q\n%s", got, want, pane)
	}
}

// TestWorktreeNewGivesItsSessionTheCallersTerm makes a worktree and its
// session with tuios worktree new, from a caller with a real terminal, and
// reads the TERM and COLORTERM the session's shell was given.
//
// new-worktree and fan make their session through createWorktreeSession,
// which built it from an empty config, so the shell got the daemon's fallback
// (xterm-256color) whatever terminal the caller had.
//
// How this could pass wrongly: the caller's TERM is screen-256color, which is
// not a fallback this daemon has, and paneTerms reads only the shell's own
// output of the value, not the echo of the command.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "The new-session verb gives
// its session the caller's terminal").
func TestWorktreeNewGivesItsSessionTheCallersTerm(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))
	repo := testutil.GitRepo(t)
	callerEnv := []string{"TERM=screen-256color", "COLORTERM=truecolor"}

	if out, err := tuiosCLIEnv(t, base, callerEnv, "start-server"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, base, callerEnv, "worktree", "new", "feat/term", "--repo", repo, "--detach"); err != nil {
		t.Fatalf("tuios worktree new: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "worktree", "ls", "--json")
	if err != nil {
		t.Fatalf("worktree ls: %v\n%s", err, out)
	}
	var rows []struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 || rows[0].Session == "" {
		t.Fatalf("worktree ls did not list one worktree with a session: %v\n%s", err, out)
	}

	got, pane := paneTerms(t, base, rows[0].Session)
	if err := os.WriteFile(filepath.Join(artifactDir(t), "worktree-pane.txt"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}
	if want := "TERM=screen-256color COLORTERM=truecolor"; got != want {
		t.Errorf("ASSERTION: the session tuios worktree new made, from a caller with a real terminal, gave its pane %q, want %q\n%s", got, want, pane)
	}
}

// TestVerbCommandsReachAnOlderDaemon: a client from this build, talking to a
// daemon built before the hello verb took term and color_term, still
// completes its handshake. That daemon refuses a parameter it does not know,
// so a hello carrying the terminal is refused with invalid_params, and the
// client asks again without it. This is the state right after an upgrade,
// before the daemon restarts.
//
// It needs that daemon: name a tuios built from upstream main, before term
// and color_term, with TUIOS_E2E_BASELINE_BIN. Without it the test is
// skipped.
//
// How it could pass wrongly, and what stops it:
//   - the new build's own daemon answering: the old build starts the daemon,
//     and its listing is read by both builds.
//   - a command that never handshakes: ls and call new-session both dial the
//     verb connection, and the session the call makes must be listed after.
//   - the retry giving the older daemon's session a terminal it never had:
//     the session's pane must read the older daemon's fallback, as every
//     verb-made session did before.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "The new-session verb gives
// its session the caller's terminal").
func TestVerbCommandsReachAnOlderDaemon(t *testing.T) {
	baseline := os.Getenv("TUIOS_E2E_BASELINE_BIN")
	if baseline == "" {
		t.Skip("TUIOS_E2E_BASELINE_BIN is not set: build tuios from upstream main, before hello took term, and name it there")
	}
	const session = "e2e-old-daemon"
	base := t.TempDir()
	killDaemon(t, base)
	callerEnv := []string{"TERM=screen-256color", "COLORTERM=truecolor"}

	old := exec.Command(baseline, "start-server")
	old.Dir = workDirIn(t, base)
	old.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		old.Env = append(old.Env, key+"="+xdgDir(base, key))
	}
	pinPreV080Looks(t, base)
	if out, err := old.CombinedOutput(); err != nil {
		t.Fatalf("start the older daemon: %v\n%s", err, out)
	}

	if out, err := tuiosCLIEnv(t, base, callerEnv, "ls"); err != nil {
		t.Fatalf("ASSERTION: tuios ls against an older daemon failed: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, base, callerEnv, "call", "new-session", `{"name":"`+session+`"}`); err != nil {
		t.Fatalf("ASSERTION: tuios call new-session against an older daemon failed: %v\n%s", err, out)
	}
	out, err := tuiosCLIEnv(t, base, callerEnv, "ls", "--json")
	if err != nil || !strings.Contains(out, `"`+session+`"`) {
		t.Fatalf("ASSERTION: the session made on the older daemon is not listed: %v\n%s", err, out)
	}

	if out, err := tuiosCLIEnv(t, base, callerEnv, "send-keys", "-s", session, "-l",
		`printf 'TERM=%s END\n' "$TERM"`+"\r"); err != nil {
		t.Fatalf("ask the shell for its TERM: %v\n%s", err, out)
	}
	pane, got := readPaneTerm(t, base, callerEnv, session)
	if err := os.WriteFile(filepath.Join(artifactDir(t), "older-daemon-pane.txt"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}
	if want := "TERM=xterm-256color"; got != want {
		t.Errorf("a session the older daemon made gave its pane %q, want its fallback %q\n%s", got, want, pane)
	}
}
