package tuie2e

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStatusLineWithADeadDaemonStillRenders runs the repro of issue #374: a
// status line payload that names a model, with TUIOS_SOCKET pointing at a
// daemon that cannot be dialed. The dial failed with a typed-nil client, the
// deferred close called Close on it, and the process died with SIGSEGV, so
// Claude Code's status line went blank. The command must instead exit 0 and
// still pass the payload to the --then command.
//
// Each case runs twice: once with no pane named, which dials to find the pane,
// and once with TUIOS_PANE_ID set, which dials later to send the values.
func TestStatusLineWithADeadDaemonStillRenders(t *testing.T) {
	// Socket paths have a short length limit, so the directory is in /tmp
	// rather than under TMPDIR.
	dir, err := os.MkdirTemp("/tmp", "i374-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	missing := filepath.Join(dir, "nope.sock")

	// A daemon that died without removing its socket leaves a socket file
	// that refuses every connection.
	stale := filepath.Join(dir, "stale.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: stale, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	_ = ln.Close()
	if fi, err := os.Stat(stale); err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("the stale socket file is not there: %v", err)
	}

	const payload = `{"model":{"id":"x"}}`
	sockets := map[string]string{"missing socket": missing, "stale socket": stale}
	panes := map[string][]string{
		"no pane":    nil,
		"named pane": {"TUIOS_PANE_ID=1", "TUIOS_SESSION=e2e-i374"},
	}
	for sockName, sock := range sockets {
		for paneName, paneEnv := range panes {
			t.Run(sockName+"/"+paneName, func(t *testing.T) {
				cmd := exec.Command(tuiosBin, "agent-statusline", "claude-code", "--integration", "1", "--then", "/bin/cat")
				cmd.Env = append(envWithoutTuios(), "TUIOS_SOCKET="+sock)
				cmd.Env = append(cmd.Env, paneEnv...)
				cmd.Stdin = strings.NewReader(payload)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				done := make(chan error, 1)
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				go func() { done <- cmd.Wait() }()
				select {
				case err = <-done:
				case <-time.After(30 * time.Second):
					_ = cmd.Process.Kill()
					t.Fatal("agent-statusline did not exit in 30s")
				}
				if err != nil {
					t.Fatalf("agent-statusline failed: %v\nstderr:\n%s", err, stderr.String())
				}
				if strings.Contains(stderr.String(), "panic") {
					t.Fatalf("agent-statusline panicked:\n%s", stderr.String())
				}
				if got := stdout.String(); got != payload {
					t.Fatalf("the --then command printed %q, want the payload %q", got, payload)
				}
			})
		}
	}
}

// envWithoutTuios is the environment without the TUIOS_ variables a run
// from inside a tuios pane inherits, so a case names its own pane.
func envWithoutTuios() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TUIOS_") {
			env = append(env, kv)
		}
	}
	return env
}
