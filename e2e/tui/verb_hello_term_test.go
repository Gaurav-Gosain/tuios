package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
//     test's TERM, xterm-ghostty, is not a fallback this daemon has, so a match
//     is the caller's value.
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
	callerEnv := []string{"TERM=xterm-ghostty", "COLORTERM=truecolor"}

	if out, err := tuiosCLIEnv(t, base, callerEnv, "start-server"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, base, callerEnv, "call", "new-session", `{"name":"`+session+`"}`); err != nil {
		t.Fatalf("create the session with the new-session verb: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, base, callerEnv, "send-keys", "-s", session, "-l",
		`printf 'TERM=%s COLORTERM=%s END\n' "$TERM" "$COLORTERM"`+"\r"); err != nil {
		t.Fatalf("ask the shell for its TERM: %v\n%s", err, out)
	}

	var pane, got string
	deadline := time.Now().Add(shellTimeout)
	for got == "" {
		pane, _ = tuiosCLIEnv(t, base, callerEnv, "capture-pane", "-s", session)
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
	if err := os.WriteFile(filepath.Join(artifactDir(t), "pane.txt"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}

	if want := "TERM=xterm-ghostty COLORTERM=truecolor"; got != want {
		t.Errorf("a session made by the new-session verb, from a caller with a real terminal, gave its pane %q, want %q\n%s", got, want, pane)
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
	callerEnv := []string{"TERM=xterm-ghostty", "COLORTERM=truecolor"}

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
	var pane, got string
	deadline := time.Now().Add(shellTimeout)
	for got == "" {
		pane, _ = tuiosCLIEnv(t, base, callerEnv, "capture-pane", "-s", session)
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
	if err := os.WriteFile(filepath.Join(artifactDir(t), "older-daemon-pane.txt"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}
	if want := "TERM=xterm-256color"; got != want {
		t.Errorf("a session the older daemon made gave its pane %q, want its fallback %q\n%s", got, want, pane)
	}
}
