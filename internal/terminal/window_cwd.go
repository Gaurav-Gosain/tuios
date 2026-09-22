package terminal

import (
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
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
// it cannot be determined. How it is read is the platform's business, and on a
// platform with neither procfs nor libproc there is no answer at all; callers
// must treat the empty string as "unknown" rather than as the root directory.
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

	cwd, _ := ShellCWD(w.ShellPgid)
	w.cwd.value = cwd
	return cwd
}

// ShellCWD reads the working directory of the process group leader pgid, and
// says whether it got one. It is the uncached body of CWD, and it takes a
// number rather than a window so a caller off the update goroutine can ask
// without touching a live Window.
//
// The false answer is "nobody looked": this platform cannot read another
// process's working directory, the process is gone, it belongs to another user,
// or the pgid was never recorded. Only a true answer is evidence of anything,
// and a caller must not read the empty string as the root directory. That
// matters beyond the window title: cwdIsSpoofed rests on this, and a false
// answer leaves the sidebar's file actions live on a directory a pane named
// over OSC 7.
//
// The path comes back with its symlinks resolved on every platform that has an
// answer, which is why sameDir compares by identity as well as by name.
func ShellCWD(pgid int) (string, bool) {
	return ptyspawn.ProcessCwd(pgid)
}
