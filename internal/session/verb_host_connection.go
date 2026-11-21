package session

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// ErrVerbHostRefused reports a host whose link is up and cannot take another
// connection. Its remedy is to close one, which is why it does not share a
// code with host_unreachable, whose remedy is to fix the link.
const ErrVerbHostRefused = "host_refused"

func (d *Daemon) verbOpenHostConnection(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Host        string `json:"host"`
		SSHAuthSock string `json:"ssh_auth_sock"`
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

	hostKey := hostAgentKey(p.Host)
	d.agentNote(cs, p.SSHAuthSock, hostKey)

	ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
	defer cancel()
	conn, err := d.federation.OpenConnectionAs(ctx, p.Host, federation.StreamOpen{Human: d.mayActAsHuman(cs) && !d.rig})
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
		d.relayHostConnection(cs, br, conn, func() { d.agentNote(cs, p.SSHAuthSock, hostKey) })
		LogBasic("Client %s left host %s", cs.clientID, p.Host)
	}
	return map[string]any{
		"type":           "host_connection",
		"host":           p.Host,
		"daemon_version": report.DaemonVersion,
		"protocol":       report.Protocol,
	}, nil
}

func hostConnectionError(host string, err error) *verbError {
	if refused, ok := errors.AsType[*federation.RefusedError](err); ok {
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

func (d *Daemon) relayHostConnection(cs *connState, br *bufio.Reader, remote io.ReadWriteCloser, onInput func()) {
	conn := cs.conn
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(remote, &usedReader{r: br, every: time.Second, used: onInput})
		_ = remote.Close()
	}()
	go func() {
		select {
		case <-d.ctx.Done():
		case <-cs.done:
		case <-done:
			return
		}
		_ = remote.Close()
		_ = conn.Close()
	}()
	_, cerr := io.Copy(conn, remote)
	if errors.Is(cerr, federation.ErrStreamStalled) {
		LogBasic("Client %s stopped reading its connection to a host for too long and the stream was dropped. The link is still up.", cs.clientID)
	} else if cerr != nil {
		LogBasic("Client %s lost its connection to a host: %v", cs.clientID, cerr)
	}
	_ = conn.Close()
	<-done
}

type usedReader struct {
	r       io.Reader
	every   time.Duration
	used    func()
	last    time.Time
	pending atomic.Bool

	started, json bool
	head          [6]byte
	have          int
	skip          uint32
}

func (u *usedReader) Read(b []byte) (int, error) {
	n, err := u.r.Read(b)
	if n > 0 && u.used != nil && u.scan(b[:n]) {
		now := time.Now()
		if wait := u.every - now.Sub(u.last); wait <= 0 {
			u.last = now
			u.used()
		} else if u.pending.CompareAndSwap(false, true) {
			time.AfterFunc(wait, func() {
				u.pending.Store(false)
				u.used()
			})
		}
	}
	return n, err
}

func (u *usedReader) scan(p []byte) bool {
	if !u.started {
		u.started = true
		u.json = p[0] == '{'
	}
	if u.json {
		return true
	}
	found := false
	for len(p) > 0 {
		if u.skip > 0 {
			k := min(uint32(len(p)), u.skip)
			p = p[k:]
			u.skip -= k
			continue
		}
		k := copy(u.head[u.have:], p)
		u.have += k
		p = p[k:]
		if u.have < len(u.head) {
			break
		}
		u.have = 0
		if MessageType(u.head[4]) == MsgInput {
			found = true
		}
		if length := binary.BigEndian.Uint32(u.head[:4]); length > 2 {
			u.skip = length - 2
		}
	}
	return found
}
