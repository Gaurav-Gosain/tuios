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

// Temporary folders: a snapshot, a patch, a restore and a jj status read the
// work tree through an index of their own, and a merge test writes its objects
// to a folder of its own. Each is a folder in the temp dir, named
// tuios-<kind>-<pid>-<random>, that git writes into.
//
// A deferred RemoveAll is not enough to remove one. A process that ends while
// git works in the folder runs no deferred call: a live upgrade execs the new
// image, a stuck call outlives the daemon's shutdown wait, a crash or SIGKILL
// ends everything at once. So every folder is also in a list of this
// process's live folders:
//
//   - RemoveTemp ends the git commands of every live folder and removes the
//     folders. The daemon calls it before a live upgrade execs and before its
//     process exits.
//   - SweepTemp removes the folders that a process left: one whose pid is not
//     running, or this process's own that is not live. A daemon calls it once
//     it serves, which covers a crash and the image a live upgrade starts.

// tempKinds are the kinds of temporary folder, in the name after "tuios-".
var tempKinds = []string{"index", "merge-tree"}

// tempDir is one live temporary folder.
type tempDir struct {
	path   string
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// temps is this process's live temporary folders. mu also holds a folder's
// creation and its entry together, so a sweep never sees a folder of this
// process that is not yet in live.
var temps struct {
	mu   sync.Mutex
	live map[string]*tempDir
}

// tempPattern is the os.MkdirTemp pattern of a folder of kind for pid.
func tempPattern(kind string, pid int) string {
	return "tuios-" + kind + "-" + strconv.Itoa(pid) + "-*"
}

// newTemp makes a temporary folder of kind and a context, from ctx, for the
// git commands that work in it. RemoveTemp ends that context. The caller
// defers remove.
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

// remove ends the folder's git commands and removes the folder. git writes
// index.lock beside the index, and leaves it when it is stopped mid-write, so
// the whole folder goes.
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
	// A caller that did not finish in time has its folder removed under it.
	// Its git commands are ended, so none of them writes there again.
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

// tempOwnerPID reads the pid from a temporary folder's name. ok is false for a
// name that is not one, which includes the names of older builds, which held
// no pid.
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
