package federation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Status is what the hub currently knows about one host.
type Status string

const (
	// StatusConnecting means no attempt has settled yet. It is the state a host
	// is in for the first second of the daemon's life and no longer.
	StatusConnecting Status = "connecting"
	// StatusUp means the link is open and the remote daemon answered the
	// handshake.
	StatusUp Status = "up"
	// StatusUnreachable means the last attempt failed. The machine is off, ssh
	// refused, or the link died.
	StatusUnreachable Status = "unreachable"
	// StatusNoDaemon means ssh and the proxy worked and the remote machine has
	// no daemon running. Nothing is wrong with the link.
	StatusNoDaemon Status = "no_daemon"
	// StatusIncompatible means the remote daemon speaks a control protocol this
	// build does not serve. Section 8: skew is normal, so it is reported as its
	// own state with both versions rather than as a failure.
	StatusIncompatible Status = "incompatible"
)

// Handshake is what a remote daemon reported about itself. The field names
// match the daemon's own hello result, so it decodes straight off the wire.
type Handshake struct {
	Protocol      int    `json:"protocol"`
	MinProtocol   int    `json:"min_protocol"`
	DaemonVersion string `json:"daemon_version"`
	PID           int    `json:"pid"`
	Sessions      int    `json:"sessions"`
}

// link is one host's supervised connection. Exactly one supervisor goroutine
// owns the dial/redial cycle; every reader takes the mutex and sees a snapshot.
type link struct {
	host Host
	opts Options

	mu     sync.Mutex
	status Status
	// reason is the short sentence shown to a user. It is plain English on
	// purpose: it lands in `tuios hosts` and in the sidebar.
	reason  string
	detail  string
	shake   Handshake
	lastOK  time.Time
	lastTry time.Time

	// ctrl is the live control stream's caller, nil unless status is up.
	ctrl *caller
	// tearDown ends the current attempt. The supervisor waits on it.
	tearDown func()

	// settled closes after the first attempt finishes either way, so a listing
	// can wait for first contact instead of reporting "connecting" forever.
	settled     chan struct{}
	settledOnce sync.Once
}

func newLink(h Host, opts Options) *link {
	return &link{
		host:    h,
		opts:    opts,
		status:  StatusConnecting,
		reason:  "The link is starting.",
		settled: make(chan struct{}),
	}
}

func (l *link) markSettled() { l.settledOnce.Do(func() { close(l.settled) }) }

// set records a new state under the lock.
//
// Reaching any state other than connecting settles the link: a listing waits
// for first contact, and first contact is the moment the status is decided, not
// the moment the attempt ends. Settling only when the attempt ended would make
// every listing wait out its own context against a host that is working, which
// is the opposite of what the wait is for.
func (l *link) set(status Status, reason, detail string) {
	l.mu.Lock()
	l.status = status
	l.reason = reason
	l.detail = detail
	if status == StatusUp {
		l.lastOK = l.opts.now()
	}
	l.mu.Unlock()
	if status != StatusConnecting {
		l.markSettled()
	}
}

// report snapshots the link for a listing.
func (l *link) report() HostReport {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := HostReport{
		Host:          l.host.Name,
		Addr:          l.host.Addr,
		Status:        l.status,
		Reason:        l.reason,
		Detail:        l.detail,
		DaemonVersion: l.shake.DaemonVersion,
		Protocol:      l.shake.Protocol,
		MinProtocol:   l.shake.MinProtocol,
		PID:           l.shake.PID,
		Sessions:      l.shake.Sessions,
	}
	if !l.lastOK.IsZero() {
		r.LastOK = l.lastOK.Unix()
	}
	if !l.lastTry.IsZero() {
		r.LastTry = l.lastTry.Unix()
	}
	return r
}

