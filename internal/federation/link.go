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

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
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
	// StatusApproval means ssh reached the machine and Tailscale SSH holds the
	// login until the person approves it in a browser. The report carries
	// the URL. The dial keeps the connection open, so the link comes up as
	// soon as the person approves. See sshgate.go.
	StatusApproval Status = GateTailscaleCheck
)

// Handshake is what a remote daemon reported about itself. The field names
// match the daemon's own hello result, so it decodes straight off the wire.
type Handshake struct {
	Protocol      int    `json:"protocol"`
	MinProtocol   int    `json:"min_protocol"`
	DaemonVersion string `json:"daemon_version"`
	PID           int    `json:"pid"`
	// Instance names the far daemon's run. See HostReport.Instance.
	Instance string `json:"instance"`
	Sessions int    `json:"sessions"`
	// Load is the far machine's load as its hello said it, nil from a
	// daemon too old to say.
	Load *Load `json:"load,omitempty"`
}

// Load is how busy a machine is, as its daemon's hello says it.
type Load struct {
	// Cores is how many CPUs the machine has.
	Cores int `json:"cores"`
	// Load1 is the one-minute load average.
	Load1 float64 `json:"load1"`
	// MemPct is how much of the memory is in use, 0 to 100, 0 when unknown.
	MemPct float64 `json:"mem_pct,omitempty"`
	// Working is how many agents on the machine are working.
	Working int `json:"agents_working"`
	// LoadOK says whether the machine could read Load1. False when it could
	// not, as on a system tuios reads no load on: Load1 is then 0 and means
	// nothing. Absent from a daemon from before the field, which sent a
	// load it read.
	LoadOK *bool `json:"load_ok,omitempty"`
}

// Known reports whether l holds a load to rank a machine by.
func (l Load) Known() bool { return l.Cores > 0 && (l.LoadOK == nil || *l.LoadOK) }

func (l Load) bounded() Load {
	l.Cores = min(max(l.Cores, 0), 1<<16)
	l.Load1 = min(max(l.Load1, 0), 1<<20)
	l.MemPct = min(max(l.MemPct, 0), 100)
	l.Working = min(max(l.Working, 0), 1<<16)
	if l.Load1 != l.Load1 || l.MemPct != l.MemPct {
		l.Load1, l.MemPct = 0, 0
	}
	return l
}

type link struct {
	host Host
	opts Options

	mu              sync.Mutex
	status          Status
	reason          string
	detail          string
	failure         SSHFailure
	approvalURL     string
	approvalRefused bool
	shake           Handshake
	held            MailHeld
	load            Load
	loadAt          time.Time
	rtt             time.Duration
	lastOK          time.Time
	lastTry         time.Time

	drops        int
	dropReason   string
	stalls       int
	ctrlFailures int

	ctrl     *caller
	mux      *mux
	tearDown func()

	resolved string
	command  string

	settled     chan struct{}
	settledOnce sync.Once

	wake       chan struct{}
	probe      chan struct{}
	lastProbe  time.Time
	asked      bool
	lossNote   string
	fresh      bool
	eagerUntil time.Time
	waiting    bool
	ask        chan chan struct{}
}

func newLink(h Host, opts Options) *link {
	return &link{
		host:    h,
		opts:    opts,
		status:  StatusConnecting,
		reason:  "The link is starting.",
		settled: make(chan struct{}),
		wake:    make(chan struct{}, 1),
		probe:   make(chan struct{}, 1),
		ask:     make(chan chan struct{}, 1),
	}
}

func (l *link) markSettled() { l.settledOnce.Do(func() { close(l.settled) }) }

func (l *link) set(status Status, reason, detail string) {
	l.setFailure(status, reason, detail, "")
}

func (l *link) setUnreachable(reason, detail string) {
	f := ClassifySSHFailure(detail)
	if f != "" {
		reason = f.Reason()
	}
	l.setFailure(StatusUnreachable, reason, detail, f)
}

