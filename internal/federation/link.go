package federation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
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
	// StatusNoBinary means ssh worked and the link could not find tuios on the
	// machine: not on the PATH, not at any known install path, and not
	// through the login shell. The fix is on that machine, or is --command.
	StatusNoBinary Status = "no_tuios"
	// StatusIncompatible means the remote daemon speaks a control protocol this
	// build does not serve. Section 8: skew is normal, so it is reported as its
	// own state with both versions rather than as a failure.
	StatusIncompatible Status = "incompatible"
	// StatusReconnecting means the link was up, it dropped, and this daemon is
	// dialing again. It is not the same fact as unreachable: unreachable is a
	// machine that has not answered, and reconnecting is a machine that was
	// answering a moment ago and probably still is. A client that lost a
	// session to this state is coming back to it, and the rail says so rather
	// than showing the host as offline for the second it takes.
	//
	// Nothing is queued against it. Up is still the only state that can be
	// called, so a reconnecting host fails a call at once, exactly as an
	// unreachable one does.
	StatusReconnecting Status = "reconnecting"
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

	// drops counts the times a link that was up went down, and dropReason is
	// the plain sentence for the last of them. They are reported so a person
	// whose link keeps dropping can see how often and why, which is the
	// difference between a keepalive timeout, a stalled stream and a dead
	// sshd. Without them all three read as "the link closed".
	drops      int
	dropReason string
	// stalls counts streams dropped for a reader that stopped reading, over
	// the life of this link.
	stalls int
	// ctrlFailures counts control calls that failed in a row on the live
	// link. It resets on any call that answers.
	ctrlFailures int

	// ctrl is the live control stream's caller, nil unless status is up.
	ctrl *caller
	// mux is the live link's multiplexer, nil unless status is up. It is what
	// a connection to the remote daemon is opened on.
	mux *mux
	// tearDown ends the current attempt. The supervisor waits on it.
	tearDown func()

	// resolved is the tuios path the probe found on this host. It is kept for
	// the life of the link so a redial runs it directly instead of probing
	// again, and dropped when a dial that used it reached the machine and
	// still found no program there. Unused when the host has a configured
	// command.
	resolved string
	// command is the binary the last dial that reached one ran on the host,
	// for the report.
	command string

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
		Command:       l.command,
		Drops:         l.drops,
		DropReason:    l.dropReason,
		Stalls:        l.stalls,
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
	// The host dialed is the configured one, with the path an earlier probe
	// found filled in as its command. That is what makes a redial run the
	// binary directly: the probe happens once per link, not once per dial.
	h := l.host
	l.mu.Lock()
	l.lastTry = l.opts.now()
	fromCache := h.Command == "" && l.resolved != ""
	if fromCache {
		h.Command = l.resolved
	}
	l.mu.Unlock()

	dialCtx, cancelDial := context.WithTimeout(ctx, h.connectTimeout())
	defer cancelDial()

	tr, err := l.opts.Dial(dialCtx, h)
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
	type preambleResult struct {
		note preambleNote
		err  error
	}
	preambleDone := make(chan preambleResult, 1)
	go func() {
		note, err := readPreamble(br)
		preambleDone <- preambleResult{note, err}
	}()
	var note preambleNote
	select {
	case res := <-preambleDone:
		note = res.note
		if res.err != nil {
			exited, code := awaitChildExit(tr)
			switch {
			case note.missing:
				// The machine answered, ran the probe, and the probe found
				// nothing. That is a state of the machine, not of the link,
				// and it is reported as its own status so the listing says
				// where the problem is.
				l.set(StatusNoBinary, "The link cannot find tuios on the host.", trimDetail(tr.Diagnostic()))
			case exited:
				l.set(StatusUnreachable, "The host did not answer.", trimDetail(tr.Diagnostic()))
			default:
				l.set(StatusUnreachable, "The host did not answer as a tuios link.", trimDetail(tr.Diagnostic()))
			}
			// A cached path that reached the machine and ran nothing is
			// stale: the binary moved or was removed. The next dial probes
			// again. 255 is ssh's own code and means the machine was never
			// reached, so the path it knows is kept for when it is.
			if fromCache && exited && code != sshExitCode {
				l.mu.Lock()
				l.resolved = ""
				l.mu.Unlock()
			}
			return false
		}
	case <-dialCtx.Done():
		l.set(StatusUnreachable, "The host did not answer in time.", trimDetail(tr.Diagnostic()))
		return false
	}

	// What the far side runs is known now: the path the probe announced, or
	// the command that was sent when no probe ran.
	l.mu.Lock()
	l.command = h.command()
	if note.command != "" {
		l.command = note.command
		if l.host.Command == "" && cacheableRemotePath(note.command) {
			l.resolved = note.command
		}
	}
	l.mu.Unlock()

	// nil accept: the hub answers an inbound open with a close and never hands
	// a peer a stream. Section 1, invariant 1 of the design document.
	m := newMuxRW(br, tr, tr, nil, dialerFirstID)
	m.stallLimit = l.opts.stallLimit
	m.onStall = func(id uint32) {
		l.mu.Lock()
		l.stalls++
		count := l.stalls
		l.mu.Unlock()
		l.logf("host %s: a stream stopped being read and was dropped. The link is still up. Streams dropped so far: %d.", l.host.Name, count)
	}
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
	l.mux = m
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
	l.mux = nil
	l.tearDown = nil
	l.ctrlFailures = 0
	l.mu.Unlock()
	closeAll()
	<-muxDone

	if ctx.Err() != nil {
		return true
	}
	// Why the link went down, said in one sentence a person can act on. The
	// three causes need three different fixes and used to read identically, so
	// the reason is worked out here rather than left as "the link closed".
	reason, detail := lossCause(tr, m.Err())
	l.mu.Lock()
	l.drops++
	l.dropReason = reason
	drops := l.drops
	l.mu.Unlock()
	l.logf("host %s: the link dropped after %d up. %s %s", l.host.Name, drops, reason, detail)
	// Reconnecting, not unreachable. The machine answered a moment ago, this
	// daemon is about to dial it again, and a listing that called that offline
	// would be reporting a failure that has not happened yet.
	l.set(StatusReconnecting, reason, detail)
	return true
}

