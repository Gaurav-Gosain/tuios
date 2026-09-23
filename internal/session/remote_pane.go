package session

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

// A pane in this daemon's session whose process runs on another machine: the
// near half of a global session. The far half is hostedPane in
// hosted_pane.go, and the case for the split is written there.
//
// This is a paneIO and nothing more. PTY takes it in place of the pty it would
// have spawned, and from that line upward nothing changes: the same emulator
// parses the bytes, the same scrollback keeps them, the same ring buffer
// sequences them and the same subscribers receive them. A window holding one
// of these is an ordinary window of an ordinary session, which is what makes a
// global session ordinary everywhere else in the daemon.
//
// The four methods map onto two channels, and which one carries what is forced
// by the shape of the far side. Read, Write and Close are the pane's own
// connection, raw from the open reply onward. Resize cannot be: the connection
// is carrying the process's bytes and framing them to make room for a control
// message would mean inventing a protocol on top of a pty. So a resize goes
// out as a verb on the link's control stream instead, addressed by the pane id
// the open returned.

// remotePaneOpenBudget bounds the open. It covers dialing a stream on a link
// that is already up and the far daemon spawning a process, both of which are
// fast or broken.
const remotePaneOpenBudget = 20 * time.Second

// remotePaneResizeBudget bounds a resize. A resize that does not land is not
// worth waiting on: the next one supersedes it, and the pane is still legible
// at the size it has.
const remotePaneResizeBudget = 10 * time.Second

// maxRemotePaneReply bounds the far daemon's reply line.
const maxRemotePaneReply = 64 * 1024

// paneFederation is the part of federation.Manager a remote pane uses. It is
// an interface so the transport can be tested against a fake pair of daemons
// rather than against a machine over ssh.
type paneFederation interface {
	OpenConnection(ctx context.Context, host string) (io.ReadWriteCloser, error)
	Call(ctx context.Context, host, verb string, params any) (json.RawMessage, error)
}

type remotePane struct {
	host string
	id   string
	fed  paneFederation

	stream io.ReadWriteCloser
	// br is the only reader of stream. It is the reader the open reply was
	// read through, and it is kept rather than discarded because the far side
	// may have written the process's first bytes into the same packet as the
	// reply: dropping it would lose the shell's first prompt.
	br *bufio.Reader

	mu     sync.Mutex
	closed bool
	// cwd is where the far machine last said the process was, with the time it
	// said so and whether an ask is already out. See Cwd.
	cwd         string
	cwdAt       time.Time
	cwdInflight bool
	// onCwdChange is called when the far machine reports a directory that is
	// not the one held, so the session can tell its clients. It is set by the
	// session that owns the pane and is nil in a test that builds one by hand.
	onCwdChange func()
	// fg is what the far machine last said is running in the pane, on the same
	// terms as cwd: a cached answer, a time it was given, and whether an ask
	// is already out. See Foreground.
	fg         foregroundInfo
	fgRunning  bool
	fgAt       time.Time
	fgInflight bool

	// callsToken opens the pane's report channel on the far machine; see
	// hosted_calls.go. Empty from a far daemon too old to have one.
	callsToken string
	// done is closed when the pane closes. It is nil in a test that builds a
	// pane by hand.
	done chan struct{}

	// resumeToken and grace are what the far machine gave for reattaching
	// the pane after the link drops: the secret, and how long it keeps the
	// process. Empty and zero from a far daemon too old to keep one, or one
	// whose policy gives no grace; the pane then ends with the link, as it
	// always did. See hosted_resume.go for the far half.
	resumeToken string
	grace       time.Duration
	// received counts the bytes of the process's output read so far, which
	// is where a reattach asks the far machine to resume from. Only Read
	// touches it.
	received int64
	// pendingErr is a read error that came with bytes, kept for the next Read.
	pendingErr error
	// link is "reconnecting" while the link is lost and the pane is being
	// reattached, and linkUntil is when the far machine stops keeping the
	// process. onLinkChange tells the session, so its clients are told.
	link         string
	linkUntil    time.Time
	onLinkChange func()
	// width and height are the last size asked for, sent again after a
	// reattach.
	width, height int
}