func (l *link) setFailure(status Status, reason, detail string, failure SSHFailure) {
	l.mu.Lock()
	changed := l.status != status
	l.status = status
	l.reason = reason
	l.detail = detail
	l.failure = failure
	if status != StatusApproval {
		l.approvalURL = ""
		l.approvalRefused = false
	}
	if status == StatusUp {
		l.lastOK = l.opts.now()
	}
	l.mu.Unlock()
	if status != StatusConnecting {
		l.markSettled()
	}
	if changed && l.opts.OnStatus != nil {
		l.opts.OnStatus(l.host.Name, status)
	}
	if status != StatusUp {
		l.noteMailHeld(MailHeld{})
	}
}

func (l *link) noteHello(load *Load, rtt time.Duration) {
	if load == nil {
		return
	}
	l.mu.Lock()
	l.load, l.loadAt, l.rtt = load.bounded(), time.Now(), rtt
	l.mu.Unlock()
}

// MailHeld is what a far machine's hello says about mail it keeps for this
// machine because its own link here is down: how many messages, and that
// link's status there. It carries nothing of the mail itself: no sender, no
// subject, no text. The far machine answers it only for the machine the link
// connection named. Zero when nothing waits, and from an older daemon.
type MailHeld struct {
	Count  int    `json:"count,omitempty"`
	Status Status `json:"status,omitempty"`
}

const maxMailHeldCount = 100000

func (h MailHeld) bounded() MailHeld {
	if h.Count <= 0 {
		return MailHeld{}
	}
	h.Count = min(h.Count, maxMailHeldCount)
	switch h.Status {
	case StatusConnecting, StatusUp, StatusUnreachable, StatusNoDaemon, StatusNoBinary,
		StatusIncompatible, StatusReconnecting, StatusApproval:
	default:
		h.Status = ""
	}
	return h
}

func (l *link) noteMailHeld(h MailHeld) {
	l.mu.Lock()
	changed := l.held != h
	l.held = h
	l.mu.Unlock()
	if changed && l.opts.OnMailHeld != nil {
		l.opts.OnMailHeld(l.host.Name, h)
	}
}

func (l *link) loadStale() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.loadAt.IsZero() && time.Since(l.loadAt) > loadMaxAgeBeats*l.opts.Heartbeat
}

func (l *link) report() HostReport {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := HostReport{
		Host:            l.host.Name,
		Addr:            l.host.Addr,
		Status:          l.status,
		Reason:          l.reason,
		Detail:          l.detail,
		Failure:         l.failure,
		ApprovalURL:     l.approvalURL,
		ApprovalRefused: l.approvalRefused,
		DaemonVersion:   l.shake.DaemonVersion,
		Protocol:        l.shake.Protocol,
		MinProtocol:     l.shake.MinProtocol,
		PID:             l.shake.PID,
		Instance:        l.shake.Instance,
		Sessions:        l.shake.Sessions,
		Command:         l.command,
		Drops:           l.drops,
		DropReason:      l.dropReason,
		Stalls:          l.stalls,
	}
	if !l.loadAt.IsZero() {
		r.Cores, r.Load1, r.MemPct, r.AgentsWorking = l.load.Cores, l.load.Load1, l.load.MemPct, l.load.Working
		r.LoadOK = l.load.LoadOK
		r.LoadAt = l.loadAt.Unix()
		r.RTTMillis = max(l.rtt.Milliseconds(), 1)
	}
	if l.failure != "" {
		r.Fix = l.failure.Fix(l.host.Addr)
		r.FixCommand = l.failure.FixCommand(l.host.Addr, l.host.SSHOptions)
	}
	if !l.lastOK.IsZero() {
		r.LastOK = l.lastOK.Unix()
	}
	if !l.lastTry.IsZero() {
		r.LastTry = l.lastTry.Unix()
	}
	return r
}

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
		l.mu.Lock()
		asked := l.asked
		l.asked = false
		l.mu.Unlock()
		if ok || asked {
			backoff = l.opts.InitialBackoff
		} else if backoff < l.opts.MaxBackoff {
			backoff = min(backoff*2, l.opts.MaxBackoff)
		}
		wait := backoff
		if l.eager() {
			wait = min(wait, approvalRetry)
		}
		if !l.waitToDial(ctx, wait) {
			return
		}
	}
}