// supervise runs the dial, serve, redial cycle until ctx ends.
func (l *link) supervise(ctx context.Context) {
	backoff := l.opts.InitialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		ok := l.attempt(ctx)
		l.markSettled()
		if ctx.Err() != nil {
			return
		}
		if ok {
			backoff = l.opts.InitialBackoff
		} else if backoff < l.opts.MaxBackoff {
			backoff = min(backoff*2, l.opts.MaxBackoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

// attempt dials once, handshakes, and then blocks until the link dies. It
// returns whether the link was ever up, which is what decides whether the
// backoff resets.
func (l *link) attempt(ctx context.Context) bool {
	l.mu.Lock()
	l.lastTry = l.opts.now()
	l.mu.Unlock()

	dialCtx, cancelDial := context.WithTimeout(ctx, l.host.connectTimeout())
	defer cancelDial()

	tr, err := l.opts.Dial(dialCtx, l.host)
	if err != nil {
		l.set(StatusUnreachable, "The host did not answer.", trimDetail(err.Error()))
		return false
	}

	closed := make(chan struct{})
	var closeOnce sync.Once
	closeAll := func() { closeOnce.Do(func() { _ = tr.Close(); close(closed) }) }
	defer closeAll()

	// The preamble read is bounded by the dial context, so a host that accepts
	// the connection and then says nothing is a failed dial rather than a
	// goroutine parked forever. This is the "accepts then hangs" case, and it
	// is the one that would otherwise look like success.
	br := bufio.NewReaderSize(tr, 32<<10)
	preambleErr := make(chan error, 1)
	go func() { preambleErr <- readPreamble(br) }()
	select {
	case err := <-preambleErr:
		if err != nil {
			l.set(StatusUnreachable, noPreambleReason(tr), trimDetail(tr.Diagnostic()))
			return false
		}
	case <-dialCtx.Done():
		l.set(StatusUnreachable, "The host did not answer in time.", trimDetail(tr.Diagnostic()))
		return false
	}

	// nil accept: the hub answers an inbound open with a close and never hands
	// a peer a stream. Section 1, invariant 1 of the design document.
	m := newMuxRW(br, tr, tr, nil, dialerFirstID)
	muxDone := make(chan struct{})
	go func() {
		defer close(muxDone)
		_ = m.run()
	}()

	ctrl, err := m.Open()
	if err != nil {
		l.set(StatusUnreachable, "The link closed before it was used.", trimDetail(joinDetail(err.Error(), tr.Diagnostic())))
		return false
	}
	c := newCaller(ctrl)

	shakeCtx, cancelShake := context.WithTimeout(ctx, l.opts.CallTimeout)
	shake, verr := handshake(shakeCtx, c, l.opts)
	cancelShake()
	if verr != nil {
		l.mu.Lock()
		l.shake = shake
		l.mu.Unlock()
		l.set(verr.status, verr.reason, trimDetail(joinDetail(verr.detail, tr.Diagnostic())))
		closeAll()
		<-muxDone
		return false
	}

	l.mu.Lock()
	l.shake = shake
	l.ctrl = c
	l.tearDown = closeAll
	l.mu.Unlock()
	l.set(StatusUp, "The host is answering.", "")

	// Hold the link until it dies, the manager stops, or a call declared it
	// hung and tore it down.
	select {
	case <-muxDone:
	case <-ctx.Done():
	case <-closed:
	}

	l.mu.Lock()
	l.ctrl = nil
	l.tearDown = nil
	l.mu.Unlock()
	closeAll()
	<-muxDone
	if ctx.Err() == nil {
		l.set(StatusUnreachable, "The link to the host closed.", trimDetail(tr.Diagnostic()))
	}
	return true
}

// noPreambleReason says which of the two no-preamble failures happened. A child
// that has already exited is ssh giving up, or the remote tuios being missing,
// and its stderr says which. A child still running that never identified itself
// reached something that is not a tuios proxy.
func noPreambleReason(tr Transport) string {
	er, ok := tr.(exitReporter)
	if !ok {
		return "The host did not answer as a tuios link."
	}
	// The pipe closing and the child being reaped are two events, and the pipe
	// wins the race often enough that asking once would misreport ssh's own
	// failure as a bad remote command. A child that is going to exit does so
	// within milliseconds of its pipe closing, so this waits a moment and no
	// longer.
	deadline := time.Now().Add(exitGrace)
	for {
		if exited, _ := er.Exited(); exited {
			return "The host did not answer."
		}
		if time.Now().After(deadline) {
			return "The host did not answer as a tuios link."
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// exitGrace is how long noPreambleReason waits for a child that closed its pipe
// to be reaped.
const exitGrace = 250 * time.Millisecond

// handshakeError carries the state a failed handshake should leave behind.
type handshakeError struct {
	status Status
	reason string
	detail string
}

// handshake runs the hello verb and decides whether this daemon is usable.
func handshake(ctx context.Context, c *caller, opts Options) (Handshake, *handshakeError) {
	raw, err := c.call(ctx, "hello", map[string]any{
		"client":   opts.ClientName,
		"version":  opts.ClientVersion,
		"protocol": opts.VerbProtocol,
	})
	if err != nil {
		var rerr *RemoteError
		switch {
		case errors.As(err, &rerr) && rerr.Code == "unknown_verb":
			// A daemon from before the handshake verb. Usable, and its version
			// is simply not knowable from here.
			return Handshake{}, nil
		case errors.As(err, &rerr) && rerr.Code == "protocol_mismatch":
			return Handshake{}, &handshakeError{
				status: StatusIncompatible,
				reason: "The remote daemon refused this control protocol.",
				detail: rerr.Message,
			}
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, ErrStreamClosed):
			// The proxy answered and then closed the control stream without a
			// word, which is what it does when it cannot reach the daemon
			// socket. On a fresh link nothing else closes that stream, so this
			// is the machine being up with tuios not running on it.
			return Handshake{}, &handshakeError{
				status: StatusNoDaemon,
				reason: "The host is up and no tuios daemon is running on it.",
			}
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			return Handshake{}, &handshakeError{
				status: StatusUnreachable,
				reason: "The remote daemon did not answer the handshake in time.",
			}
		default:
			return Handshake{}, &handshakeError{
				status: StatusUnreachable,
				reason: "The remote daemon did not answer the handshake.",
				detail: err.Error(),
			}
		}
	}

	var shake Handshake
	if err := json.Unmarshal(raw, &shake); err != nil {
		return Handshake{}, &handshakeError{
			status: StatusIncompatible,
			reason: "The remote daemon sent a handshake this build cannot read.",
			detail: err.Error(),
		}
	}
	if shake.Protocol > 0 {
		if shake.Protocol < opts.MinVerbProtocol || shake.MinProtocol > opts.VerbProtocol {
			return shake, &handshakeError{
				status: StatusIncompatible,
				reason: fmt.Sprintf("The host speaks control protocol %d and this daemon serves %d to %d.",
					shake.Protocol, opts.MinVerbProtocol, opts.VerbProtocol),
				detail: "Upgrade tuios on one of the two machines.",
			}
		}
	}
	return shake, nil
}

// call runs one verb on this link. A link that is not up fails immediately,
// which is section 7's rule: nothing is queued and nothing waits on a machine
// that is not there.
func (l *link) call(ctx context.Context, verb string, params any) (json.RawMessage, error) {
	l.mu.Lock()
	c, status, reason := l.ctrl, l.status, l.reason
	l.mu.Unlock()
	if c == nil {
		return nil, &UnreachableError{Host: l.host.Name, Status: status, Reason: reason}
	}

	callCtx, cancel := context.WithTimeout(ctx, l.opts.CallTimeout)
	defer cancel()
	raw, err := c.call(callCtx, verb, params)
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
		// A control stream that timed out or ended cannot be reused. Tearing
		// the link down is what makes the next call fail fast instead of
		// queueing behind the same wedged stream, and the supervisor redials.
		//
		// The status is set here rather than left to the supervisor, which
		// notices a moment later. A listing taken in that moment would say the
		// host is up when the call that just failed proved otherwise, and a
		// listing that is wrong for one round trip is still wrong.
		l.mu.Lock()
		down := l.tearDown
		l.ctrl = nil
		l.mu.Unlock()
		if down != nil {
			l.set(StatusUnreachable, "The host stopped answering.", "")
			down()
		}
	}
	return raw, err
}

// UnreachableError is what a call against a host that is not up returns. It is
// final: the caller reports it, it does not retry a different name.
type UnreachableError struct {
	Host   string
	Status Status
	Reason string
}

func (e *UnreachableError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("host %s is %s. %s", e.Host, e.Status, e.Reason)
	}
	return fmt.Sprintf("host %s is %s", e.Host, e.Status)
}

// joinDetail folds a transport diagnostic onto an error string.
func joinDetail(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ": ")
}

// trimDetail bounds and flattens a detail line. It comes from another machine,
// so it is treated as data: newlines collapse, and it cannot be long enough to
// push anything off a listing.
func trimDetail(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const limit = 200
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}