// remotePaneLinkReconnecting is the link state of a pane being reattached.
const remotePaneLinkReconnecting = "reconnecting"

// linkState is the pane's link state and, while reconnecting, when the far
// machine's grace ends.
func (p *remotePane) linkState() (string, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.link, p.linkUntil
}

// isClosed reports whether Close has run.
func (p *remotePane) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// openRemotePane starts a process on host and returns the pane it speaks to.
func openRemotePane(ctx context.Context, fed paneFederation, host string, spec hostedPaneSpec) (*remotePane, error) {
	if fed == nil {
		return nil, fmt.Errorf("this daemon has no links, so it cannot put a pane on %s", host)
	}
	stream, err := fed.OpenConnection(ctx, host)
	if err != nil {
		return nil, err
	}

	opened, br, err := openPaneReply(stream, spec)
	if err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("tuios on %s could not start a pane: %w", host, err)
	}
	p := &remotePane{
		host: host, id: opened.Pane, fed: fed, stream: stream, br: br,
		callsToken: opened.CallsToken, done: make(chan struct{}),
		width: spec.Width, height: spec.Height,
	}
	if opened.ResumeToken != "" && opened.Grace > 0 {
		p.resumeToken = opened.ResumeToken
		p.grace = time.Duration(opened.Grace) * time.Second
	}
	return p, nil
}

// openPaneOn sends open-pane on a fresh connection to the far daemon and reads
// its reply. On success the connection is the pty from here on, and the reader
// that is returned is the only one that may read it.
func openPaneOn(stream io.ReadWriteCloser, spec hostedPaneSpec) (string, *bufio.Reader, error) {
	opened, br, err := openPaneReply(stream, spec)
	return opened.Pane, br, err
}

// paneOpened is the part of the open-pane reply the owner keeps. CallsToken is
// empty from a far daemon too old to carry reports from the pane.
type paneOpened struct {
	Pane        string `json:"pane"`
	CallsToken  string `json:"calls_token"`
	ResumeToken string `json:"resume_token"`
	Grace       int64  `json:"grace"`
	Resumed     bool   `json:"resumed"`
	Gap         bool   `json:"gap"`
}

// openPaneReply is openPaneOn with the whole reply.
//
// A far daemon from before reports from hosted panes refuses the window param
// with invalid_params, because every verb line is checked against the verb's
// schema, and one from before resumable panes refuses resumable the same way.
// That refusal comes before anything is spawned and leaves the stream reading
// verb lines, so the request is sent again on the same stream without the
// param it named. The pane then opens as it did on that daemon: with no calls
// token and no report channel without window, and ending with the link
// without resumable.
func openPaneReply(stream io.ReadWriteCloser, spec hostedPaneSpec) (paneOpened, *bufio.Reader, error) {
	br := bufio.NewReader(stream)
	droppedWindow := false
	var resp openPaneResponse
	var err error
	// At most one retry per optional param: the far daemon names one unknown
	// param per refusal.
	for range 3 {
		if resp, err = sendOpenPane(stream, br, spec); err != nil {
			return paneOpened{}, nil, err
		}
		switch {
		case spec.Resumable && refusesParam(resp.Error, "resumable"):
			spec.Resumable = false
			continue
		case spec.Window != "" && refusesParam(resp.Error, "window"):
			spec.Window = ""
			droppedWindow = true
			continue
		}
		break
	}
	if resp.Result != nil {
		if droppedWindow {
			// A token from a daemon that did not take the window would
			// promise a channel with no window behind it. Such a daemon sends
			// none, and one that did is not believed.
			resp.Result.CallsToken = ""
		}
		if !spec.Resumable && spec.Resume == nil {
			resp.Result.ResumeToken, resp.Result.Grace = "", 0
		}
	}
	if resp.Error != nil {
		if resp.Error.Code == ErrVerbUnknownVerb {
			return paneOpened{}, nil, fmt.Errorf("that machine's tuios is too old to host a pane. Update it, then restart its daemon with 'tuios kill-server'")
		}
		return paneOpened{}, nil, resp.Error
	}
	if resp.Result == nil || resp.Result.Pane == "" {
		return paneOpened{}, nil, fmt.Errorf("the reply named no pane")
	}
	return *resp.Result, br, nil
}