// lossCause turns a dead link into the sentence its report carries.
//
// Three failures end a link and they are not the same event. ssh gave up, which
// its own stderr explains and its exit code confirms. The pipe ended with ssh
// still running, which is the far proxy or the far daemon going away. Or this
// side read a frame it could not use, which is a protocol fault and not a
// network one. Naming which is what lets a person fix the right thing.
func lossCause(tr Transport, muxErr error) (reason, detail string) {
	diag := trimDetail(tr.Diagnostic())
	exited, code := awaitChildExit(tr)
	switch {
	case exited && code == sshExitCode:
		return "The connection to the host ended. tuios is connecting again.", diag
	case exited:
		return "The tuios on the host stopped. tuios is connecting again.", trimDetail(joinDetail(diag, fmt.Sprintf("the remote command exited with %d", code)))
	case muxErr != nil && !errors.Is(muxErr, io.EOF) && !errors.Is(muxErr, io.ErrUnexpectedEOF) && !errors.Is(muxErr, ErrLinkClosed):
		return "The host sent something this link cannot read. tuios is connecting again.", trimDetail(joinDetail(muxErr.Error(), diag))
	default:
		return "The link to the host closed. tuios is connecting again.", diag
	}
}

// logf writes one line to whatever the manager was given, or nowhere.
func (l *link) logf(format string, args ...any) {
	if l.opts.Log != nil {
		l.opts.Log(format, args...)
	}
}

