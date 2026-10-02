//go:build !slim

package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// remoteListing asks the machine a pane runs on to list one of its
// directories.
//
// The rail's file section asks the daemon that owns the pane, which was the
// whole fix for a pane reached over a link: a client listing its own disk
// reported that the pane's directory did not exist. A window whose process is
// on another machine moves that same mistake one step along, because the
// daemon that owns the window is not the machine that owns the files either.
// So it asks the one that is.
//
// A failure is reported as the listing's error rather than as a message
// failure. The section has a row to say why it is empty, and "that machine did
// not answer" is the honest thing to put in it.
func (d *Daemon) remoteListing(host, dir string, maxEntries int) *DirListingPayload {
	if d.federation == nil {
		return &DirListingPayload{Dir: dir, Err: "No link to " + host + "."}
	}
	ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
	defer cancel()

	raw, err := d.federation.Call(ctx, host, "read-dir", map[string]any{
		"dir": dir,
		"max": maxEntries,
	})
	if err != nil {
		message, _ := federationErrorText(err)
		if message == "" {
			message = host + " could not list it."
		}
		return &DirListingPayload{Dir: dir, Err: message}
	}
	var out DirListingPayload
	if json.Unmarshal(raw, &out) != nil {
		return &DirListingPayload{Dir: dir, Err: host + " sent a listing this build cannot read."}
	}
	if out.Dir == "" {
		out.Dir = dir
	}
	// The spoof question is never answered for a pane on another machine. It
	// compares an announced directory against a shell's own, and this daemon
	// holds neither: the shell is over there and the pid means nothing here.
	out.Spoofed = false
	return &out
}

// remoteDirWaitMS bounds one wait-dir asked of another machine. The wait is
// asked again when it ends, so this only bounds how long a far daemon keeps a
// watch for a near daemon that went away without closing the link.
const remoteDirWaitMS = 10 * 60 * 1000

// remoteDirWaitBudget is how long the near daemon waits for the answer to one
// wait-dir: the far side's own bound and a margin for the link.
const remoteDirWaitBudget = remoteDirWaitMS*time.Millisecond + fleetCallBudget

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
	params := map[string]any{"dir": dir, "timeout": remoteDirWaitMS}
	for {
		raw, err := fc.call("wait-dir", params, remoteDirWaitBudget)
		if err != nil {
			return err
		}
		var res struct {
			Changed bool `json:"changed"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return fmt.Errorf("an answer this build cannot read: %w", err)
		}
		if res.Changed {
			notify()
		}
	}
}
