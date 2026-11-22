//go:build unix && !js

package session

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

func TestOwnedPaneProcessHelper(t *testing.T) {
	mode := os.Getenv("TUIOS_TEST_OWNED_PROCESS")
	if mode == "" {
		return
	}
	if mode == "ignore-term" {
		signal.Ignore(syscall.SIGTERM)
	}
	if mode == "server" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("alive"))
			}))
		}()
		if err := os.WriteFile(os.Getenv("TUIOS_TEST_SERVER_FILE"), []byte(listener.Addr().String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestRemoveWorktreeStopsOwnedProcessesAndBlocksOnSurvivor(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	created := newWorktreeCall(t, c, repo, "owned-processes", nil)
	path := created["path"].(string)
	sess := d.manager.GetSession("repo-owned-processes")
	if sess == nil {
		t.Fatal("worktree session was not created")
	}
	state := sess.GetState()
	if len(state.Windows) == 0 {
		t.Fatal("worktree session has no pane")
	}
	paneID := state.Windows[0].ID
	cooperative := startOwnedPaneProcess(t, paneID, "server")
	stubborn := startOwnedPaneProcess(t, paneID, "ignore-term")
	unrelated := startOwnedPaneProcess(t, "", "normal")
	waitHTTPServer(t, cooperative.serverURL)
	for _, process := range []*ownedTestProcess{cooperative, stubborn, unrelated} {
		t.Cleanup(func() {
			_ = process.cmd.Process.Kill()
			select {
			case <-process.done:
			case <-time.After(3 * time.Second):
				t.Errorf("process %d did not exit during cleanup", process.cmd.Process.Pid)
			}
		})
	}

	resp := c.call(t, `{"id":2,"verb":"remove-worktree","params":{"session":"repo-owned-processes"}}`)
	if code := errCode(t, resp); code != ErrVerbOwnedProcesses {
		t.Fatalf("code = %q, want %q; response: %v", code, ErrVerbOwnedProcesses, resp)
	}
	message := fmt.Sprint(resp)
	for _, want := range []string{strconv.Itoa(stubborn.cmd.Process.Pid), paneID, "repo-owned-processes"} {
		if !strings.Contains(message, want) {
			t.Errorf("diagnostic %q does not identify %q", message, want)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("blocked close-out removed the worktree: %v", err)
	}
	waitProcessExit(t, cooperative)
	if httpServerAvailable(cooperative.serverURL) {
		t.Errorf("owned test server still responds after close-out")
	}
	assertProcessAlive(t, stubborn.cmd.Process.Pid)
	assertProcessAlive(t, unrelated.cmd.Process.Pid)

	if err := stubborn.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill stubborn owned process: %v", err)
	}
	waitProcessExit(t, stubborn)
	removed := result(t, c.call(t, `{"id":3,"verb":"remove-worktree","params":{"session":"repo-owned-processes"}}`))
	if removed["session_killed"] != true {
		t.Errorf("successful close-out result = %v, want session_killed", removed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("successful close-out left worktree: %v", err)
	}
	assertProcessAlive(t, unrelated.cmd.Process.Pid)
}

type ownedTestProcess struct {
	cmd       *exec.Cmd
	done      chan struct{}
	serverURL string
}

func startOwnedPaneProcess(t *testing.T, paneID, mode string) *ownedTestProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnedPaneProcessHelper$")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "TUIOS_PANE_ID=") && !strings.HasPrefix(entry, "TUIOS_TEST_OWNED_PROCESS=") && !strings.HasPrefix(entry, "TUIOS_TEST_SERVER_FILE=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	if paneID != "" {
		cmd.Env = append(cmd.Env, "TUIOS_PANE_ID="+paneID)
	}
	if mode != "" {
		cmd.Env = append(cmd.Env, "TUIOS_TEST_OWNED_PROCESS="+mode)
	}
	portFile := ""
	if mode == "server" {
		portFile = filepath.Join(t.TempDir(), "server-address")
		cmd.Env = append(cmd.Env, "TUIOS_TEST_SERVER_FILE="+portFile)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start owned process: %v", err)
	}
	process := &ownedTestProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(process.done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	visible := false
	for time.Now().Before(deadline) {
		if paneID == "" {
			_, visible = procinfo.StartTime(cmd.Process.Pid)
		} else if got, ok := procinfo.EnvVar(cmd.Process.Pid, "TUIOS_PANE_ID"); ok && got == paneID {
			visible = true
		}
		if visible {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !visible {
		t.Fatalf("process %d did not become visible", cmd.Process.Pid)
	}
	if portFile == "" {
		return process
	}
	for time.Now().Before(deadline) {
		if address, err := os.ReadFile(portFile); err == nil {
			process.serverURL = "http://" + string(address)
			return process
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("test server process %d did not publish its address", cmd.Process.Pid)
	return nil
}

func waitHTTPServer(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if httpServerAvailable(address) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("test server %s did not become available", address)
}

func httpServerAvailable(address string) bool {
	client := http.Client{Timeout: 250 * time.Millisecond}
	response, err := client.Get(address)
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return response.StatusCode == http.StatusOK
}

func waitProcessExit(t *testing.T, process *ownedTestProcess) {
	t.Helper()
	select {
	case <-process.done:
	case <-time.After(3 * time.Second):
		t.Fatalf("process %d did not exit", process.cmd.Process.Pid)
	}
}

func assertProcessAlive(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Errorf("process %d is not alive: %v", pid, err)
	}
}