// openPaneResponse is one reply to open-pane.
type openPaneResponse struct {
	Result *paneOpened `json:"result"`
	Error  *verbError  `json:"error"`
}

// sendOpenPane writes one open-pane request for spec and reads its reply
// through br, which must be the only reader of stream.
func sendOpenPane(stream io.Writer, br *bufio.Reader, spec hostedPaneSpec) (openPaneResponse, error) {
	params, err := json.Marshal(spec)
	if err != nil {
		return openPaneResponse{}, err
	}
	req, err := json.Marshal(verbRequest{
		ID:     json.RawMessage(`1`),
		Verb:   "open-pane",
		Params: params,
	})
	if err != nil {
		return openPaneResponse{}, err
	}
	if _, err := stream.Write(append(req, '\n')); err != nil {
		return openPaneResponse{}, fmt.Errorf("cannot ask for a pane: %w", err)
	}
	line, err := readLimitedLine(br, maxRemotePaneReply)
	if err != nil {
		return openPaneResponse{}, fmt.Errorf("no answer to the pane request: %w", err)
	}
	var resp openPaneResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return openPaneResponse{}, fmt.Errorf("the reply cannot be read by this build: %w", err)
	}
	return resp, nil
}

// refusesParam reports whether verr is a far daemon's schema check refusing
// open-pane's param name, which is how a daemon from before that param
// answers it.
func refusesParam(verr *verbError, name string) bool {
	if verr == nil || verr.Code != ErrVerbInvalidParams {
		return false
	}
	if verr.Hint != nil && verr.Hint.Param != "" {
		return verr.Hint.Param == name
	}
	return strings.Contains(verr.Message, "no parameter") && strings.Contains(verr.Message, name)
}

// Read returns the process's output. The stream ends when the far side closes
// it, which is what the process exiting and the link dropping both look like
// from here. A pane the far machine keeps for a grace is reattached (see
// reconnect), and Read carries on from the byte it stopped at; any other end
// is returned, and PTY treats it the way it treats a local shell exiting.
func (p *remotePane) Read(b []byte) (int, error) {
	for {
		p.mu.Lock()
		br, pending := p.br, p.pendingErr
		p.pendingErr = nil
		p.mu.Unlock()
		err := pending
		if err == nil {
			var n int
			n, err = br.Read(b)
			if n > 0 {
				p.mu.Lock()
				p.received += int64(n)
				if err != nil {
					p.pendingErr = err
				}
				p.mu.Unlock()
				return n, nil
			}
			if err == nil {
				continue
			}
		}
		if p.isClosed() || p.resumeToken == "" {
			return 0, err
		}
		if rerr := p.reconnect(); rerr != nil {
			if p.isClosed() {
				return 0, err
			}
			return 0, io.EOF
		}
	}
}

// errPaneReconnecting is what a write gets while the link is being restored.
var errPaneReconnecting = errors.New("the link to this pane's machine is down and tuios is reconnecting")

// Write sends input to the process. While the pane is being reattached there
// is nowhere to send it, and the keystrokes are refused rather than queued:
// typing into a pane whose screen is frozen and replaying it later is worse
// than typing again.
func (p *remotePane) Write(b []byte) (int, error) {
	p.mu.Lock()
	closed, stream, link := p.closed, p.stream, p.link
	p.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	if link != "" {
		return 0, errPaneReconnecting
	}
	return stream.Write(b)
}

