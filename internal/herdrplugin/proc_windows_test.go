//go:build windows

package herdrplugin

import (
	"testing"
	"time"
)

// TestStartWithoutAJob is the path a daemon takes when Windows refuses the
// job: the command must still start, resumed from its suspended start, and
// StopPlugin must still kill the command itself.
func TestStartWithoutAJob(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	useJobs = false
	defer func() { useJobs = true }()
	r := NewRunner()
	child := startHelper(t, r, "parent")
	defer killPid(child)
	if !alive(child) {
		t.Fatal("the command did not run")
	}
	r.StopAll(5 * time.Second)
	for _, l := range r.Logs("test.plugin", 1) {
		if l.Status == StatusRunning {
			t.Fatal("StopAll did not end the command")
		}
	}
}
