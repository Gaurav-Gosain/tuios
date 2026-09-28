//go:build !windows

package tmuxcompat

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// The holder tests run RunPane in a child process: this test binary run
// again with holderEnvKey set, so the holder is a real process with real
// children and signals, as it is in a pane.
const holderEnvKey = "TMUXCOMPAT_TEST_HOLDER"

// runHolderIfAsked runs the holder and exits when this binary was started as
// one. TestMain calls it before anything else.
func runHolderIfAsked() {
	if os.Getenv(holderEnvKey) == "1" {
		var cmd []string
		if c := os.Getenv("TMUXCOMPAT_TEST_CMD"); c != "" {
			cmd = strings.Split(c, "\x1f")
		}
		if d, err := time.ParseDuration(os.Getenv(replyDelayEnvKey)); err == nil {
			beforeReply = func() { time.Sleep(d) }
		}
		if gate := os.Getenv(exitGateEnvKey); gate != "" {
			afterExit = func(waiting *atomic.Int32) { exitGate(gate, waiting) }
		}
		os.Exit(RunPane(PaneOptions{
			Dir:     os.Getenv("TMUXCOMPAT_TEST_DIR"),
			Window:  os.Getenv("TUIOS_PANE_ID"),
			Command: cmd,
			Env:     []string{"HOLDER_EXTRA=yes"},
			Shell:   "/bin/sh",
		}))
	}
}

// Hooks for a holder child. replyDelayEnvKey holds a duration the holder
// sleeps before it writes each respawn reply. exitGateEnvKey names a
// directory for exitGate.
const (
	replyDelayEnvKey = "TMUXCOMPAT_TEST_REPLY_DELAY"
	exitGateEnvKey   = "TMUXCOMPAT_TEST_EXIT_GATE"
)

// exitGate holds a holder whose command has ended until a request waits on
// its queue: it writes <dir>/exited, then waits until waiting is above zero.
// The deadline is only a failsafe for a test that never sends.
func exitGate(dir string, waiting *atomic.Int32) {
	_ = os.WriteFile(filepath.Join(dir, "exited"), []byte("1"), 0o600)
	deadline := time.Now().Add(10 * time.Second)
	for waiting.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
}

// shortDir is a temporary directory with a short path: a unix socket path is
// capped near 104 bytes on macOS, and t.TempDir there is long.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "tc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func startHolder(t *testing.T, dir, window string, cmd ...string) (*exec.Cmd, chan error) {
	t.Helper()
	return startHolderEnv(t, dir, window, nil, cmd...)
}

// startHolderEnv is startHolder with extra KEY=VALUE for the holder itself.
func startHolderEnv(t *testing.T, dir, window string, env []string, cmd ...string) (*exec.Cmd, chan error) {
	t.Helper()
	c := exec.Command(os.Args[0], "-test.run=^$")
	c.Env = append(append(os.Environ(), env...),
		holderEnvKey+"=1",
		"TMUXCOMPAT_TEST_DIR="+dir,
		"TMUXCOMPAT_TEST_CMD="+strings.Join(cmd, "\x1f"),
		"TUIOS_PANE_ID="+window,
	)
	// No stdout or stderr: a command the holder leaves running must not hold
	// the test binary's output open.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	// SIGTERM, which the holder passes to its command's process group, as it
	// does when the pane is closed; then SIGKILL for anything left.
	t.Cleanup(func() {
		pid := c.Process.Pid
		_ = syscall.Kill(pid, syscall.SIGTERM)
		for i := 0; i < 100 && syscall.Kill(pid, 0) == nil; i++ {
			time.Sleep(20 * time.Millisecond)
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	})
	return c, done
}

func waitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return ""
}

// TestHolderRespawnsInPlace starts a holder the way split-window does, with a
// placeholder that never exits, then respawns it with another command the way
// Claude Code does. The placeholder must be ended, the new command must run
// with the pane's tmux environment, and the holder (the pane) must end with
// the new command.
func TestHolderRespawnsInPlace(t *testing.T) {
	dir := shortDir(t)
	window := "win-respawn"
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	_, done := startHolder(t, dir, window, "echo $$ > "+first+"; exec sleep 60")
	placeholder := waitFile(t, first)

	cmd := `printf '%s|%s|%s|%s' "$TMUX_PANE" "$HOLDER_EXTRA" "$RESPAWN_EXTRA" "$(pwd -P)" > ` + second
	cwd, _ := filepath.EvalSymlinks(dir)
	if err := RequestRespawn(dir, window, RespawnRequest{Command: []string{cmd}, Cwd: dir, Env: []string{"RESPAWN_EXTRA=ok"}}); err != nil {
		t.Fatalf("RequestRespawn: %v", err)
	}
	got := waitFile(t, second)
	if want := PaneID(window) + "|yes|ok|" + cwd; got != want {
		t.Errorf("the respawned command saw %q, want %q", got, want)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the holder exited with %v, want the new command's status 0", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the holder kept running after its command ended")
	}
	if err := exec.Command("kill", "-0", placeholder).Run(); err == nil {
		t.Errorf("the placeholder %s is still running after the respawn", placeholder)
	}
	if _, err := os.Stat(paneSocket(dir, window)); !os.IsNotExist(err) {
		t.Errorf("the holder left its socket behind: %v", err)
	}
}

