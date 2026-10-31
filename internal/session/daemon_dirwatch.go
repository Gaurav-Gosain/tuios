package session

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
)

// Watching a listed folder for a client that cannot watch it itself.
//
// The rail's files section keeps its listing true by asking the kernel when
// the listed folder's names change (app/sidebar_files_watch.go). The client
// can only ask its own kernel, so on its own a folder on another machine would
// stay as it was first read: a file deleted over there would stay on the list.
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
// A client lists a folder and then asks for the watch. A change in between is in
// neither, and the rail kept a removed file until the next change. So a watch that
// has started says once that the folder changed, and the client reads it again.
// That read costs a listing per watch, and it shows no change when nothing moved.
// A wait on the far machine also takes the names its last answer saw (after), so
// a change between two waits is the answer to the next one. A far daemon too old
// to have this sends no names, and the watch goes on as it did.
//
// Nothing polls. A wait on the far machine sleeps in the kernel until the folder
// changes, and a link connection that carries one costs nothing while it waits.
// A wait that fails (the link is down, or the far daemon is too old to have
// wait-dir) ends the watch, and the listing is read again when the pane changes
// folder.

// remoteDirWaitMS bounds one wait-dir asked of another machine. The wait is
// asked again when it ends, so this only bounds how long a far daemon keeps a
// watch for a near daemon that went away without closing the link.
const remoteDirWaitMS = 10 * 60 * 1000

// remoteDirWaitBudget is how long the near daemon waits for the answer to one
// wait-dir: the far side's own bound and a margin for the link.
const remoteDirWaitBudget = remoteDirWaitMS*time.Millisecond + fleetCallBudget

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
		go func() {
			// Over a link a watch tells when a folder's names change, so it
			// keeps to the read rules a listing and wait-dir keep to. The
			// check is in here because it reads the path, which can be a
			// mount that stopped answering. A refused watch never starts and
			// nothing is sent.
			if verr := d.checkLinkRead(cs, dir); verr != nil {
				return
			}
			d.runLocalDirWatch(dir, done, notify)
		}()
	}
	return nil
}

// watchStartDelayForTest holds back the start of a far watch, so the end-to-end
// test can make a change land before it with no race. It holds only with
// TUIOS_E2E=1, and ordinary runs never set it.
var watchStartDelayForTest = sync.OnceValue(func() time.Duration {
	if os.Getenv("TUIOS_E2E") != "1" {
		return 0
	}
	ms, _ := strconv.Atoi(os.Getenv("TUIOS_E2E_WATCH_START_DELAY_MS"))
	return time.Duration(ms) * time.Millisecond
})

// runLocalDirWatch reports each settled change to dir's names until done.
func (d *Daemon) runLocalDirWatch(dir string, done <-chan struct{}, notify func()) {
	events := make(chan struct{}, 1)
	w, err := dirwatch.Watch(dir, func() { dirwatch.Nudge(events) })
	if err != nil {
		return
	}
	defer w.Close()
	notify()
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

// watchRemoteDir asks host, on one connection of its own, to answer when dir
// changes, and asks again on the same connection each time it answers. It
// returns the error that ended it. Closing done closes the connection, which
// ends the wait on the far side too. A far daemon of any version takes the
// next request on the connection, so this needs nothing new of it.
func (d *Daemon) watchRemoteDir(host, dir string, done <-chan struct{}, notify func()) error {
	if d.federation == nil {
		return errNoFederation
	}
	ctx, cancel := context.WithCancel(d.ctx)
	defer cancel()
	conn, err := d.federation.OpenConnection(ctx, host)
	if err != nil {
		return err
	}
	go func() {
		select {
		case <-done:
		case <-ctx.Done():
		}
		_ = conn.Close()
	}()
	fc := &fleetConn{rw: conn, br: bufio.NewReader(conn)}
	if delay := watchStartDelayForTest(); delay > 0 {
		select {
		case <-time.After(delay):
		case <-done:
			return nil
		}
	}
	// The first wait ends at once. It only starts the far side's watch and reads
	// the names, and the read the notify asks for comes after that.
	timeout := 1
	after := ""
	for first := true; ; first = false {
		params := map[string]any{"dir": dir, "timeout": timeout}
		if after != "" {
			params["after"] = after
		}
		raw, err := fc.call("wait-dir", params, remoteDirWaitBudget)
		if err != nil {
			return err
		}
		var res struct {
			Changed bool   `json:"changed"`
			Names   string `json:"names"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return fmt.Errorf("an answer this build cannot read: %w", err)
		}
		after = res.Names
		timeout = remoteDirWaitMS
		if first || res.Changed {
			notify()
		}
	}
}

// dirNamesMax is the most names dirNamesDigest reads. A folder with more says
// nothing, and its waits work as they did without one.
const dirNamesMax = 100_000

// dirNamesDigest is a short sum of the names in dir, or "" when they cannot be
// read or there are too many. It is what a wait-dir answer hands the next wait.
func dirNamesDigest(dir string) string {
	f, err := os.Open(dir) //nolint:gosec // the caller passed the read check for dir
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	names, err := f.Readdirnames(dirNamesMax + 1)
	if err != nil && !errors.Is(err, io.EOF) || len(names) > dirNamesMax {
		return ""
	}
	slices.Sort(names)
	h := sha256.New()
	for _, n := range names {
		_, _ = io.WriteString(h, n)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// verbWaitDir answers when the names in a directory on this machine change, or
// when the timeout ends first.
func (d *Daemon) verbWaitDir(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Dir     string `json:"dir"`
		Timeout int    `json:"timeout"`
		// After is the names the caller's last answer saw. See the file comment.
		After string `json:"after"`
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

	// The watch starts before the read check. The check takes a few
	// milliseconds on macOS, and a change in that time would be lost to a
	// caller that listed the folder just before it asked to wait.
	events := make(chan struct{}, 1)
	w, err := dirwatch.Watch(dir, func() { dirwatch.Nudge(events) })
	if err != nil {
		if verr := d.checkLinkRead(cs, dir); verr != nil {
			return nil, verr
		}
		return nil, newVerbError(ErrVerbInternal, "cannot watch "+echoName(dir)+": "+err.Error())
	}
	defer w.Close()
	// Over a link a wait tells when a folder's names change, so it keeps to
	// the read rules a listing keeps to. Nothing about the folder is sent
	// before this check.
	if verr := d.checkLinkRead(cs, dir); verr != nil {
		return nil, verr
	}

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
	// Read with the watch running, so a change is in these names or wakes the wait.
	names := dirNamesDigest(dir)
	if p.After != "" && names != "" && names != p.After {
		return map[string]any{"dir": dir, "changed": true, "names": names}, nil
	}
	unchanged := map[string]any{"dir": dir, "changed": false, "names": names}
	select {
	case <-events:
		// Let the burst settle, so the caller reads the folder once. The
		// change is the answer whatever ends the wait during the settle.
		dirwatch.AfterBurst(events, d.ctx.Done())
		return map[string]any{"dir": dir, "changed": true, "names": dirNamesDigest(dir)}, nil
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
