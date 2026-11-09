package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	guios "github.com/Gaurav-Gosain/tuios/internal/guios/client"
)

// guios closes the window of a pane whose program exited, as a TUI does, and
// it must never close a live one: closing a window ends its program. On
// 2026-11-09 a live claude pane vanished twice right after a guios showed its
// session. This runs the guios client (internal/guios/client, the part of
// guios that reaps, with no window) against a scratch daemon.
//
// How this could pass wrongly, written down first:
//   - The client could never have run. It writes a line when it subscribes,
//     and the test waits for that line.
//   - The live pane could be live only because nothing was closed at all. The
//     same client closes the pane that exits, and the test waits for that.
//   - A close could come from somewhere else. No TUI is attached, so nothing
//     but the client closes a window.
//
// The artifact is the client's log: one line for each window it closed, with
// the window, its PTY and the exit code.
func TestGuiosReapClosesOnlyExitedPanes(t *testing.T) {
	if os.Getenv("TUIOS_E2E_GUIOS_REAP_CHILD") == "1" {
		guiosReapChild()
		return
	}
	base := t.TempDir()
	const session = "e2e-guios-reap"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	killDaemon(t, base)
	open := func(name string, args ...string) string {
		t.Helper()
		all := append([]string{"new-window", "-s", session, "--no-focus", "--print-id", name}, args...)
		out, err := tuiosCLI(t, base, all...)
		if err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
		return reapLastLine(out)
	}
	live := open("live", "--", "sleep", "600")
	kept := open("kept", "--keep-open", "--", "sh", "-c", "exit 4")
	exits := open("exits", "--", "sh", "-c", "sleep 3; exit 3")

	logPath := filepath.Join(artifactDir(t), "guios-reap-client.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create %s: %v", logPath, err)
	}
	defer logFile.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestGuiosReapClosesOnlyExitedPanes$", "-test.v")
	child.Dir = workDirIn(t, base)
	child.Env = append(os.Environ(), "TUIOS_E2E_GUIOS_REAP_CHILD=1")
	for _, key := range xdgKeys {
		child.Env = append(child.Env, key+"="+xdgDir(base, key))
	}
	child.Stdout, child.Stderr = logFile, logFile
	if err := child.Start(); err != nil {
		t.Fatalf("start the guios client: %v", err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	readLog := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(shellTimeout)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: %s. The client's log:\n%s", what, readLog())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	waitFor("the guios client never subscribed", func() bool {
		return strings.Contains(readLog(), "guios client subscribed")
	})
	windows := func() map[string]bool {
		ids := map[string]bool{}
		for _, r := range xpanesRowsIn(t, base, session) {
			ids[r.ID] = true
		}
		return ids
	}
	waitFor("the pane whose program exited was not closed", func() bool { return !windows()[exits] })
	waitFor("no log line names the close", func() bool {
		return strings.Contains(readLog(), "guios: closing window "+exits[:8]+" ") &&
			strings.Contains(readLog(), "its program exited with code 3")
	})
	// Give the client a few more builds to get any other one wrong.
	time.Sleep(2 * time.Second)
	now := windows()
	if !now[live] {
		t.Fatalf("ASSERTION: the client closed the live pane. The client's log:\n%s", readLog())
	}
	if !now[kept] {
		t.Fatalf("ASSERTION: the client closed the keep_open pane. The client's log:\n%s", readLog())
	}
}

// guiosReapChild is the guios client: it subscribes, as the app does, and
// runs until the test kills it.
func guiosReapChild() {
	c, err := guios.Connect(true)
	if err != nil {
		os.Stderr.WriteString("connect: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := c.Call("subscribe", nil, nil); err != nil {
		os.Stderr.WriteString("subscribe: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Stderr.WriteString("guios client subscribed\n")
	<-c.Done()
}

// reapLastLine is the last non-empty line of out.
func reapLastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