// awaitChildExit tells the two no-preamble failures apart. A child that has
// already exited is ssh giving up, or the remote tuios being missing, and its
// stderr says which. A child still running that never identified itself
// reached something that is not a tuios proxy. The exit code comes back with
// the answer, because it is what says whether ssh reached the machine.
//
// A transport that is not a child process is reported as still running, which
// is the answer that blames the link rather than the machine.
func awaitChildExit(tr Transport) (exited bool, code int) {
	er, ok := tr.(exitReporter)
	if !ok {
		return false, 0
	}
	// The pipe closing and the child being reaped are two events, and the pipe
	// wins the race often enough that asking once would misreport ssh's own
	// failure as a bad remote command. A child that is going to exit does so
	// within milliseconds of its pipe closing, so this waits a moment and no
	// longer.
	deadline := time.Now().Add(exitGrace)
	for {
		if exited, err := er.Exited(); exited {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return true, ee.ExitCode()
			}
			return true, 0
		}
		if time.Now().After(deadline) {
			return false, 0
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// exitGrace is how long awaitChildExit waits for a child that closed its pipe
// to be reaped.
const exitGrace = 250 * time.Millisecond

// sshExitCode is what ssh exits with for its own failures: it could not reach
// the machine, or was refused by it. Any other code came from the far side.
const sshExitCode = 255

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
	if err == nil {
		l.mu.Lock()
		l.ctrlFailures = 0
		l.mu.Unlock()
		return raw, nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		l.controlStreamFailed(c)
	}
	return raw, err
}

// maxControlFailures is how many control calls may fail in a row on a link
// whose pipe is still carrying traffic before the link is torn down anyway.
// Three is enough to tell a slow answer from a wedged remote and small enough
// that a genuinely stuck daemon is redialed within a minute.
const maxControlFailures = 3

// controlStreamFailed runs when a call on the control stream did not answer.
//
// It used to tear the whole link down, and that is the bug the maintainer felt.
// The control stream is one stream among many on a shared pipe, and an attached
// session is another. A listing that missed its eight second deadline - which a
// busy pane on a link across an ocean does on its own - killed the ssh child,
// and killing the ssh child threw away the session the person was typing into.
// The rail polls that listing every five seconds while it is open, so the link
// had five seconds to be slow once, over and over, and every miss was fatal.
//
// What happens instead: the stream is replaced, because a stream with an
// unanswered request on it can never be reused, and the link is left alone
// while the pipe is still carrying frames. Only a pipe that has gone quiet, or
// a remote that has failed maxControlFailures calls in a row, ends the link.
func (l *link) controlStreamFailed(failed *caller) {
	l.mu.Lock()
	if l.ctrl != failed {
		// Another call already replaced this stream. Nothing to do, and
		// nothing to count twice.
		l.mu.Unlock()
		return
	}
	m, down := l.mux, l.tearDown
	l.ctrlFailures++
	failures := l.ctrlFailures
	l.mu.Unlock()

	if m != nil && failures < maxControlFailures && m.alive(l.opts.linkQuietLimit) {
		if s, err := m.Open(); err == nil {
			l.mu.Lock()
			if l.ctrl == failed {
				l.ctrl = newCaller(s)
				l.mu.Unlock()
				l.logf("host %s: a listing did not answer in time. The link is still carrying traffic, so it is kept and the control stream is replaced.", l.host.Name)
				return
			}
			l.mu.Unlock()
			_ = s.Close()
			return
		}
	}

	// The pipe is quiet or the remote has stopped answering. The status is set
	// here rather than left to the supervisor, which notices a moment later. A
	// listing taken in that moment would say the host is up when the call that
	// just failed proved otherwise.
	l.mu.Lock()
	l.ctrl = nil
	l.mu.Unlock()
	if down != nil {
		l.set(StatusUnreachable, "The host stopped answering.", "")
		l.logf("host %s: the link is being dialed again, because %d control calls in a row did not answer.", l.host.Name, failures)
		down()
	}
}

// openConnection opens a fresh stream on the live link. On the far side the
// proxy answers it by dialing that machine's daemon socket, so what comes back
// is a raw connection to the remote daemon: whatever is written on it reaches
// that daemon exactly as a local client's bytes would, and whatever the daemon
// writes comes back the same way.
//
// It is the one primitive under every verb that is not a listing. Nothing here
// reads or interprets what crosses; the caller that owns the two ends does.
//
// Like call, a link that is not up fails at once with UnreachableError. A link
// that is up but cannot take another stream fails with RefusedError, which is a
// different remedy: the machine is fine, this side has too many connections
// open to it.
func (l *link) openConnection() (*Stream, error) {
	l.mu.Lock()
	m, status, reason := l.mux, l.status, l.reason
	l.mu.Unlock()
	if m == nil {
		return nil, &UnreachableError{Host: l.host.Name, Status: status, Reason: reason}
	}
	// What this stream carries is an attached session, not a listing, so it
	// gets the limit that was chosen for one. See connectionStallLimit.
	s, err := m.OpenWithStall(l.opts.connStallLimit)
	if err == nil {
		return s, nil
	}
	{
		if errors.Is(err, ErrTooManyStreams) {
			return nil, &RefusedError{Host: l.host.Name, Err: err,
				Reason: fmt.Sprintf("This daemon already holds %d connections to the host. Close one before you open another.", maxStreams)}
		}
		return nil, &UnreachableError{Host: l.host.Name, Status: StatusUnreachable, Reason: "The link to the host closed."}
	}
}

// RefusedError is what an open against a host that is up returns when the link
// itself cannot carry another connection. Like UnreachableError it is final.
type RefusedError struct {
	Host   string
	Reason string
	Err    error
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("host %s refused the connection. %s", e.Host, e.Reason)
}

func (e *RefusedError) Unwrap() error { return e.Err }

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
