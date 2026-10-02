package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
)

// Watching a listed folder for a client that cannot watch it itself.
//
// The rail's files section keeps its listing true by asking the kernel when
// the listed folder's names change (app/sidebar_files_watch.go). The client
// can only ask its own kernel, so a folder on another machine stayed as it was
// first read: a file deleted over there stayed on the list (issue #313).
//
// So the client asks its daemon, with MsgWatchDir, and the daemon pushes
// MsgDirChanged when the names change. The daemon that answers is the one the
// folder is nearest to:
//
//   - A session attached on another machine is served by that machine's
//     daemon, over the link. The folder is on its disk, so it watches the
//     folder itself.
//   - A window on another machine inside a session of this daemon is not. Its
//     folder is on the machine that runs its process, so this daemon asks that
//     machine with the wait-dir verb, on a connection of its own over the link,
//     and asks again on the same connection each time one wait ends.
//
// Nothing polls. A wait on the far machine sleeps in the kernel until the folder
// changes, and a link connection that carries one costs nothing while it waits.
// A wait that fails (the link is down, or the far daemon is too old to have
// wait-dir) ends the watch, and the listing is read again when the pane changes
// folder, as it was before this existed.

// dirWatchSlot is a connection's one folder watch.
type dirWatchSlot struct {
	mu   sync.Mutex
	done chan struct{}
}

// replace ends the current watch and returns the done channel of a new one, or
// ends it and returns nil.
func (w *dirWatchSlot) replace(start bool) chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done != nil {
		close(w.done)
		w.done = nil
	}
	if start {
		w.done = make(chan struct{})
	}
	return w.done
}

// stopDirWatch ends the connection's folder watch, when it closes.
func (cs *connState) stopDirWatch() { cs.dirWatch.replace(false) }

// handleWatchDir points the connection's folder watch at the folder named, or
// ends it for an empty one.
func (d *Daemon) handleWatchDir(cs *connState, msg *Message) error {
	var p WatchDirPayload
	if err := msg.ParsePayload(&p); err != nil {
		return fmt.Errorf("invalid watch-dir payload: %w", err)
	}
	if p.Dir == "" || !filepath.IsAbs(p.Dir) {
		cs.dirWatch.replace(false)
		return nil
	}
	dir := filepath.Clean(p.Dir)
	cs.mu.Lock()
	sessionID := cs.sessionID
	cs.mu.Unlock()
	host := d.windowHost(sessionID, p.WindowID)

	done := cs.dirWatch.replace(true)
	notify := func() {
		select {
		case <-done:
			// Replaced while the change settled: the client has moved on.
		default:
			_ = d.sendMessage(cs, MsgDirChanged, &DirChangedPayload{Dir: dir})
		}
	}
	// Off the connection's own goroutine, because starting a watch is a
	// syscall on a path that can be a mount that stopped answering, or a
	// round trip to another machine.
	if host != "" {
		go d.runRemoteDirWatch(host, dir, done, notify)
	} else {
		go d.runLocalDirWatch(dir, done, notify)
	}
	return nil
}

// runLocalDirWatch reports each settled change to dir's names until done.
func (d *Daemon) runLocalDirWatch(dir string, done <-chan struct{}, notify func()) {
	events := make(chan struct{}, 1)
	w, err := dirwatch.Watch(dir, func() { dirwatch.Nudge(events) })
	if err != nil {
		return
	}
	defer w.Close()
	for {
		select {
		case <-done:
			return
		case <-d.ctx.Done():
			return
		case <-events:
		}
		if !dirwatch.AfterBurst(events, done) {
			return
		}
		notify()
	}
}

// runRemoteDirWatch asks host to wait for dir to change, reports each change,
// and asks again, until done or until a wait fails.
func (d *Daemon) runRemoteDirWatch(host, dir string, done <-chan struct{}, notify func()) {
	err := d.watchRemoteDir(host, dir, done, notify)
	select {
	case <-done:
	default:
		LogBasic("Stopped watching %s on %s: %v", dir, host, err)
	}
}

// errNoFederation says this daemon has no links to ask another machine over.
var errNoFederation = errors.New("no link to another machine")

// verbWaitDir answers when the names in a directory on this machine change, or
// when the timeout ends first.
func (d *Daemon) verbWaitDir(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Dir     string `json:"dir"`
		Timeout int    `json:"timeout"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Dir == "" || !filepath.IsAbs(p.Dir) {
		return nil, invalidParam("dir", "wait-dir needs an absolute directory to watch.")
	}
	if p.Timeout < 0 || p.Timeout > maxWaitTimeoutMS {
		return nil, invalidParam("timeout", fmt.Sprintf("timeout is milliseconds from 1 to %d (24 hours)", maxWaitTimeoutMS))
	}
	timeout := defaultWaitTimeout
	if p.Timeout > 0 {
		timeout = time.Duration(p.Timeout) * time.Millisecond
	}
	dir := filepath.Clean(p.Dir)

	events := make(chan struct{}, 1)
	w, err := dirwatch.Watch(dir, func() { dirwatch.Nudge(events) })
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "cannot watch "+echoName(dir)+": "+err.Error())
	}
	defer w.Close()

	// The caller going away ends the wait. The watch on the connection
	// sleeps in the poller, so a far daemon holding a wait for a near one
	// does no work until the folder or the connection changes.
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var gone, ended <-chan struct{}
	if cs != nil {
		ended = cs.hostedEnded
		if cs.conn != nil {
			var stop func()
			gone, stop = watchPeerClose(cs.conn)
			defer stop()
		}
	}
	unchanged := map[string]any{"dir": dir, "changed": false}
	select {
	case <-events:
		// Let the burst settle, so the caller reads the folder once. The
		// change is the answer whatever ends the wait during the settle.
		dirwatch.AfterBurst(events, d.ctx.Done())
		return map[string]any{"dir": dir, "changed": true}, nil
	case <-timer.C:
		return unchanged, nil
	case <-gone:
		return unchanged, nil
	case <-ended:
		return unchanged, nil
	case <-d.ctx.Done():
		return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
	}
}

// verbReadDir lists a directory on this machine.
//
// It is the far half of the rail's file section for a pane whose process runs
// here. The section asks the daemon that owns the pane rather than reading a
// filesystem itself, for the reason it was taught once already: the machine
// with the process is the machine with the files, and any other answer is a
// listing of the wrong disk under the right path.
//
// It carries no authority the link did not already have. A configured host can
// be asked for a shell, and reading the names in a directory is strictly less
// than that.
func (d *Daemon) verbReadDir(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Dir string `json:"dir"`
		Max int    `json:"max"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Dir == "" {
		return nil, invalidParam("dir", "read-dir needs a directory to list.")
	}
	return listDir(filepath.Clean(p.Dir), p.Max), nil
}