// Resize tells the far daemon the rectangle this pane is being drawn into.
//
// It is sent on the link's control stream rather than on the pane's own
// connection, and it is best effort: a resize that fails leaves the pane
// readable at the size it already had, and the next layout change sends
// another. A pane that the far side has forgotten reports itself, so the
// caller learns the process is gone from the resize as well as from the read.
func (p *remotePane) Resize(width, height int) error {
	p.mu.Lock()
	closed := p.closed
	p.width, p.height = width, height
	p.mu.Unlock()
	if closed {
		return io.ErrClosedPipe
	}
	ctx, cancel := context.WithTimeout(context.Background(), remotePaneResizeBudget)
	defer cancel()
	_, err := p.fed.Call(ctx, p.host, "resize-pane", map[string]any{
		"pane":   p.id,
		"width":  width,
		"height": height,
	})
	return err
}

// Close ends the pane. Closing the stream is what tells the far side: its
// relay sees the read end, and it kills the process rather than leaving a
// shell on a pty nobody holds.
//
// A pane the far machine would keep for a grace is ended there with
// close-pane as well, since a closed stream is also what a dropped link looks
// like, and the far machine would otherwise keep the process waiting for a
// reattach that is not coming. It is sent on its own goroutine and bounded:
// Close must not wait on another machine, and a link that is down cannot
// carry it anyway, in which case the grace ends the process there.
func (p *remotePane) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	if p.done != nil {
		close(p.done)
	}
	stream := p.stream
	p.mu.Unlock()
	if p.resumeToken != "" && p.fed != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), remotePaneResizeBudget)
			defer cancel()
			_, _ = p.fed.Call(ctx, p.host, "close-pane", map[string]any{"pane": p.id})
		}()
	}
	return stream.Close()
}

// remotePaneRetryMin and remotePaneRetryMax bound the wait between attempts
// to reattach a pane: soon after the drop, when a flapping link is most
// likely back, then no more often than every few seconds.
const (
	remotePaneRetryMin = 250 * time.Millisecond
	remotePaneRetryMax = 5 * time.Second
)

// reconnect reattaches the pane after its stream ended: it opens a new
// connection to the far daemon and sends open-pane with resume, until one is
// answered or the far machine's grace runs out. While it tries, the pane's
// link state is reconnecting, which the session shows its clients.
//
// A reply of unknown_pane, forbidden or invalid_params is final: the process
// exited, its grace ran out, or the far machine will not take the token. That
// and a grace that ran out end the pane, which is returned as an error.
//
// The first attempt is made before the pane says it is reconnecting. A stream
// also ends when the far process exits, and then the link is up and the far
// daemon answers unknown_pane at once: the pane closes without flashing a
// reconnect it never needed.
func (p *remotePane) reconnect() error {
	deadline := time.Now().Add(p.grace)
	wait := remotePaneRetryMin
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		if p.isClosed() {
			return io.ErrClosedPipe
		}
		if attempt == 1 {
			p.setLink(remotePaneLinkReconnecting, deadline)
			LogBasic("Pane %s on %s lost its link; reattaching for up to %s", shortID(p.id), p.host, p.grace)
		}
		opened, stream, br, final := p.reattachOnce()
		if stream != nil {
			p.mu.Lock()
			old := p.stream
			// The bytes the old reader held and the pane had not read are
			// the ones the far machine replays from received, so dropping
			// the old reader loses nothing and repeats nothing.
			p.stream, p.br = stream, br
			width, height, closed := p.width, p.height, p.closed
			p.mu.Unlock()
			_ = old.Close()
			if closed {
				_ = stream.Close()
				return io.ErrClosedPipe
			}
			p.setLink("", time.Time{})
			LogBasic("Pane %s on %s is reattached", shortID(p.id), p.host)
			p.afterReattach(opened.Gap, width, height)
			return nil
		}
		if final {
			break
		}
		select {
		case <-p.done:
			return io.ErrClosedPipe
		case <-time.After(wait):
		}
		wait = min(wait*2, remotePaneRetryMax)
	}
	p.setLink("", time.Time{})
	LogBasic("Pane %s on %s could not be reattached, so it is closed", shortID(p.id), p.host)
	return errPaneGone
}

// errPaneGone is a pane that cannot be reattached.
var errPaneGone = errors.New("the pane's process on the other machine is gone")

