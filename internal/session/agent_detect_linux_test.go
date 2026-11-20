//go:build linux

package session

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

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
	// Each child starts as a fork of the shell, a shell in the leader's group,
	// and becomes sleep when it execs. The second also moves out of the group
	// through setsid. Counting one child in the group and one outside is not
	// enough: the one in the group can still be the shell, before its exec.
	// Wait for the tree to settle into an in-group sleep and one child
	// outside the group.
	settled := func() (bool, string) {
		var in []string
		out := 0
		for _, child := range readChildren(leader) {
			if readPGRP(child) == leader {
				in = append(in, readComm(child))
			} else {
				out++
			}
		}
		state := fmt.Sprintf("in the group %q, outside it %d", in, out)
		return len(in) == 1 && in[0] == "sleep" && out == 1, state
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ok, state := settled()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the process tree did not settle within 5s: %s", state)
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
