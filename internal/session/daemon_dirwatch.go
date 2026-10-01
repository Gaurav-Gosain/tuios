package session

import (
	"bufio"
	"context"
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
//     and asks again each time one wait ends.
//
// Nothing polls. A wait on the far machine sleeps in the kernel until the folder
// changes, and a link connection that carries one costs nothing while it waits.
// A wait that fails (the link is down, or the far daemon is too old to have
// wait-dir) ends the watch, and the listing is read again when the pane changes
// folder, as it was before this existed.

// dirWatchSettle is how long a burst of changes is left to settle before it is
// reported. A `git checkout` or an `rm -r` is thousands of events, and the
// listing only needs the state they leave behind.
const dirWatchSettle = 100 * time.Millisecond

// remoteDirWaitMS bounds one wait-dir asked of another machine. The wait is
// asked again when it ends, so this only bounds how long a far daemon keeps a
// watch for a near daemon that went away without closing the link.
const remoteDirWaitMS = 10 * 60 * 1000

// maxDirWaitReply bounds the far daemon's answer to wait-dir.
const maxDirWaitReply = 64 * 1024

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
	w, err := dirwatch.Watch(dir, func() {
		select {
		case events <- struct{}{}:
		default: // a change is already pending, and one report covers both
		}
	})
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
		select {
		case <-done:
			return
		case <-time.After(dirWatchSettle):
		}
		select {
		case <-events:
		default:
		}
		notify()
	}
}

// runRemoteDirWatch asks host to wait for dir to change, reports each change,
// and asks again, until done or until a wait fails.
func (d *Daemon) runRemoteDirWatch(host, dir string, done <-chan struct{}, notify func()) {
	for {
		changed, err := d.waitRemoteDir(host, dir, done)
		select {
		case <-done:
			return
		default:
		}
		if err != nil {
			LogBasic("Stopped watching %s on %s: %v", dir, host, err)
			return
		}
		if changed {
			notify()
		}
	}
}

// errNoFederation says this daemon has no links to ask another machine over.
var errNoFederation = errors.New("no link to another machine")

// waitRemoteDir asks host, on a connection of its own, to answer when dir
// changes. Closing done closes the connection, which ends the wait on the far
// side too.
func (d *Daemon) waitRemoteDir(host, dir string, done <-chan struct{}) (bool, error) {
	if d.federation == nil {
		return false, errNoFederation
	}
	ctx, cancel := context.WithCancel(d.ctx)
	defer cancel()
	conn, err := d.federation.OpenConnection(ctx, host)
	if err != nil {
		return false, err
	}
	go func() {
		select {
		case <-done:
		case <-ctx.Done():
		}
		_ = conn.Close()
	}()

	params, err := json.Marshal(map[string]any{"dir": dir, "timeout": remoteDirWaitMS})
	if err != nil {
		return false, err
	}
	req, err := json.Marshal(verbRequest{ID: json.RawMessage(`1`), Verb: "wait-dir", Params: params})
	if err != nil {
		return false, err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return false, err
	}
	line, err := readLimitedLine(bufio.NewReader(conn), maxDirWaitReply)
	if err != nil {
		return false, err
	}
	var resp struct {
		Result *struct {
			Changed bool `json:"changed"`
		} `json:"result"`
		Error *verbError `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return false, err
	}
	if resp.Error != nil {
		return false, fmt.Errorf("%s: %s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil {
		return false, errors.New("an answer with no result")
	}
	return resp.Result.Changed, nil
}

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
	w, err := dirwatch.Watch(dir, func() {
		select {
		case events <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "cannot watch "+echoName(dir)+": "+err.Error())
	}
	defer w.Close()

	deadline, stop := d.waitDeadline(cs, timeout)
	defer stop()
	select {
	case <-events:
		// Let the burst settle, so the caller reads the folder once.
		select {
		case <-time.After(dirWatchSettle):
		case <-deadline:
		}
		return map[string]any{"dir": dir, "changed": true}, nil
	case <-deadline:
		return map[string]any{"dir": dir, "changed": false}, nil
	case <-d.ctx.Done():
		return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
	}
}
