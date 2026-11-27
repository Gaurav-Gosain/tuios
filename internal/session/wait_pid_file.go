package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
	"github.com/Gaurav-Gosain/tuios/internal/pidwait"
)

// The pid and file conditions of wait-for: a wait on something outside tuios.
//
// A lead that waited on a background job with a pgrep loop matched its own
// command line and never ended. wait-for pid asks the kernel to say when the
// process exits (internal/pidwait), and wait-for file asks it to say when a
// file appears or is written (internal/dirwatch). Nothing polls, and nothing
// runs once the wait ends.

const (
	waitPid  = "pid"
	waitFile = "file"
)

// Bounds on a file wait.
const (
	waitFileMaxPattern = 1024
	// waitFileMaxLine is the longest line a pattern is matched against. A
	// longer line is matched in pieces of this size.
	waitFileMaxLine = 64 << 10
	// waitFileChunk is how much one read takes.
	waitFileChunk = 256 << 10
	// waitFileShownLine is how much of the matching line the result carries.
	waitFileShownLine = 200
)

// waitPidExit resolves when the process pid exits, at once when there is no
// such process.
func (d *Daemon) waitPidExit(pid int, deadline <-chan time.Time) (any, *verbError) {
	if pid <= 0 {
		return nil, invalidParam("pid", "pid is required for the pid condition, and is a process id above 0")
	}
	ctx, cancel := context.WithCancel(d.ctx)
	defer cancel()
	timedOut := make(chan struct{})
	go func() {
		select {
		case <-deadline:
			close(timedOut)
			cancel()
		case <-ctx.Done():
		}
	}()
	err := pidwait.Wait(ctx, pid)
	switch {
	case err == nil:
		return waitMatched(waitPid, map[string]any{"pid": pid, "was_running": true}), nil
	case errors.Is(err, pidwait.ErrNotRunning):
		return waitMatched(waitPid, map[string]any{"pid": pid, "was_running": false}), nil
	}
	select {
	case <-timedOut:
		return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for process "+strconv.Itoa(pid)+" to exit", &VerbHint{
			Param:  "timeout",
			Detail: "The process still runs. Raise timeout (milliseconds).",
		})
	default:
	}
	if d.ctx.Err() != nil {
		return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
	}
	return nil, newVerbError(ErrVerbInternal, "cannot wait for process "+strconv.Itoa(pid)+": "+err.Error())
}

// fileWaitRefusal is the refusal of a file wait the caller may not make. A
// file wait says whether a file exists and whether its text matches, which
// is a read of the file, so it is held to the rules of a file read: a pane
// needs the files grant, a restricted connection may not, and a linked
// machine needs files and keeps to the read roots and the deny list.
func (d *Daemon) fileWaitRefusal(cs *connState, path string) *verbError {
	if cs == nil {
		return nil
	}
	if cs.viaLink {
		if !d.linkAllowsFiles(cs.linkPeer) {
			return hintedVerbError(ErrVerbForbidden, "wait-for file reads a file, and the link's policy on this machine does not allow files", &VerbHint{
				Detail:  "Nothing was read. Add \"files\" to allow in the [hosts] table for this machine.",
				Missing: []string{"files"},
			})
		}
		return d.checkLinkRead(cs, path)
	}
	if cs.scope.Load() != nil {
		return newVerbError(ErrVerbForbidden, "wait-for file reads a file, and a restricted connection may not. Nothing was read")
	}
	if pa := d.paneAuthority(cs); pa != nil && !pa.grants.Has(GrantFiles) {
		return grantForbidden("wait-for", pa, "wait-for file reads a file, which needs the files grant")
	}
	return nil
}

// waitFileMatch resolves when the file at path exists, and with pattern,
// when a line of it matches. The watches start before each look, so a change
// between the two wakes the wait.
func (d *Daemon) waitFileMatch(cs *connState, path, pattern string, deadline <-chan time.Time) (any, *verbError) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, invalidParam("path", "path is required for the file condition, and must be absolute")
	}
	path = filepath.Clean(path)
	if verr := d.fileWaitRefusal(cs, path); verr != nil {
		return nil, verr
	}
	var re *regexp.Regexp
	if pattern != "" {
		if len(pattern) > waitFileMaxPattern {
			return nil, invalidParam("pattern", "pattern is at most "+strconv.Itoa(waitFileMaxPattern)+" bytes")
		}
		var err error
		if re, err = regexp.Compile(pattern); err != nil {
			return nil, invalidParam("pattern", "invalid regular expression: "+err.Error())
		}
	}

	w := &fileWaiter{path: path, re: re, events: make(chan struct{}, 1)}
	defer w.close()
	existed := false
	for first := true; ; first = false {
		if err := w.watch(); err != nil {
			return nil, newVerbError(ErrVerbInternal, "cannot watch "+echoName(path)+": "+err.Error())
		}
		found, line := w.check()
		if first {
			existed = w.file != nil
		}
		if found {
			res := map[string]any{"path": path, "existed": existed}
			if re != nil {
				res["line"] = line
			}
			return waitMatched(waitFile, res), nil
		}
		select {
		case <-deadline:
			what := "to exist"
			if re != nil {
				what = "to match " + echoName(pattern)
			}
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for "+echoName(path)+" "+what, &VerbHint{
				Param:  "timeout",
				Detail: "Raise timeout (milliseconds), or check the path and the pattern.",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-w.events:
		}
	}
}