// reattachOnce makes one attempt. It returns the new stream and its reader on
// success, and final when the far machine said the pane cannot come back.
func (p *remotePane) reattachOnce() (paneOpened, io.ReadWriteCloser, *bufio.Reader, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), remotePaneOpenBudget)
	stream, err := p.fed.OpenConnection(ctx, p.host)
	cancel()
	if err != nil {
		return paneOpened{}, nil, nil, false
	}
	p.mu.Lock()
	offset := p.received
	p.mu.Unlock()
	spec := hostedPaneSpec{Resume: &hostedResume{Pane: p.id, Token: p.resumeToken, Offset: offset}}
	br := bufio.NewReader(stream)
	resp, err := sendOpenPane(stream, br, spec)
	if err != nil {
		_ = stream.Close()
		return paneOpened{}, nil, nil, false
	}
	if resp.Error != nil {
		_ = stream.Close()
		switch resp.Error.Code {
		case ErrVerbUnknownPane, ErrVerbForbidden, ErrVerbInvalidParams, ErrVerbUnknownVerb:
			return paneOpened{}, nil, nil, true
		}
		return paneOpened{}, nil, nil, false
	}
	if resp.Result == nil || resp.Result.Pane != p.id {
		_ = stream.Close()
		return paneOpened{}, nil, nil, true
	}
	return *resp.Result, stream, br, false
}

// afterReattach sends the pane's size again, since a resize while the link
// was down went nowhere. When more output was missed than the far ring holds,
// the size is nudged down a row and back, so a full screen program draws its
// whole screen again rather than leaving what the gap cut in half.
func (p *remotePane) afterReattach(gap bool, width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), remotePaneResizeBudget)
		defer cancel()
		if gap && height > 1 {
			_, _ = p.fed.Call(ctx, p.host, "resize-pane", map[string]any{"pane": p.id, "width": width, "height": height - 1})
		}
		_, _ = p.fed.Call(ctx, p.host, "resize-pane", map[string]any{"pane": p.id, "width": width, "height": height})
	}()
}

// setLink records the link state and tells the session.
func (p *remotePane) setLink(state string, until time.Time) {
	p.mu.Lock()
	changed := p.link != state
	p.link, p.linkUntil = state, until
	notify := p.onLinkChange
	p.mu.Unlock()
	if changed && notify != nil {
		notify()
	}
}

// Host is the machine the process runs on.
func (p *remotePane) Host() string { return p.host }

// SetFederation gives the session the links a window on another machine is
// opened over. The daemon installs it as each session is created; a session
// without one can hold only windows of its own, which is what every session
// built by a test or by a daemon with no [hosts] table is.
func (s *Session) SetFederation(fed paneFederation) {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()
	s.fed = fed
}

// SetRemotePaneHook installs what is told of each window this session opens
// on another machine. The daemon uses it to hold the pane's report channel.
func (s *Session) SetRemotePaneHook(hook func(windowID string, p *remotePane)) {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()
	s.onRemotePane = hook
}

// openRemotePaneFor starts this session's window on another machine.
//
// The terminal type and the colour support travel with the request, because
// the pane is drawn by this session's emulator and the program at the far end
// has to be told what it is really talking to. The shell does not travel: see
// below.
func (s *Session) openRemotePaneFor(windowID, host string, width, height int, cwd string, command []string) (paneIO, error) {
	fed := s.fed
	if fed == nil {
		return nil, fmt.Errorf("this daemon has no links, so a window cannot be put on %s. Add it to the [hosts] table in the config", host)
	}
	spec := hostedPaneSpec{
		Width:   width,
		Height:  height,
		Cwd:     cwd,
		Command: command,
		Session: s.Name,
	}
	// The window id goes only when there is a daemon to hold the report
	// channel it promises: a far machine that gets it exports it and waits for
	// the channel before answering a report.
	if s.onRemotePane != nil {
		spec.Window = windowID
	}
	// Ask the far machine to keep the process through a dropped link. It
	// decides how long from its own policy; an older one refuses the param
	// and the pane ends with the link as before.
	spec.Resumable = true
	if s.config != nil {
		// The terminal type travels and the shell does not.
		//
		// TERM and COLORTERM describe the emulator the program is talking to,
		// and that emulator is here, so this session's answer is the right one
		// wherever the process runs. The shell is a file that has to exist on
		// the machine running it. Sending this session's shell made the far
		// side try to exec a path from this machine: a laptop running zsh
		// asked a Linux host for /bin/zsh and got "no such file or directory".
		//
		// So the far machine picks its own shell, which is what open-pane does
		// with an empty one. A caller that wants a particular shell over there
		// can still name it, but it has to be a path that exists over there.
		spec.Term, spec.ColorTerm = s.config.Term, s.config.ColorTerm
	}
	ctx, cancel := context.WithTimeout(context.Background(), remotePaneOpenBudget)
	defer cancel()
	p, err := openRemotePane(ctx, fed, host, spec)
	if err != nil {
		return nil, err
	}
	p.onCwdChange = s.PublishLiveFacts
	p.mu.Lock()
	p.onLinkChange = s.PublishLiveFacts
	p.mu.Unlock()
	return p, nil
}

