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
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

const remoteDirWaitMS = 10 * 60 * 1000

const remoteDirWaitBudget = remoteDirWaitMS*time.Millisecond + fleetCallBudget

type dirWatchSlot struct {
	mu   sync.Mutex
	done chan struct{}
}

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

func (cs *connState) stopDirWatch() { cs.dirWatch.replace(false) }

func (d *Daemon) handleWatchDir(cs *connState, msg *Message) error {
	var p WatchDirPayload
	if err := msg.ParsePayload(&p); err != nil {
		return errfmt.Errorf("invalid watch-dir payload: %w", err)
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
		default:
			_ = d.sendMessage(cs, MsgDirChanged, &DirChangedPayload{Dir: dir})
		}
	}
	if host != "" {
		go d.runRemoteDirWatch(host, dir, done, notify)
	} else {
		go func() {
			if verr := d.checkLinkRead(cs, dir); verr != nil {
				return
			}
			d.runLocalDirWatch(dir, done, notify)
		}()
	}
	return nil
}

func watchStartDelayForTest() time.Duration {
	return testknob.Millis("watch_start_delay_ms")
}

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

func (d *Daemon) runRemoteDirWatch(host, dir string, done <-chan struct{}, notify func()) {
	err := d.watchRemoteDir(host, dir, done, notify)
	select {
	case <-done:
	default:
		LogBasic("Stopped watching %s on %s: %v", dir, host, err)
	}
}

var errNoFederation = errors.New("no link to another machine")

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
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			return errfmt.Errorf("an answer this build cannot read: %w", err)
		}
		after = res.Names
		timeout = remoteDirWaitMS
		if first || res.Changed {
			notify()
		}
	}
}

const dirNamesMax = 100_000

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

func (d *Daemon) verbWaitDir(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Dir     string `json:"dir"`
		Timeout int    `json:"timeout"`
		After   string `json:"after"`
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
		if verr := d.checkLinkRead(cs, dir); verr != nil {
			return nil, verr
		}
		return nil, newVerbError(ErrVerbInternal, "cannot watch "+echoName(dir)+": "+err.Error())
	}
	defer w.Close()
	if verr := d.checkLinkRead(cs, dir); verr != nil {
		return nil, verr
	}

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
	names := dirNamesDigest(dir)
	if p.After != "" && names != "" && names != p.After {
		return map[string]any{"dir": dir, "changed": true, "names": names}, nil
	}
	unchanged := map[string]any{"dir": dir, "changed": false, "names": names}
	select {
	case <-events:
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
