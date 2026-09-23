//go:build !windows

package tmuxcompat

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"
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
		os.Exit(RunPane(PaneOptions{
			Dir:     os.Getenv("TMUXCOMPAT_TEST_DIR"),
			Window:  os.Getenv("TUIOS_PANE_ID"),
			Command: cmd,
			Env:     []string{"HOLDER_EXTRA=yes"},
			Shell:   "/bin/sh",
		}))
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
	c := exec.Command(os.Args[0], "-test.run=^$")
	c.Env = append(os.Environ(),
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

// TestHolderPutsItsCommandInTheForeground runs a holder on a terminal, as a
// pane runs it, and checks the command's process group is the terminal's
// foreground group and not the holder's. Agent detection reads the
// foreground process: with the holder there, a teammate's pane reads as a
// shell at its prompt and its agent is never found.
func TestHolderPutsItsCommandInTheForeground(t *testing.T) {
	dir := shortDir(t)
	report := filepath.Join(dir, "fg")
	p, err := xpty.NewUnixPty(80, 24)
	if err != nil {
		t.Skip("no pty:", err)
	}
	defer p.Close()
	var screen strings.Builder
	var mu sync.Mutex
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := p.Read(buf)
			mu.Lock()
			screen.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		if t.Failed() {
			mu.Lock()
			t.Logf("the pane showed:\n%s", screen.String())
			mu.Unlock()
		}
	})

	c := exec.Command(os.Args[0], "-test.run=^$")
	c.Env = append(os.Environ(),
		holderEnvKey+"=1",
		"TMUXCOMPAT_TEST_DIR="+dir,
		"TMUXCOMPAT_TEST_CMD="+`echo "$(ps -o pgid= -p $$) $(ps -o tpgid= -p $$)" > `+report+`; exec sleep 60`,
		"TUIOS_PANE_ID=win-fg",
	)
	// As internal/ptyspawn starts a pane's process: a session of its own,
	// with the terminal as its controlling one.
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := p.Start(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(c.Process.Pid, syscall.SIGTERM)
		_, _ = c.Process.Wait()
	})
	fields := strings.Fields(waitFile(t, report))
	if len(fields) != 2 {
		t.Fatalf("the command reported %q", fields)
	}
	if fields[0] != fields[1] {
		t.Errorf("the command's group %s is not the terminal's foreground group %s", fields[0], fields[1])
	}
	if fields[0] == strconv.Itoa(c.Process.Pid) {
		t.Errorf("the command runs in the holder's group %s", fields[0])
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

// TestHolderExitsWithItsCommand checks a pane closes with its command.
func TestHolderExitsWithItsCommand(t *testing.T) {
	dir := shortDir(t)
	_, done := startHolder(t, dir, "win-exit", "exit 3")
	select {
	case err := <-done:
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 3 {
			t.Errorf("holder exit = %v, want status 3", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the holder did not end with its command")
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

func TestRespawnWithoutAHolder(t *testing.T) {
	dir := shortDir(t)
	err := RequestRespawn(dir, "nobody", RespawnRequest{Command: []string{"true"}})
	if err == nil || !strings.Contains(err.Error(), "not opened through the tmux shim") {
		t.Errorf("RequestRespawn with no holder = %v", err)
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

func TestInstallLink(t *testing.T) {
	dir := shortDir(t)
	for _, exe := range []string{"/opt/a/tuios", "/opt/b/tuios"} {
		if err := InstallLink(dir, exe); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.Readlink(filepath.Join(BinDir(dir), "tmux")); got != exe {
			t.Errorf("link = %q, want %q", got, exe)
		}
	}
}
