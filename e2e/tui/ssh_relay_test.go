package tuie2e

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// The ssh relay stands in for sshd. The fake ssh a daemon runs execs this test
// binary with relaySockEnv set. That process sends the command and its own
// environment to startSSHRelay's socket and relays its stdio. The relay, in the
// test process, runs the command with the connection as its stdio. The command
// is then a child of the test process and not of the daemon, as a command
// sshd runs is not.

// relaySockEnv names the relay socket for a relay client.
const relaySockEnv = "TUIOS_E2E_RELAY_SOCK"

// relayRequest is the first line a relay client sends.
type relayRequest struct {
	Cmd string   `json:"cmd"`
	Env []string `json:"env"`
}

// runRelayClientIfAsked runs the relay client and exits when this binary was
// started as one. TestMain calls it first.
func runRelayClientIfAsked() {
	sock := os.Getenv(relaySockEnv)
	if sock == "" {
		return
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		os.Exit(255)
	}
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if len(kv) < len(relaySockEnv) || kv[:len(relaySockEnv)] != relaySockEnv {
			env = append(env, kv)
		}
	}
	line, _ := json.Marshal(relayRequest{Cmd: os.Getenv("TUIOS_E2E_RELAY_CMD"), Env: env})
	if _, err := conn.Write(append(line, '\n')); err != nil {
		os.Exit(255)
	}
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		if uc, ok := conn.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
	}()
	_, _ = io.Copy(os.Stdout, conn)
	os.Exit(0)
}

// testBinary is the path of this test binary.
func testBinary(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return exe
}

// startSSHRelay starts the relay for one test and returns its socket.
func startSSHRelay(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rl")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var children []*exec.Cmd
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range children {
			if c.Process != nil {
				_ = c.Process.Kill()
			}
		}
		mu.Unlock()
		_ = os.RemoveAll(dir)
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				r := bufio.NewReader(conn)
				line, err := r.ReadBytes('\n')
				if err != nil {
					return
				}
				var req relayRequest
				if json.Unmarshal(line, &req) != nil {
					return
				}
				cmd := exec.Command("/bin/sh", "-c", req.Cmd)
				cmd.Env = req.Env
				cmd.Stdin = r
				cmd.Stdout = conn
				mu.Lock()
				children = append(children, cmd)
				mu.Unlock()
				if cmd.Start() != nil {
					return
				}
				_ = cmd.Wait()
			}()
		}
	}()
	return sock
}
