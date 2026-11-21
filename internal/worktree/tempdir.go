package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var tempKinds = []string{"index", "merge-tree"}

type tempDir struct {
	path   string
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

var temps struct {
	mu   sync.Mutex
	live map[string]*tempDir
}

func tempPattern(kind string, pid int) string {
	return "tuios-" + kind + "-" + strconv.Itoa(pid) + "-*"
}

func newTemp(ctx context.Context, kind string) (*tempDir, context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	temps.mu.Lock()
	defer temps.mu.Unlock()
	dir, err := os.MkdirTemp("", tempPattern(kind, os.Getpid()))
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	t := &tempDir{path: dir, cancel: cancel, done: make(chan struct{})}
	if temps.live == nil {
		temps.live = make(map[string]*tempDir)
	}
	temps.live[dir] = t
	return t, ctx, nil
}

func (t *tempDir) remove() {
	t.once.Do(func() {
		t.cancel()
		_ = os.RemoveAll(t.path)
		temps.mu.Lock()
		delete(temps.live, t.path)
		temps.mu.Unlock()
		close(t.done)
	})
}

// RemoveTemp ends the git commands that work in this process's temporary
// folders, and waits up to wait for their callers to remove them. It then
// removes the folders that are left. Call it before the process execs or
// exits. A folder made after it starts is not removed.
func RemoveTemp(wait time.Duration) {
	temps.mu.Lock()
	live := make([]*tempDir, 0, len(temps.live))
	for _, t := range temps.live {
		live = append(live, t)
	}
	temps.mu.Unlock()
	if len(live) == 0 {
		return
	}
	for _, t := range live {
		t.cancel()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
waiting:
	for _, t := range live {
		select {
		case <-t.done:
		case <-timer.C:
			break waiting
		}
	}
	for _, t := range live {
		t.remove()
	}
}

// SweepTemp removes the temporary folders in the temp dir that a process left
// behind: those of a process that is not running, and those of this process
// that are not live. It returns how many it removed. A folder of another user,
// or of a process that runs, stays.
func SweepTemp() int {
	root := os.TempDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	self := os.Getpid()
	removed := 0
	for _, e := range entries {
		pid, ok := tempOwnerPID(e.Name())
		if !ok || !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name())
		if pid == self {
			temps.mu.Lock()
			_, live := temps.live[path]
			if !live && os.RemoveAll(path) == nil {
				removed++
			}
			temps.mu.Unlock()
			continue
		}
		if processAlive(pid) || !ownedByMe(path) {
			continue
		}
		if os.RemoveAll(path) == nil {
			removed++
		}
	}
	return removed
}

func tempOwnerPID(name string) (pid int, ok bool) {
	for _, kind := range tempKinds {
		rest, found := strings.CutPrefix(name, "tuios-"+kind+"-")
		if !found {
			continue
		}
		num, random, found := strings.Cut(rest, "-")
		if !found || random == "" {
			return 0, false
		}
		pid, err := strconv.Atoi(num)
		return pid, err == nil && pid > 0
	}
	return 0, false
}