// Foreground is what the far machine last said is running in this pane, and
// whether it could read anything at all.
//
// It is the answer the agent detector needs and the one thing the daemon that
// owns the window cannot work out for itself: the pane's process is on another
// machine, so the pid here is zero and every tier that starts from a process
// gives up. Asking the far machine is the only way to know, which is exactly
// the argument pane-cwd makes about directories.
//
// It never waits. The cached answer is returned at once and a stale one starts
// a refresh on its own goroutine, because this is called from the detector's
// tick and a round trip to another machine must not hold that up. The first
// call therefore reports nothing running, which is what a pane whose process
// has not been looked at yet honestly is.
func (p *remotePane) Foreground() (foregroundInfo, bool) {
	p.mu.Lock()
	info, running := p.fg, p.fgRunning
	asked, closed, inflight := p.fgAt, p.closed, p.fgInflight
	stale := time.Since(asked) > remotePaneAgentTTL
	if !closed && stale && !inflight {
		p.fgInflight = true
		go p.refreshForeground()
	}
	p.mu.Unlock()
	return info, running
}

// remotePaneAgentTTL is how long an answer about what a pane is running stays
// good before it is asked for again.
//
// Longer than the directory's. A directory changes the moment somebody types
// cd and the rail is showing it, so a second is already slow. What a pane is
// running changes when a command starts or ends, the detector's own tick is
// two seconds, and the answer costs a round trip to another machine.
const remotePaneAgentTTL = 2 * time.Second