// fileWaiter is the state of one file wait: the watches, and how far into
// the file the lines have been matched.
type fileWaiter struct {
	path   string
	re     *regexp.Regexp
	events chan struct{}

	// dir watches the entries of the nearest folder of path that exists, so
	// the file, or a folder on the way to it, appearing wakes the wait.
	// dirInfo tells that folder from a new one made at the same path.
	dir     *dirwatch.Watcher
	dirPath string
	dirInfo os.FileInfo
	// fileW watches the file's writes, while it exists and a pattern is
	// matched.
	fileW *dirwatch.Watcher
	// file is the file that was read, to tell it from a new one at the path.
	file os.FileInfo
	// off is how far it was read, and carry the line it ended in.
	off   int64
	carry []byte
}

func (w *fileWaiter) nudge() { dirwatch.Nudge(w.events) }

// watch points the watches at what is on disk now.
func (w *fileWaiter) watch() error {
	// A folder below can appear while the watch on the one above is made,
	// and that watch would never see it. So after each new watch the folders
	// are looked at again, until the nearest one stays the same.
	for {
		dir, info := nearestDir(filepath.Dir(w.path))
		if w.dir != nil && dir == w.dirPath && os.SameFile(info, w.dirInfo) {
			break
		}
		if w.dir != nil {
			w.dir.Close()
			w.dir = nil
		}
		dw, err := dirwatch.Watch(dir, w.nudge)
		if err != nil {
			return err
		}
		w.dir, w.dirPath, w.dirInfo = dw, dir, info
	}
	st, err := os.Stat(w.path)
	exists := err == nil && st.Mode().IsRegular()
	switch {
	case w.re == nil || !exists:
		if w.fileW != nil {
			w.fileW.Close()
			w.fileW = nil
		}
	case w.fileW == nil || w.file == nil || !os.SameFile(st, w.file):
		if w.fileW != nil {
			w.fileW.Close()
		}
		fw, err := dirwatch.WatchFile(w.path, w.nudge)
		if err != nil {
			return err
		}
		w.fileW = fw
	}
	return nil
}

// nearestDir is dir, or the nearest folder above it that exists.
func nearestDir(dir string) (string, os.FileInfo) {
	for {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir, st
		}
		up := filepath.Dir(dir)
		if up == dir {
			return dir, nil
		}
		dir = up
	}
}

// check reports whether the wait is over, and the matching line.
func (w *fileWaiter) check() (bool, string) {
	st, err := os.Stat(w.path)
	if err != nil {
		w.file = nil
		return false, ""
	}
	if w.re == nil {
		w.file = st
		return true, ""
	}
	if !st.Mode().IsRegular() {
		return false, ""
	}
	if w.file == nil || !os.SameFile(st, w.file) || st.Size() < w.off {
		// A new file at the path, or this one cut short: read it from the
		// start.
		w.off, w.carry = 0, nil
	}
	w.file = st
	f, err := os.Open(w.path)
	if err != nil {
		return false, ""
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(w.off, io.SeekStart); err != nil {
		return false, ""
	}
	buf := make([]byte, waitFileChunk)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			w.off += int64(n)
			if line, ok := w.scan(buf[:n]); ok {
				return true, line
			}
		}
		if err != nil || n == 0 {
			break
		}
	}
	// The line the file ends in so far counts too: a marker printed with no
	// newline yet still matches.
	if len(w.carry) > 0 && w.re.Match(w.carry) {
		return true, shownLine(w.carry)
	}
	return false, ""
}

// scan matches each whole line in data, with the line carried from before.
func (w *fileWaiter) scan(data []byte) (string, bool) {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			w.carry = append(w.carry, data...)
			if len(w.carry) > waitFileMaxLine {
				piece := w.carry
				w.carry = nil
				if w.re.Match(piece) {
					return shownLine(piece), true
				}
			}
			return "", false
		}
		line := data[:i]
		if len(w.carry) > 0 {
			line = slices.Concat(w.carry, line)
			w.carry = nil
		}
		data = data[i+1:]
		if w.re.Match(line) {
			return shownLine(line), true
		}
	}
	return "", false
}

func shownLine(b []byte) string {
	b = bytes.TrimRight(b, "\r")
	if len(b) > waitFileShownLine {
		b = b[:waitFileShownLine]
	}
	return string(bytes.ToValidUTF8(b, []byte("?")))
}

func (w *fileWaiter) close() {
	if w.dir != nil {
		w.dir.Close()
	}
	if w.fileW != nil {
		w.fileW.Close()
	}
}