// TestHolderAnswersARespawnToAnInstantCommand respawns to a command that ends
// at once, with the reply held back a moment. The holder must write the reply
// before it exits with that command, or respawn-pane reads EOF although the
// respawn ran.
func TestHolderAnswersARespawnToAnInstantCommand(t *testing.T) {
	dir := shortDir(t)
	window := "win-instant"
	marker := filepath.Join(dir, "up")
	_, done := startHolderEnv(t, dir, window, []string{replyDelayEnvKey + "=500ms"}, "echo up > "+marker+"; exec sleep 60")
	waitFile(t, marker)
	if err := RequestRespawn(dir, window, RespawnRequest{Command: []string{"true"}}); err != nil {
		t.Fatalf("RequestRespawn: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the holder exited with %v, want the new command's status 0", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the holder kept running after its command ended")
	}
}

// TestHolderAnswersARequestWaitingAtExit sends a respawn request while the
// pane's command has just ended. The holder must answer it with the reason
// before it exits, not leave the requester to read EOF, and must not run it.
func TestHolderAnswersARequestWaitingAtExit(t *testing.T) {
	dir := shortDir(t)
	window := "win-ended"
	_, done := startHolderEnv(t, dir, window, []string{exitGateEnvKey + "=" + dir}, "true")
	waitFile(t, filepath.Join(dir, "exited"))
	conn, err := net.Dial("unix", paneSocket(dir, window))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	respawned := filepath.Join(dir, "respawned")
	if _, err := conn.Write([]byte(`{"window":"` + window + `","command":["touch ` + respawned + `"]}` + "\n")); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("the holder did not answer: %v", err)
	}
	if !strings.Contains(reply, `"ok":false`) || !strings.Contains(reply, errPaneEnded.Error()) {
		t.Errorf("reply = %q, want a refusal saying the pane's command ended", reply)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the holder exited with %v, want its command's status 0", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the holder kept running after its command ended")
	}
	if _, err := os.Stat(respawned); err == nil {
		t.Error("the request ran after the pane's command ended")
	}
}

// TestHolderEmptyRespawnRerunsTheFirstCommand follows tmux: respawn-pane with
// no command runs the pane's first command again.
func TestHolderEmptyRespawnRerunsTheFirstCommand(t *testing.T) {
	dir := shortDir(t)
	window := "win-rerun"
	count := filepath.Join(dir, "count")
	_, done := startHolder(t, dir, window, "echo run >> "+count+"; exec sleep 60")
	waitFile(t, count)
	if err := RequestRespawn(dir, window, RespawnRequest{}); err != nil {
		t.Fatalf("RequestRespawn: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(count)
		if strings.Count(string(data), "run") == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(count)
	if n := strings.Count(string(data), "run"); n != 2 {
		t.Fatalf("the first command ran %d times, want 2", n)
	}
	select {
	case <-done:
		t.Fatal("the holder ended although its command is still running")
	default:
	}
}

// TestHolderRefusesAnotherWindowsRequest sends a holder a request naming a
// different window, as a request would after two windows' numbers collided,
// and checks it is refused and the pane's command left running.
func TestHolderRefusesAnotherWindowsRequest(t *testing.T) {
	dir := shortDir(t)
	marker := filepath.Join(dir, "ran")
	_, done := startHolder(t, dir, "win-mine", "echo up > "+marker+"; exec sleep 60")
	waitFile(t, marker)
	conn, err := net.Dial("unix", paneSocket(dir, "win-mine"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(`{"window":"win-other","command":["touch ` + filepath.Join(dir, "respawned") + `"]}` + "\n")); err != nil {
		t.Fatal(err)
	}
	reply, _ := bufio.NewReader(conn).ReadString('\n')
	if !strings.Contains(reply, `"ok":false`) || !strings.Contains(reply, "win-other") {
		t.Errorf("reply = %q, want a refusal naming the other window", reply)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "respawned")); err == nil {
		t.Error("the refused request ran")
	}
	select {
	case <-done:
		t.Error("the holder ended on a refused request")
	default:
	}
}

func TestEnsureDirClosesAnOpenDirectory(t *testing.T) {
	dir := filepath.Join(shortDir(t), "tmux")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0o777)
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(dir)
	if st.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, want 0700", st.Mode().Perm())
	}
	link := filepath.Join(shortDir(t), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(link); err == nil {
		t.Error("EnsureDir accepted a symlink")
	}
}
