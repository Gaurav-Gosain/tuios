//go:build linux

package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestReadExe checks the real binary is resolved from procfs, using this test
// process as the one process guaranteed to be there. It is the read that lets the
// detector see past a process that renamed itself.
func TestReadExe(t *testing.T) {
	got := readExe(os.Getpid())
	if got == "" {
		t.Fatal("readExe returned nothing for the running test process")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("readExe = %q, want an absolute path", got)
	}
	if strings.HasSuffix(got, " (deleted)") {
		t.Errorf("readExe = %q, want the deleted marker stripped", got)
	}
	if readExe(-1) != "" {
		t.Error("readExe returned a path for an impossible pid")
	}
}

// TestParseStatTPGID checks the tpgid is read from field 8 even when the comm in
// field 2 contains spaces and parentheses.
func TestParseStatTPGID(t *testing.T) {
	// pid (comm) state ppid pgrp session tty_nr tpgid ...
	line := "1234 (weird (name) x) S 1000 1234 1000 34816 4321 4194304 ..."
	got, ok := parseStatTPGID(line)
	if !ok || got != 4321 {
		t.Fatalf("parseStatTPGID = (%d, %v), want (4321, true)", got, ok)
	}

	if _, ok := parseStatTPGID("garbage without paren"); ok {
		t.Error("parseStatTPGID accepted a line with no ')'")
	}
	if _, ok := parseStatTPGID("1 (init) S 0 1"); ok {
		t.Error("parseStatTPGID accepted a truncated line")
	}
}

// TestReadProcessInfoSelf checks the three readings agree about this process,
// which is the one process a test can be certain of.
func TestReadProcessInfoSelf(t *testing.T) {
	info := readProcessInfo(os.Getpid())
	if info.comm == "" {
		t.Error("readProcessInfo returned no comm for the running test process")
	}
	if len(info.argv) == 0 {
		t.Error("readProcessInfo returned no argv for the running test process")
	}
	if info.exe == "" {
		t.Error("readProcessInfo returned no exe for the running test process")
	}
	if got := readProcessInfo(-1); got.comm != "" || got.argv != nil || got.exe != "" {
		t.Errorf("readProcessInfo(-1) = %+v, want the zero value", got)
	}
}

// TestForegroundGroupStaysInTheGroup pins the boundary of the walk: a child in
// another process group is a background job or a daemon the wrapper started,
// not what the pane runs in the foreground, and the walk never yields it.
func TestForegroundGroupStaysInTheGroup(t *testing.T) {
	if _, err := os.Stat("/proc/self/task"); err != nil {
		t.Skip("no procfs")
	}
	// The leader is its own process group leader, with one child in the group
	// and one that setsid moved out of it.
	cmd := exec.Command("sh", "-c", "sleep 1000 & setsid sleep 1000 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start the tree: %v", err)
	}
	leader := cmd.Process.Pid
	t.Cleanup(func() {
		for _, child := range readChildren(leader) {
			if p, err := os.FindProcess(child); err == nil {
				_ = p.Kill()
			}
		}
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	// setsid is reached through a fork of the shell, so the second child is
	// briefly a shell in the leader's group before it moves out. Wait for the
	// tree to settle into one child in the group and one outside it.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		in, out := 0, 0
		for _, child := range readChildren(leader) {
			if readPGRP(child) == leader {
				in++
			} else {
				out++
			}
		}
		if in == 1 && out == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	var got []foregroundInfo
	for member := range foregroundGroup(leader, agentGroupWalkLimit, agentGroupWalkDepth) {
		got = append(got, member)
	}
	if len(got) != 1 {
		t.Fatalf("the walk yielded %d members, want exactly the one in the leader's process group: %+v", len(got), got)
	}
	if got[0].comm != "sleep" || got[0].depth != 1 {
		t.Fatalf("member = %+v, want the in-group sleep at depth 1", got[0])
	}
}
