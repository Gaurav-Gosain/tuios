package terminal

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// cwdRefreshInterval bounds how often the shell's working directory is read
// from the OS. The value is only consulted while rendering a window title, so a
// readlink per window per frame would be pure overhead; a directory that
// changed a moment ago catching up on the next second is not noticeable.
const cwdRefreshInterval = time.Second

// cwdCache memoises the last working directory read for a window.
type cwdCache struct {
	mu        sync.Mutex
	value     string
	fetchedAt time.Time
}

// CWD returns the shell's current working directory, or the empty string when
// it cannot be determined. It is read from /proc, so it is available on Linux
// (and some BSDs) and empty elsewhere; callers must treat the empty string as
// "unknown" rather than as the root directory.
//
// The result is cached for cwdRefreshInterval because the render path asks for
// it once per window per frame.
func (w *Window) CWD() string {
	if w.ShellPgid == 0 {
		return ""
	}

	w.cwd.mu.Lock()
	defer w.cwd.mu.Unlock()

	if time.Since(w.cwd.fetchedAt) < cwdRefreshInterval {
		return w.cwd.value
	}
	w.cwd.fetchedAt = time.Now()

	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", w.ShellPgid))
	if err != nil {
		w.cwd.value = ""
		return ""
	}
	w.cwd.value = cwd
	return cwd
}