// refreshForeground asks the far machine what the pane is running.
func (p *remotePane) refreshForeground() {
	ctx, cancel := context.WithTimeout(context.Background(), remotePaneResizeBudget)
	defer cancel()
	raw, err := p.fed.Call(ctx, p.host, "pane-agent", map[string]any{"pane": p.id})

	p.mu.Lock()
	defer p.mu.Unlock()
	p.fgInflight = false
	// The clock moves whether or not the answer was useful, so a machine that
	// is refusing is asked on the same schedule rather than once per tick.
	p.fgAt = time.Now()
	if err != nil {
		return
	}
	var res struct {
		Running  bool     `json:"running"`
		Comm     string   `json:"comm"`
		Argv     []string `json:"argv"`
		Exe      string   `json:"exe"`
		PID      int      `json:"pid"`
		ShellPID int      `json:"shell_pid"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return
	}
	p.fg = foregroundInfo{
		comm:     res.Comm,
		argv:     res.Argv,
		exe:      res.Exe,
		pid:      res.PID,
		shellPID: res.ShellPID,
	}
	p.fgRunning = res.Running
}

// remotePaneCwdTTL is how long a directory the far machine gave stays good
// before a fresher one is asked for. A shell changes directory when somebody
// types cd, so a second is both far faster than anyone types and slow enough
// that a rail redrawing at sixty frames a second asks once.
const remotePaneCwdTTL = time.Second

// Cwd is where the far machine last said the pane's process was.
//
// It never waits. The caller is GetState, which runs on the render path, and a
// call over a link takes about as long as a frame does even when the link is
// healthy. So this answers from the last reply and starts a new ask when that
// one is stale, which means the first call after a cd is a frame behind and
// every one after it is current. A rail one frame behind on a directory is not
// a fault anybody can see; a rail that stops drawing while it asks is.
func (p *remotePane) Cwd() (string, bool) {
	p.mu.Lock()
	cwd, asked, closed, inflight := p.cwd, p.cwdAt, p.closed, p.cwdInflight
	stale := time.Since(asked) > remotePaneCwdTTL
	if !closed && stale && !inflight {
		p.cwdInflight = true
		go p.refreshCwd()
	}
	p.mu.Unlock()
	return cwd, cwd != ""
}

// refreshCwd asks the far machine where the pane is and stores the answer.
func (p *remotePane) refreshCwd() {
	ctx, cancel := context.WithTimeout(context.Background(), remotePaneResizeBudget)
	defer cancel()
	raw, err := p.fed.Call(ctx, p.host, "pane-cwd", map[string]any{"pane": p.id})

	p.mu.Lock()
	defer p.mu.Unlock()
	p.cwdInflight = false
	// The clock moves whether or not the answer was useful, so a machine that
	// is refusing is asked once a second rather than once a frame.
	p.cwdAt = time.Now()
	if err != nil {
		return
	}
	var res struct {
		Cwd string `json:"cwd"`
	}
	changed := false
	if json.Unmarshal(raw, &res) == nil && res.Cwd != "" && res.Cwd != p.cwd {
		p.cwd, changed = res.Cwd, true
	}
	notify := p.onCwdChange
	if !changed || notify == nil {
		return
	}
	// Outside the lock, and on this goroutine rather than a new one: the push
	// writes to client sockets, and holding a pane's lock across that would
	// put a slow client in front of the next read of where the pane is.
	go notify()
}

// PublishLiveFacts pushes the session's state to its clients because something
// the emulators or the processes know has changed, rather than because the
// session itself was altered.
//
// A remote pane's directory is the case it exists for. Nothing about the
// session changes when a shell on another machine runs cd: the window set, the
// layout and the names are all as they were, so no mutation happens and no
// push follows. But the directory is on the snapshot clients are given, and
// without a push they would be told the first answer and never a later one.
//
// A pane on this machine has no such problem, which is why this was not needed
// before: its shell announces over OSC 7, and that announcement reaches every
// client through its own emulator rather than through the session's state.
//
// It goes through mutateState with an empty change so it takes the version
// with it. The push is skipped for a version already sent, so a bump is what
// makes it a push at all, and a version is the honest record anyway: what
// clients hold afterwards is not what they held before.
func (s *Session) PublishLiveFacts() {
	// The cached read goes first. liveCwds holds its answer for a second so
	// the render path does not ask the operating system per frame, and the
	// push that created the pane had already cached the answer from before the
	// far machine replied: an empty one. Publishing without clearing it sent
	// that same empty answer again, which is the whole reason this was still
	// broken after the push itself was fixed.
	s.forgetCwdCache()
	_ = s.mutateState(func(*SessionState) error { return nil })
}

// remoteForeground is what the far machine says this pane is running, for a
// pane whose process is on another machine. The last result says whether this
// is such a pane at all, which is what tells "nothing is running there" apart
// from "this question does not apply here".
func (p *PTY) remoteForeground() (foregroundInfo, bool, bool) {
	rp, ok := p.pty.(*remotePane)
	if !ok {
		return foregroundInfo{}, false, false
	}
	info, running := rp.Foreground()
	return info, running, true
}

// refreshRemoteCwdOnOutput asks a pane on another machine where it is, if it
// has been long enough since the last answer.
//
// Reading the cached value is what starts the ask, so this is a map read and a
// clock comparison for a pane of this daemon's own, and at most one call a
// second for one elsewhere. The answer landing is what publishes it; see
// remotePane.Cwd and Session.PublishLiveFacts.
func (p *PTY) refreshRemoteCwdOnOutput() {
	if rp, ok := p.pty.(*remotePane); ok {
		rp.Cwd()
	}
}
