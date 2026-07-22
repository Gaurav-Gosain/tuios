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

// contains reports substring membership.
func contains(s, sub string) bool { return strings.Contains(s, sub) }

// writeTapeConfigFile writes a config.toml with the given [tape] body into the
// isolated XDG_CONFIG_HOME, before tuios starts, so the setting is in effect at
// boot.
func writeTapeConfigFile(t *testing.T, base, tapeBody string) {
	t.Helper()
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tapeBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestProjectTapeAutoReviewOpensDialog verifies the opt-in auto_review setting:
// with [tape] auto_review = true, entering a directory with an untrusted tape
// opens the review dialog automatically (rendered) without any keypress and
// without running anything, and re-entering the same directory does not re-pop.
func TestProjectTapeAutoReviewOpensDialog(t *testing.T) {
	base := t.TempDir()
	writeTapeConfigFile(t, base, "[tape]\nautorun = \"ask\"\nauto_review = true\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "scratch"}})
	killDaemon(t, base)

	const sessionName = "autoreviewproj"
	tape := "Session \"" + sessionName + "\"\n" +
		"Type \"echo SHOULD_NOT_RUN\" Enter\n"
	dir := writeProjectTape(t, base, "proj-autoreview", tape)

	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	// Entering the directory auto-opens the review dialog - no leader T t.
	enterProjectDir(t, term, dir)
	if err := term.WaitForText("Project Tape", uiTimeout); err != nil {
		t.Fatalf("review dialog did not auto-open with auto_review=true: %v\n%s", err, term.Snapshot())
	}
	// It is the untrusted review, and it shows the actions - nothing has run.
	if err := term.WaitForText("Trust and run", uiTimeout); err != nil {
		t.Fatalf("auto-opened dialog missing the Trust and run action: %v\n%s", err, term.Snapshot())
	}
	if !contains(term.Screen().Text(), "untrusted") {
		t.Fatalf("auto-opened dialog is not the untrusted review\n%s", term.Snapshot())
	}
	// Auto-review must not execute anything.
	time.Sleep(1500 * time.Millisecond)
	if lsHasSession(t, base, sessionName) {
		t.Fatalf("auto-review ran the tape; session %q must not exist", sessionName)
	}

	// Dismiss with Esc; the dialog closes and nothing is trusted.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !contains(s.Text(), "Project Tape")
	}, uiTimeout); err != nil {
		t.Fatalf("dialog did not close on Esc: %v\n%s", err, term.Snapshot())
	}

	// Re-entering the same directory must NOT re-pop the dialog (once per dir per
	// session dedup). Emit the OSC 7 report again for the same path.
	osc := `printf '\033]7;file://localhost` + dir + `\033\\'`
	if err := term.SendKeys(osc, tuitest.Enter); err != nil {
		t.Fatalf("re-emit osc7: %v", err)
	}
	time.Sleep(2 * time.Second)
	if contains(term.Screen().Text(), "Project Tape") {
		t.Fatalf("re-entering a handled dir re-popped the review dialog\n%s", term.Snapshot())
	}
	alive(t, term, "after auto-review opened, dismissed, and did not re-pop")
}

// TestProjectTapeAutoReviewDisabledStaysPassive is the negative case: with the
// default (auto_review off), entering a tape directory surfaces only the passive
// indicator - the review dialog does NOT auto-open.
func TestProjectTapeAutoReviewDisabledStaysPassive(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "scratch"}})
	killDaemon(t, base)

	tape := "Session \"passiveproj\"\nType \"echo hi\" Enter\n"
	dir := writeProjectTape(t, base, "proj-passive", tape)

	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	enterProjectDir(t, term, dir)
	// The passive badge appears...
	if err := term.WaitForText("tape ?", uiTimeout); err != nil {
		t.Fatalf("no passive indicator: %v\n%s", err, term.Snapshot())
	}
	// ...but the dialog must NOT auto-open.
	time.Sleep(1500 * time.Millisecond)
	if contains(term.Screen().Text(), "Project Tape") {
		t.Fatalf("review dialog auto-opened with auto_review off (default must stay passive)\n%s", term.Snapshot())
	}
	alive(t, term, "after a passive-only detection")
}

// TestProjectTapeCurrentScopeRendersLayout is the render-based acceptance test on
// the building client: a `Scope current` project tape builds a split layout in
// the client's own session, and the RENDERED screen shows both panes' command
// output, proving the body actually executed (Enter ran the commands and Split
// created a real pane). The markers are computed by the shell ($((...))), so they
// can only appear if the echo executed, not by the tape text being shown in the
// review dialog.
func TestProjectTapeCurrentScopeRendersLayout(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "scratch"}})
	killDaemon(t, base)

	tape := "Scope current\n" +
		"EnableTiling\n" +
		"Type \"echo editormark-$((2*3))\" Enter\n" +
		"Split vertical\n" +
		"Type \"echo servermark-$((7*8))\" Enter\n"
	dir := writeProjectTape(t, base, "proj-current", tape)

	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	enterProjectDir(t, term, dir)
	if err := term.WaitForText("tape ?", uiTimeout); err != nil {
		t.Fatalf("no untrusted project-tape indicator: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "T", "t"); err != nil {
		t.Fatalf("send review chord: %v", err)
	}
	if err := term.WaitForText("Project Tape", uiTimeout); err != nil {
		t.Fatalf("review dialog did not open on leader T t: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("send trust-and-run: %v", err)
	}

	// The rendered screen must show both panes' executed output at once.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return contains(txt, "editormark-6") && contains(txt, "servermark-56")
	}, 20*time.Second); err != nil {
		t.Fatalf("both panes' command output did not render: %v\n%s", err, term.Snapshot())
	}
	waitWindowCount(t, term, 2, "after building the current-scope layout")
	alive(t, term, "after a current-scope project tape built its layout")
}

