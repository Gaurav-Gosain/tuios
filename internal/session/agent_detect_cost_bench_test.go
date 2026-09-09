//go:build linux

package session

import (
	"os"
	"os/exec"
	"testing"
)

// The detector runs on a timer against live processes, so its cost per pane is
// a budget rather than a curiosity. These benchmarks read real processes
// through the same procfs path the daemon uses, for the four shapes a pane takes:
// the agent is the leader (the common case, and the walk never runs), an
// ordinary program is the leader (no walk: it is not a wrapper), a wrapper with
// the agent behind it (the walk stops at the first match), and a wrapper over a
// program with many children and no agent (the walk runs to its bound, the worst
// case the design allows).
//
// Measured on 2026-09-09 on the maintainer's box with nothing else running: 14,
// 22, 49 and 201 microseconds respectively. Before the walk existed the first
// two were the same and the last two were 29 and 25, a miss each time. So the
// walk costs nothing where it does not run, and the worst case it allows costs
// a pane 0.01% of one core at the default two-second tick.

func benchLeader(b *testing.B, args ...string) int {
	b.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	if err := cmd.Start(); err != nil {
		b.Skipf("cannot start %v: %v", args, err)
	}
	b.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		for _, child := range readChildren(cmd.Process.Pid) {
			if p, err := os.FindProcess(child); err == nil {
				_ = p.Kill()
			}
		}
	})
	return cmd.Process.Pid
}

func benchResolve(b *testing.B, leader int) {
	b.Helper()
	m := newAgentMatcher(nil)
	b.ReportAllocs()
	for b.Loop() {
		info := readProcessInfo(leader)
		info.pid, info.shellPID = leader, 1
		info.group = foregroundGroup(leader, agentGroupWalkLimit, agentGroupWalkDepth)
		m.identify(info)
	}
}

// fakeAgent returns a binary named like an agent that sleeps, so the matcher has
// something to find without an agent installed.
func fakeAgent(b *testing.B) string {
	b.Helper()
	bin := b.TempDir() + "/crush"
	if err := os.Symlink("/usr/bin/sleep", bin); err != nil {
		b.Skip(err)
	}
	return bin
}

func waitForChildren(leader, n int) {
	for range 10000 {
		if len(readChildren(leader)) >= n {
			return
		}
	}
}

func BenchmarkDetectCostLeaderIsAgent(b *testing.B) {
	benchResolve(b, benchLeader(b, fakeAgent(b), "1000"))
}

func BenchmarkDetectCostLeaderIsEditor(b *testing.B) {
	benchResolve(b, benchLeader(b, "sleep", "1000"))
}

func BenchmarkDetectCostWrapperOverAgent(b *testing.B) {
	leader := benchLeader(b, "sh", "-c", fakeAgent(b)+" 1000; true")
	waitForChildren(leader, 1)
	benchResolve(b, leader)
}

func BenchmarkDetectCostWrapperOverBuild(b *testing.B) {
	leader := benchLeader(b, "sh", "-c", "for i in $(seq 1 30); do sleep 1000 & done; wait")
	waitForChildren(leader, 30)
	benchResolve(b, leader)
}
