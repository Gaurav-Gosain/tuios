package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// writeProjectTape creates a project directory under base with a .tuios.tape and
// returns its absolute path. The directory is 0700 so it passes the trust
// hygiene checks.
func writeProjectTape(t *testing.T, base, name, content string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".tuios.tape"), []byte(content), 0o600); err != nil {
		t.Fatalf("write tape: %v", err)
	}
	return dir
}

// enterProjectDir cd's the focused shell into dir and emits an OSC 7
// working-directory report, which is what tuios keys project-tape detection off.
// A bare `cd` under /bin/sh does not emit OSC 7, so the test emits it explicitly,
// exactly as an OSC-7-aware shell would.
func enterProjectDir(t *testing.T, term *tuitest.Terminal, dir string) {
	t.Helper()
	osc := `printf '\033]7;file://localhost` + dir + `\033\\'`
	if err := term.SendKeys("cd "+dir+" && "+osc, tuitest.Enter); err != nil {
		t.Fatalf("cd into project dir: %v", err)
	}
}

// lsHasSession reports whether `tuios ls` lists a session with the given name.
func lsHasSession(t *testing.T, base, name string) bool {
	t.Helper()
	out, _ := tuiosCLI(t, base, "ls")
	return strings.Contains(out, name)
}

// TestProjectTapeReviewTrustAndRun reproduces the owner's flow end to end: cd
// into a directory that carries a .tuios.tape, open the review dialog, choose
// Trust and run, and confirm the tape actually builds its project session and
// that trust persists to disk. It also confirms the tape did not run before the
// user trusted it.
func TestProjectTapeReviewTrustAndRun(t *testing.T) {
	// A daemon-backed session (tuios new) so session-per-project scope has a
	// place to create the project session.
	term, base := start(t, startOpts{args: []string{"new", "scratch"}})
	killDaemon(t, base)

	const sessionName = "e2eproj"
	tape := "Session \"" + sessionName + "\"\n" +
		"Type \"echo TAPE_E2E_RAN\" Enter\n"
	dir := writeProjectTape(t, base, "proj-review", tape)

	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	// Enter the project directory; detection surfaces the passive untrusted
	// indicator (the "tape ?" dock badge) after the debounce. Nothing runs.
	enterProjectDir(t, term, dir)
	if err := term.WaitForText("tape ?", uiTimeout); err != nil {
		t.Fatalf("no untrusted project-tape indicator after entering the dir: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after detecting the project tape")

	// Security: the tape must not have run just because we cd'd into its dir.
	if lsHasSession(t, base, sessionName) {
		t.Fatalf("session %q exists before the user trusted the tape; detection must run nothing\n%s", sessionName, term.Snapshot())
	}

	// Open the review dialog via the advertised chord (leader T t).
	if err := term.SendKeys(tuitest.Ctrl('b'), "T", "t"); err != nil {
		t.Fatalf("send review chord: %v", err)
	}
	if err := term.WaitForText("Project Tape", uiTimeout); err != nil {
		t.Fatalf("review dialog did not open on leader T t: %v\n%s", err, term.Snapshot())
	}
	// The dialog shows the content being reviewed and the trust actions.
	if err := term.WaitForText("Trust and run", uiTimeout); err != nil {
		t.Fatalf("review dialog missing the Trust and run action: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "echo TAPE_E2E_RAN") {
		t.Fatalf("review dialog did not display the tape content (the security boundary)\n%s", term.Snapshot())
	}

	// Choose Trust and run.
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("send trust-and-run: %v", err)
	}

	// The session named after the project is created and switched to.
	if err := term.WaitFor(func(tuitest.Screen) bool {
		return lsHasSession(t, base, sessionName)
	}, uiTimeout); err != nil {
		out, _ := tuiosCLI(t, base, "ls")
		t.Fatalf("session %q was not created by Trust and run: %v\nls output:\n%s\n%s", sessionName, err, out, term.Snapshot())
	}

	// Trust persisted to the store for next time.
	storePath := filepath.Join(base, "XDG_DATA_HOME", "tuios", "tape-trust.toml")
	if err := term.WaitFor(func(tuitest.Screen) bool {
		data, rerr := os.ReadFile(storePath)
		return rerr == nil && strings.Contains(string(data), "[[trusted]]") && strings.Contains(string(data), ".tuios.tape")
	}, uiTimeout); err != nil {
		data, _ := os.ReadFile(storePath)
		t.Fatalf("trust was not persisted to %s: %v\nstore:\n%s\n%s", storePath, err, string(data), term.Snapshot())
	}

	alive(t, term, "after Trust and run built the project session")
}

// TestProjectTapeUntrustedNeverAutoRuns confirms the core security invariant on
// screen: even in autorun = auto, an untrusted tape never runs and never
// auto-opens a dialog. It only surfaces the passive banner.
func TestProjectTapeUntrustedNeverAutoRuns(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "scratch"}, env: []string{"TUIOS_TAPE_AUTORUN=auto"}})
	killDaemon(t, base)

	const sessionName = "autoproj"
	tape := "Session \"" + sessionName + "\"\n" +
		"Type \"echo SHOULD_NOT_RUN\" Enter\n"
	dir := writeProjectTape(t, base, "proj-auto", tape)

	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	enterProjectDir(t, term, dir)
	if err := term.WaitForText("tape ?", uiTimeout); err != nil {
		t.Fatalf("no untrusted indicator in auto mode: %v\n%s", err, term.Snapshot())
	}

	// Give auto mode every chance to (wrongly) act, then assert it did not.
	time.Sleep(2 * time.Second)
	if lsHasSession(t, base, sessionName) {
		t.Fatalf("auto mode ran an untrusted tape; session %q must not exist\n%s", sessionName, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "Project Tape") {
		t.Fatalf("auto mode force-opened the review dialog for an untrusted tape\n%s", term.Snapshot())
	}
	alive(t, term, "after an untrusted tape was ignored in auto mode")
}
