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

// startInLogged is like startIn but returns the path to the raw PTY log so a
// test can read what tuios printed to stdout after the TUI exited (the
// detach/kill message lands there, past the alt-screen reset).
func startInLogged(t *testing.T, base string, o startOpts) (*tuitest.Terminal, string) {
	t.Helper()

	env := make([]string, 0, len(xdgKeys)+len(o.env)+2)
	for _, key := range xdgKeys {
		dir := filepath.Join(base, key)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("start: mkdir %s: %v", key, err)
		}
		env = append(env, key+"="+dir)
	}
	env = append(env, "SHELL=/bin/sh", "ENV=", "PS1=$ ")
	env = append(env, o.env...)

	cols, rows := o.cols, o.rows
	if cols == 0 {
		cols, rows = 120, 40
	}

	argv := append([]string{tuiosBin}, o.args...)
	argv = append(argv, "--no-animations")

	logPath := filepath.Join(t.TempDir(), "pty.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("start: create pty log: %v", err)
	}
	t.Cleanup(func() { _ = logFile.Close() })

	term := tuitest.StartT(t, argv,
		tuitest.WithSize(cols, rows),
		tuitest.WithTerm("xterm-256color"),
		tuitest.WithEnv(env...),
		tuitest.WithLog(logFile),
	)
	return term, logPath
}

func waitExit(t *testing.T, term *tuitest.Terminal, what string) int {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if code, exited := term.ExitCode(); exited {
			return code
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s: tuios never exited\n%s", what, term.Snapshot())
	return -1
}

func sessionListed(t *testing.T, base, name string) bool {
	t.Helper()
	out, _ := tuiosCLI(t, base, "ls", "--json")
	var sessions []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		t.Fatalf("parse ls --json: %v\noutput:\n%s", err, out)
	}
	for _, s := range sessions {
		if s.Name == name {
			return true
		}
	}
	return false
}

