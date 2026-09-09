package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// open-host-connection: a connection to the daemon on another machine.
//
// This is the one primitive under everything that is not a listing. A client
// that wants to attach a session on host "build", or run a verb there, asks
// this daemon for a connection to build's daemon and then speaks build's
// daemon's own protocol on it, binary or JSON, exactly as it would on build's
// socket. The daemon in the middle relays bytes and reads none of them.
//
// That shape is chosen over forwarding verbs one by one for three reasons.
// Attaching is a stream and not a request, so a per-verb forwarder would have
// to grow a second channel for it anyway. The remote daemon's own protocol
// already carries version negotiation and its own errors, so nothing here has
// to know which verbs the far side has. And the untrusted fence stays where it
// already is: in the one process that decodes the far side's bytes, which is
// the client that asked, never this daemon.
//
// What this daemon does guarantee: the far side gets no channel into this
// daemon (the link refuses inbound streams), a stream nobody drains is dropped
// without costing the link, and a frame is never larger than the link's cap.

// ErrVerbHostRefused reports a host whose link is up and cannot take another
// connection. Its remedy is to close one, which is why it does not share a
// code with host_unreachable, whose remedy is to fix the link.
const ErrVerbHostRefused = "host_refused"

// verbOpenHostConnection opens the connection and hands the client's
// connection to the relay once the reply has been written.
func (d *Daemon) verbOpenHostConnection(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Host string `json:"host"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Host == "" {
		return nil, invalidParam("host", "open-host-connection needs a host name. Run 'tuios hosts' to see the configured hosts.")
	}
	if p.Host == federation.LocalHostName {
		return nil, invalidParam("host", "host "+echoName(p.Host)+" is this machine. Connect to the daemon socket directly.")
	}
	if verr := d.checkHostParam(p.Host); verr != nil {
		return nil, verr
	}

	ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
	defer cancel()
	conn, err := d.federation.OpenConnection(ctx, p.Host)
	if err != nil {
		return nil, hostConnectionError(p.Host, err)
	}

	var report federation.HostReport
	for _, r := range d.federation.Reports(ctx) {
		if r.Host == p.Host {
			report = r
			break
		}
	}

	LogBasic("Client %s connected through to host %s", cs.clientID, p.Host)
	cs.takeover = func(br *bufio.Reader) {
		d.relayHostConnection(cs, br, conn)
		LogBasic("Client %s left host %s", cs.clientID, p.Host)
	}
	return map[string]any{
		"type":           "host_connection",
		"host":           p.Host,
		"daemon_version": report.DaemonVersion,
		"protocol":       report.Protocol,
	}, nil
}

// hostConnectionError turns a link failure into the error a caller reads,
// naming the machine and what to do about it.
func hostConnectionError(host string, err error) *verbError {
	var refused *federation.RefusedError
	if errors.As(err, &refused) {
		return hintedVerbError(ErrVerbHostRefused, refused.Error(), &VerbHint{
			Param:  "host",
			Detail: "The host is up. Close a connection to it, then try again.",
		})
	}
	message, code := federationErrorText(err)
	hint := &VerbHint{
		Param:   "host",
		Command: "tuios hosts",
		Detail:  "The listing says why the host is not up. Nothing is queued for a host that is down.",
	}
	if code == ErrVerbUnknownHost {
		hint.Detail = "A host name is matched exactly. Nothing is guessed, so a near miss cannot reach the wrong machine."
	}
	return hintedVerbError(code, message, hint)
}

// relayHostConnection copies bytes between the client's connection and the
// stream to the remote daemon until either side ends, then ends the other.
//
// The client's end going away closes the stream, which the far side's proxy
// turns into the remote daemon seeing its client disconnect: the session there
// keeps running, as it would for any client that left. The stream going away,
// which is the link dropping, closes the client's connection: the client sees
// a disconnect and says which machine it lost.
//
// Nothing in here decodes a byte. The bounds on what crosses are the link's
// own, a megabyte per frame and a dropped stream for a reader that stalls, and
// the client's, which caps a message at what it caps a local daemon's at.
func (d *Daemon) relayHostConnection(cs *connState, br *bufio.Reader, remote io.ReadWriteCloser) {
	conn := cs.conn
	// The JSON loop set no deadline and the relay wants none either: a pane
	// can be silent for hours.
	_ = conn.SetReadDeadline(time.Time{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Client to remote. br holds nothing past the request line, because
		// the client sends nothing until the reply has arrived, and it is
		// used rather than the bare connection so a byte it did buffer is not
		// lost.
		_, _ = io.Copy(remote, br)
		_ = remote.Close()
	}()
	go func() {
		// The daemon stopping or the connection being dropped ends the relay
		// from the outside; without this the copy would wait for a byte that
		// no longer has anywhere to go.
		select {
		case <-d.ctx.Done():
		case <-cs.done:
		case <-done:
			return
		}
		_ = remote.Close()
		_ = conn.Close()
	}()
	_, _ = io.Copy(conn, remote)
	// Nothing more can cross once the remote end is gone, so the client's
	// connection is closed whole rather than half: a client that only ever
	// reads sees the end at once.
	_ = conn.Close()
	<-done
}