func (l *link) waitToDial(ctx context.Context, wait time.Duration) bool {
	clock := newSleepWatch()
	deadline := time.Now().Add(wait)
	t := time.NewTimer(min(wait, l.opts.Heartbeat))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-l.wake:
			return true
		case <-t.C:
		}
		if clock.slept() {
			l.logf("host %s: the computer woke from sleep, so the link is dialed now.", l.host.Name)
			l.mu.Lock()
			l.asked = true
			l.fresh = true
			l.mu.Unlock()
			return true
		}
		left := time.Until(deadline)
		if left <= 0 {
			return true
		}
		t.Reset(min(left, l.opts.Heartbeat))
	}
}

const approvalRetry = 5 * time.Second

const retryWindow = 2 * time.Minute

const retryMinGap = 2 * time.Second

func (l *link) retry() {
	l.mu.Lock()
	now := l.opts.now()
	l.eagerUntil = now.Add(retryWindow)
	l.asked = true
	up := l.status == StatusUp
	recent := !l.lastTry.IsZero() && now.Sub(l.lastTry) < retryMinGap
	if up {
		recent = !l.lastProbe.IsZero() && now.Sub(l.lastProbe) < retryMinGap
		if !recent {
			l.lastProbe = now
		}
	}
	l.mu.Unlock()
	if recent {
		return
	}
	ch := l.wake
	if up {
		ch = l.probe
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (l *link) eager() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status == StatusApproval && l.opts.now().Before(l.eagerUntil)
}

func (l *link) attempt(ctx context.Context) bool {
	h := l.host
	l.mu.Lock()
	l.lastTry = l.opts.now()
	h.fresh, l.fresh = l.fresh, false
	l.lossNote = ""
	fromCache := h.Command == "" && l.resolved != ""
	if fromCache {
		h.Command = l.resolved
	}
	l.mu.Unlock()

	dialCtx, cancelDial := context.WithTimeout(ctx, h.connectTimeout())
	defer cancelDial()

	tr, err := l.opts.Dial(dialCtx, h)
	if err != nil {
		l.setUnreachable("The host did not answer.", trimDetail(err.Error()))
		return false
	}

	closed := make(chan struct{})
	var closeOnce sync.Once
	closeAll := func() { closeOnce.Do(func() { _ = tr.Close(); close(closed) }) }
	defer closeAll()

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
	waitCtx := dialCtx
	gateTick := time.NewTicker(gatePoll)
	defer gateTick.Stop()
	var waitedGate *SSHGate
preamble:
	for {
		select {
		case res := <-preambleDone:
			note = res.note
			if res.err != nil {
				exited, code := awaitChildExit(tr)
				gate := ParseSSHGate(tr.Diagnostic(), l.host.TailscaleLogin)
				switch {
				case gate != nil && gate.Kind == GateTailscalePolicy:
					l.set(StatusUnreachable, gate.Sentence(), trimDetail(tr.Diagnostic()))
				case gate != nil && gate.Kind == GateTailscaleCheck && !gate.Approved:
					l.setApproval("", false, "Tailscale ended the sign-in before you finished it. tuios asks again and shows a new sign-in page.")
				case note.missing:
					l.set(StatusNoBinary, "The link cannot find tuios on the host.", trimDetail(tr.Diagnostic()))
				case exited && code == sshExitCode:
					l.setUnreachable("The host did not answer.", trimDetail(tr.Diagnostic()))
				case exited:
					l.set(StatusUnreachable, "The host did not answer.", trimDetail(tr.Diagnostic()))
				default:
					l.set(StatusUnreachable, "The host did not answer as a tuios link.", trimDetail(tr.Diagnostic()))
				}
				if fromCache && exited && code != sshExitCode {
					l.mu.Lock()
					l.resolved = ""
					l.mu.Unlock()
				}
				return false
			}
			break preamble
		case <-gateTick.C:
			if waitedGate != nil {
				continue
			}
			if g := ParseSSHGate(tr.Diagnostic(), l.host.TailscaleLogin); g != nil && g.Kind == GateTailscaleCheck && !g.Approved {
				waitedGate = g
				l.setApproval(g.URL, g.Refused, g.WaitSentence())
				var cancelWait context.CancelFunc
				waitCtx, cancelWait = context.WithTimeout(ctx, l.opts.ApprovalWait)
				defer cancelWait()
				l.logf("host %s: Tailscale SSH waits for an approval at %s", l.host.Name, g.URL)
			}
		case <-waitCtx.Done():
			if waitedGate != nil {
				l.setApproval("", false, "The Tailscale sign-in page expired. tuios asks again and shows a new sign-in page.")
				return false
			}
			l.set(StatusUnreachable, "The host did not answer in time.", trimDetail(tr.Diagnostic()))
			if !h.fresh {
				l.mu.Lock()
				l.fresh = true
				l.mu.Unlock()
			}
			return false
		}
	}

	l.mu.Lock()
	l.command = h.command()
	if note.command != "" {
		l.command = note.command
		if l.host.Command == "" && cacheableRemotePath(note.command) {
			l.resolved = note.command
		}
	}
	l.mu.Unlock()

	m := newMuxRW(br, tr, tr, nil, dialerFirstID)
	m.stallLimit = l.opts.stallLimit
	m.self = l.opts.Self
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
	shakeAt := time.Now()
	shake, verr := handshake(shakeCtx, c, l.opts)
	shakeRTT := time.Since(shakeAt)
	cancelShake()
	if verr != nil && verr.status == StatusNoDaemon {
		l.set(verr.status, verr.reason, trimDetail(joinDetail(verr.detail, tr.Diagnostic())))
		_ = ctrl.Close()
		c, shake, shakeRTT, verr = l.waitForDaemon(ctx, m, muxDone, verr)
	}
	if verr != nil {
		l.mu.Lock()
		l.shake = shake
		l.mu.Unlock()
		l.set(verr.status, verr.reason, trimDetail(joinDetail(verr.detail, tr.Diagnostic())))
		closeAll()
		<-muxDone
		return false
	}

	l.noteHello(shake.Load, shakeRTT)
	l.mu.Lock()
	l.shake = shake
	l.ctrl = c
	l.mux = m
	l.tearDown = closeAll
	l.mu.Unlock()
	l.set(StatusUp, "The host is answering.", "")
	select {
	case <-l.probe:
	default:
	}
	go l.heartbeat(ctx, m, closed, closeAll)

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
	reason, detail := lossCause(tr, m.Err())
	l.mu.Lock()
	if l.lossNote != "" {
		reason, detail = l.lossNote, ""
	}
	l.drops++
	l.dropReason = reason
	drops := l.drops
	l.mu.Unlock()
	l.logf("host %s: the link dropped after %d up. %s %s", l.host.Name, drops, reason, detail)
	l.set(StatusReconnecting, reason, detail)
	return true
}

const gatePoll = 100 * time.Millisecond

func (l *link) setApproval(url string, refused bool, reason string) {
	l.mu.Lock()
	moved := l.status == StatusApproval && (l.approvalURL != url || l.approvalRefused != refused)
	l.approvalURL = url
	l.approvalRefused = refused
	l.mu.Unlock()
	l.set(StatusApproval, reason, "")
	if moved && l.opts.OnStatus != nil {
		l.opts.OnStatus(l.host.Name, StatusApproval)
	}
}

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

func (l *link) logf(format string, args ...any) {
	if l.opts.Log != nil {
		l.opts.Log(format, args...)
	}
}

func awaitChildExit(tr Transport) (exited bool, code int) {
	er, ok := tr.(exitReporter)
	if !ok {
		return false, 0
	}
	deadline := time.Now().Add(exitGrace)
	for {
		if exited, err := er.Exited(); exited {
			if ee, ok := errors.AsType[*exec.ExitError](err); ok {
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

const exitGrace = 250 * time.Millisecond

const sshExitCode = 255

func (l *link) waitForDaemon(ctx context.Context, m *mux, muxDone <-chan struct{}, last *handshakeError) (*caller, Handshake, time.Duration, *handshakeError) {
	l.mu.Lock()
	l.waiting = true
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.waiting = false
		l.mu.Unlock()
		select {
		case done := <-l.ask:
			close(done)
		default:
		}
	}()
	l.markSettled()

	backoff := l.opts.InitialBackoff
	for {
		var answered chan struct{}
		clock := newSleepWatch()
		deadline := time.Now().Add(backoff)
		t := time.NewTimer(min(backoff, l.opts.Heartbeat))
	wait:
		for {
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, Handshake{}, 0, last
			case <-muxDone:
				t.Stop()
				return nil, Handshake{}, 0, last
			case <-l.wake:
				break wait
			case answered = <-l.ask:
				break wait
			case <-t.C:
			}
			if clock.slept() {
				break wait
			}
			left := time.Until(deadline)
			if left <= 0 {
				break wait
			}
			t.Reset(min(left, l.opts.Heartbeat))
		}
		t.Stop()

		c, shake, rtt, verr := l.askOver(ctx, m)
		if answered != nil {
			close(answered)
		}
		switch {
		case verr == nil:
			return c, shake, rtt, nil
		case verr.status == StatusNoDaemon:
			last = verr
			backoff = min(backoff*2, l.opts.MaxBackoff)
		default:
			l.mu.Lock()
			l.fresh = true
			l.mu.Unlock()
			return nil, shake, 0, verr
		}
	}
}

func (l *link) askOver(ctx context.Context, m *mux) (*caller, Handshake, time.Duration, *handshakeError) {
	l.mu.Lock()
	l.lastTry = l.opts.now()
	l.mu.Unlock()
	ctrl, err := m.Open()
	if err != nil {
		return nil, Handshake{}, 0, &handshakeError{status: StatusUnreachable, reason: "The link closed before it was used.", detail: err.Error()}
	}
	c := newCaller(ctrl)
	shakeCtx, cancel := context.WithTimeout(ctx, l.opts.CallTimeout)
	at := time.Now()
	shake, verr := handshake(shakeCtx, c, l.opts)
	rtt := time.Since(at)
	cancel()
	if verr != nil {
		_ = ctrl.Close()
	}
	return c, shake, rtt, verr
}

func (l *link) askWaiting(ctx context.Context) {
	l.mu.Lock()
	ok := l.waiting && (l.lastTry.IsZero() || l.opts.now().Sub(l.lastTry) >= retryMinGap)
	l.mu.Unlock()
	if !ok {
		return
	}
	done := make(chan struct{})
	select {
	case l.ask <- done:
	default:
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

type handshakeError struct {
	status Status
	reason string
	detail string
}

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
			return Handshake{}, nil
		case errors.As(err, &rerr) && rerr.Code == "protocol_mismatch":
			return Handshake{}, &handshakeError{
				status: StatusIncompatible,
				reason: "The remote daemon refused this control protocol.",
				detail: rerr.Message,
			}
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, ErrStreamClosed):
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
	if err := jsonx.Unmarshal(raw, &shake); err != nil {
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

const maxControlFailures = 3

func (l *link) controlStreamFailed(failed *caller) {
	l.mu.Lock()
	if l.ctrl != failed {
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

	l.mu.Lock()
	l.ctrl = nil
	l.mu.Unlock()
	if down != nil {
		l.set(StatusUnreachable, "The host stopped answering.", "")
		l.logf("host %s: the link is being dialed again, because %d control calls in a row did not answer.", l.host.Name, failures)
		down()
	}
}

func (l *link) openConnection(info StreamOpen) (*Stream, error) {
	l.mu.Lock()
	m, status, reason := l.mux, l.status, l.reason
	l.mu.Unlock()
	if m == nil {
		return nil, &UnreachableError{Host: l.host.Name, Status: status, Reason: reason}
	}
	if !info.Bulk {
		info.Window = true
	}
	s, err := m.open(l.opts.connStallLimit, info)
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

func joinDetail(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ": ")
}

func trimDetail(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const limit = 200
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}