// TestLeaderQuitKillsSession: leader q in a daemon-attached session should KILL
// the session (label says "Quit and kill session").
func TestLeaderQuitKillsSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "repro-kill", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	term, logPath := startInLogged(t, base, startOpts{args: []string{"attach", "repro-kill"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("never attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	// leader q
	if err := term.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
		t.Fatalf("send leader q: %v", err)
	}
	waitExit(t, term, "after leader q")

	// Give the daemon a moment to process the kill.
	time.Sleep(300 * time.Millisecond)
	if sessionListed(t, base, "repro-kill") {
		out, _ := tuiosCLI(t, base, "ls")
		t.Fatalf("BUG1: session 'repro-kill' still exists after leader q (q did not kill)\nls:\n%s", out)
	}

	// The exit message must say the session was killed, not detached: the label
	// is "Quit and kill session", and reporting a detach after a kill is what
	// made leader q look like it only detached.
	logBytes, _ := os.ReadFile(logPath)
	msg := exitLine(string(logBytes))
	t.Logf("leader q killed the session (repro-kill gone); exit line: %q", msg)
	if !strings.HasPrefix(msg, "Killed session 'repro-kill'") {
		t.Fatalf("BUG1: exit message after leader q should report a kill, got %q\nlog tail:\n%s",
			msg, tailMessage(string(logBytes)))
	}
}

// TestLeaderQuitConfirm exercises the confirm_quit path: leader q raises the
// dialog; cancelling leaves the session alive; confirming kills it and reports a
// kill.
func TestLeaderQuitConfirm(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	// Turn on the always-confirm-quit preference through the config file, which
	// the attach path reads via ApplyAppearanceConfig.
	cfgPath := filepath.Join(base, "XDG_CONFIG_HOME", "tuios", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte("[appearance]\nconfirm_quit = true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if out, err := tuiosCLI(t, base, "new", "repro-confirm", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	term, logPath := startInLogged(t, base, startOpts{args: []string{"attach", "repro-confirm"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("never attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	// leader q raises the confirmation dialog.
	if err := term.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
		t.Fatalf("send leader q: %v", err)
	}
	if err := term.WaitForText("Close all windows and quit?", uiTimeout); err != nil {
		t.Fatalf("confirm dialog never appeared: %v\n%s", err, term.Snapshot())
	}

	// Cancel: the client keeps running and the session survives.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	alive(t, term, "after cancelling quit")
	if !sessionListed(t, base, "repro-confirm") {
		t.Fatalf("session was killed after cancelling the quit dialog")
	}

	// Now confirm: leader q, then 'y' kills the session.
	if err := term.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
		t.Fatalf("send leader q (second time): %v", err)
	}
	if err := term.WaitForText("Close all windows and quit?", uiTimeout); err != nil {
		t.Fatalf("confirm dialog never reappeared: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("y"); err != nil {
		t.Fatalf("send y: %v", err)
	}
	waitExit(t, term, "after confirming quit")
	time.Sleep(300 * time.Millisecond)

	if sessionListed(t, base, "repro-confirm") {
		t.Fatalf("session 'repro-confirm' still exists after confirming quit")
	}
	logBytes, _ := os.ReadFile(logPath)
	msg := exitLine(string(logBytes))
	t.Logf("confirmed quit; exit line: %q", msg)
	if !strings.HasPrefix(msg, "Killed session 'repro-confirm'") {
		t.Fatalf("exit message after confirmed quit should report a kill, got %q", msg)
	}
}

// TestLeaderDetachKeepsSession: leader d should DETACH (session survives).
func TestLeaderDetachKeepsSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "repro-detach", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	term, logPath := startInLogged(t, base, startOpts{args: []string{"attach", "repro-detach"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("never attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("send leader d: %v", err)
	}
	waitExit(t, term, "after leader d")
	time.Sleep(300 * time.Millisecond)

	if !sessionListed(t, base, "repro-detach") {
		out, _ := tuiosCLI(t, base, "ls")
		t.Fatalf("leader d killed the session (should detach)\nls:\n%s", out)
	}
	logBytes, _ := os.ReadFile(logPath)
	t.Logf("leader d detached, session survives. Exit message tail:\n%s", tailMessage(string(logBytes)))
}

// TestExitMessageNamesCurrentSession: after switching to 'myproj', the exit
// message must name myproj, not the original session.
func TestExitMessageNamesCurrentSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "session-0", "--detach"); err != nil {
		t.Fatalf("create session-0: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "myproj", "--detach"); err != nil {
		t.Fatalf("create myproj: %v: %s", err, out)
	}

	term, logPath := startInLogged(t, base, startOpts{args: []string{"attach", "session-0"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("never attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	// Open session switcher (leader S), type myproj, enter.
	if err := term.SendKeys(tuitest.Ctrl('b'), "S"); err != nil {
		t.Fatalf("send leader S: %v", err)
	}
	if err := term.WaitForText("myproj", uiTimeout); err != nil {
		t.Fatalf("session switcher never showed myproj: %v\n%s", err, term.Snapshot())
	}
	// Type the name to filter, then Enter to switch.
	if err := term.SendKeys("myproj"); err != nil {
		t.Fatalf("type myproj: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	// Wait until we are on myproj (notification "Session: myproj").
	if err := term.WaitForText("Session: myproj", uiTimeout); err != nil {
		t.Fatalf("never switched to myproj: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	// Detach now.
	if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("send leader d: %v", err)
	}
	waitExit(t, term, "after switch+detach")
	time.Sleep(200 * time.Millisecond)

	logBytes, _ := os.ReadFile(logPath)
	msg := exitLine(string(logBytes))
	t.Logf("exit line: %q", msg)
	if msg == "" {
		t.Fatalf("BUG2: no 'Detached/Killed session' exit line found\nlog tail:\n%s", tailMessage(string(logBytes)))
	}
	if strings.Contains(msg, "session-0") {
		t.Fatalf("BUG2: exit line names original session-0, not current myproj:\n%s", msg)
	}
	if !strings.Contains(msg, "myproj") {
		t.Fatalf("BUG2: exit line does not name current session myproj:\n%s", msg)
	}
}

// exitLine returns the client's final "Detached from session ..." or "Killed
// session ..." line printed to stdout after the alt-screen reset. It ignores
// the alt-screen render (which can contain session names from overlays like the
// session switcher) by matching only the literal message prefixes.
func exitLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Detached from session '") ||
			strings.HasPrefix(line, "Killed session '") {
			return line
		}
	}
	return ""
}

// tailMessage extracts printable lines mentioning session from raw PTY bytes,
// for diagnostics only.
func tailMessage(raw string) string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(line, "session") || strings.Contains(line, "detached") ||
			strings.Contains(line, "Killed") || strings.Contains(line, "myproj") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	if len(out) > 6 {
		out = out[len(out)-6:]
	}
	return strings.Join(out, "\n")
}
