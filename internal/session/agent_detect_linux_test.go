//go:build linux

package session

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

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
