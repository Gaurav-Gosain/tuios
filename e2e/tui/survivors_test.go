package tuie2e

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// survivorGrace is how long the processes of a test have to end after its
// teardown. A closing pane's program gets one second to act on the hangup
// (paneCloseGrace in internal/session), so this leaves a wide margin.
const survivorGrace = 5 * time.Second

// failSurvivors fails the test when a process it started still runs after
// its teardown, and kills that process. killDaemon calls it for each
// isolation root a test stops a daemon in. The first call registers one check
// for the test, before the daemons' own stops, so it runs after every daemon,
// its panes and the clients are gone: a link's proxy ends only when both of
// its daemons have. It looks at every root the test named by then.
//
// A process is the test's when its working directory is under a root, or its
// environment points HOME or an XDG folder into one: every process the suite
// starts gets those (xdgKeys), also one that changed its directory or left
// its session with setsid. A process that has begun to exit, or that belongs
// to another user, shows neither, so it is not counted. Linux only: it reads
// /proc.
//
// Left running, such a process outlives the suite: a pane's background job
// that writes to its dead terminal spins on EIO, and a helper that holds
// connections sleeps on for days.
func failSurvivors(t *testing.T, base string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	v, loaded := survivorRoots.LoadOrStore(t, &rootList{})
	roots := v.(*rootList)
	roots.add(base)
	if loaded {
		return
	}
	t.Cleanup(func() {
		defer survivorRoots.Delete(t)
		bases := roots.all()
		deadline := time.Now().Add(survivorGrace)
		left := testProcs(bases)
		for len(left) > 0 && time.Now().Before(deadline) {
			pause(t, "the test's processes end after its teardown", 100*time.Millisecond)
			left = testProcs(bases)
		}
		for _, pid := range left {
			t.Errorf("ASSERTION: a process this test started still runs %s after its teardown, so it is killed now: %s", survivorGrace, describeProc(pid))
			if p, err := os.FindProcess(pid); err == nil && ownedBy(pid, bases) {
				_ = p.Kill()
			}
		}
	})
}

// survivorRoots maps a test to the isolation roots failSurvivors checks.
var survivorRoots sync.Map

// rootList is the isolation roots of one test.
type rootList struct {
	mu    sync.Mutex
	bases []string
}

func (r *rootList) add(base string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.bases, base) {
		r.bases = append(r.bases, base)
	}
}

func (r *rootList) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.bases)
}

// testProcs lists the processes other than this one that belong to a test
// with the isolation roots bases (ownedBy).
func testProcs(bases []string) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		if ownedBy(pid, bases) {
			out = append(out, pid)
		}
	}
	return out
}

// ownedBy reports whether process pid works under one of bases, or has HOME
// or an XDG folder in one.
func ownedBy(pid int, bases []string) bool {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	if cwd, err := os.Readlink(filepath.Join(dir, "cwd")); err == nil && underAny(cwd, bases) {
		return true
	}
	raw, err := os.ReadFile(filepath.Join(dir, "environ"))
	if err != nil {
		return false
	}
	for kv := range bytes.SplitSeq(raw, []byte{0}) {
		k, v, ok := strings.Cut(string(kv), "=")
		if ok && (k == "HOME" || strings.HasPrefix(k, "XDG_")) && underAny(v, bases) {
			return true
		}
	}
	return false
}

// underAny reports whether path is one of bases or in one.
func underAny(path string, bases []string) bool {
	return slices.ContainsFunc(bases, func(base string) bool { return under(path, base) })
}

// under reports whether path is base or in it. A folder removed while a
// process works in it reads as "PATH (deleted)".
func under(path, base string) bool {
	path = strings.TrimSuffix(path, " (deleted)")
	return path == base || strings.HasPrefix(path, base+string(filepath.Separator))
}