// TestProjectTapeSessionScopeBuildsSession reproduces the owner's flow: a
// session-scoped multi-command tape creates a project session, builds a
// three-pane layout, names the panes, and runs the echoes. It verifies the built
// session against ground truth (the daemon's own pane capture, i.e. what actually
// ran and where) and a fresh client's render of the finished layout. It also
// confirms the tape did not run before the user trusted it, and that trust
// persists.
//
// The building client's in-place render of a session it just switched into and
// populated is a separate, pre-existing client-sync limitation (a fresh attach
// renders it correctly, as this test shows); the tape's job - constructing the
// session correctly - is what is asserted here.
func TestProjectTapeSessionScopeBuildsSession(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "scratch"}})
	killDaemon(t, base)

	const sessionName = "e2eproj"
	tape := "Session \"" + sessionName + "\"\n" +
		"RenameWindow \"editor\"\n" +
		"Type \"echo editormark-$((2*3))\" Enter\n" +
		"Split vertical\n" +
		"RenameWindow \"server\"\n" +
		"Type \"echo servermark-$((7*8))\" Enter\n" +
		"Split horizontal\n" +
		"RenameWindow \"shell\"\n" +
		"Focus \"editor\"\n"
	dir := writeProjectTape(t, base, "proj-session", tape)

	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	// Detection surfaces the passive indicator; nothing runs yet.
	enterProjectDir(t, term, dir)
	if err := term.WaitForText("tape ?", uiTimeout); err != nil {
		t.Fatalf("no untrusted indicator: %v\n%s", err, term.Snapshot())
	}
	if lsHasSession(t, base, sessionName) {
		t.Fatalf("session %q exists before trust; detection must run nothing", sessionName)
	}

	// Review + Trust and run.
	if err := term.SendKeys(tuitest.Ctrl('b'), "T", "t"); err != nil {
		t.Fatalf("review chord: %v", err)
	}
	if err := term.WaitForText("Project Tape", uiTimeout); err != nil {
		t.Fatalf("dialog did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("trust-and-run: %v", err)
	}

	// The project session is created.
	if err := term.WaitFor(func(tuitest.Screen) bool {
		return lsHasSession(t, base, sessionName)
	}, uiTimeout); err != nil {
		t.Fatalf("session %q was not created: %v", sessionName, err)
	}

	// Ground truth: the daemon's own capture of each named pane shows the echo
	// actually ran there (the marker is computed, so it proves execution). This
	// also proves the panes were named (editor/server) and that Split created
	// distinct panes.
	assertPaneRan(t, base, sessionName, "editor", "editormark-6")
	assertPaneRan(t, base, sessionName, "server", "servermark-56")

	// Render proof: a fresh client attaching to the built session renders the
	// three-pane layout with the executed output visible.
	c2 := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", sessionName}})
	if err := c2.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return contains(txt, "editormark-6") && contains(txt, "servermark-56")
	}, uiTimeout); err != nil {
		t.Fatalf("fresh client did not render the built layout's output: %v\n%s", err, c2.Snapshot())
	}
	waitWindowCount(t, c2, 3, "fresh client on the built session")

	// Trust persisted.
	storePath := filepath.Join(base, "XDG_DATA_HOME", "tuios", "tape-trust.toml")
	data, _ := os.ReadFile(storePath)
	if !contains(string(data), "[[trusted]]") || !contains(string(data), ".tuios.tape") {
		t.Fatalf("trust was not persisted to %s:\n%s", storePath, string(data))
	}
	alive(t, term, "after Trust and run built the project session")
}

// assertPaneRan fails unless the daemon's capture of the named pane contains the
// computed marker, i.e. the tape's command executed in that pane.
func assertPaneRan(t *testing.T, base, session, window, marker string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, err := tuiosCLI(t, base, "capture-pane", "--session", session, "--window", window)
		if err == nil && contains(out, marker) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	out, _ := tuiosCLI(t, base, "capture-pane", "--session", session, "--window", window)
	t.Fatalf("pane %q of session %q never showed %q (command did not run there):\n%s", window, session, marker, out)
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
		t.Fatalf("auto mode ran an untrusted tape; session %q must not exist", sessionName)
	}
	if contains(term.Screen().Text(), "Project Tape") {
		t.Fatalf("auto mode force-opened the review dialog for an untrusted tape")
	}
	alive(t, term, "after an untrusted tape was ignored in auto mode")
}
