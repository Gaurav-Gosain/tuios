package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	id, br, err := openPaneOn(stream, spec)
	if err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("tuios on %s could not start a pane: %w", host, err)
	}
	return &remotePane{host: host, id: id, fed: fed, stream: stream, br: br}, nil
}

// openPaneOn sends open-pane on a fresh connection to the far daemon and reads
// its reply. On success the connection is the pty from here on, and the reader
// that is returned is the only one that may read it.
func openPaneOn(stream io.ReadWriteCloser, spec hostedPaneSpec) (string, *bufio.Reader, error) {
	params, err := json.Marshal(spec)
	if err != nil {
		return "", nil, err
	}
	req, err := json.Marshal(verbRequest{
		ID:     json.RawMessage(`1`),
		Verb:   "open-pane",
		Params: params,
	})
	if err != nil {
		return "", nil, err
	}
	if _, err := stream.Write(append(req, '\n')); err != nil {
		return "", nil, fmt.Errorf("cannot ask for a pane: %w", err)
	}

	br := bufio.NewReader(stream)
	line, err := readLimitedLine(br, maxRemotePaneReply)
	if err != nil {
		return "", nil, fmt.Errorf("no answer to the pane request: %w", err)
	}
	var resp struct {
		Result *struct {
			Pane string `json:"pane"`
		} `json:"result"`
		Error *verbError `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return "", nil, fmt.Errorf("the reply cannot be read by this build: %w", err)
	}
	if resp.Error != nil {
		if resp.Error.Code == ErrVerbUnknownVerb {
			return "", nil, fmt.Errorf("that machine's tuios is too old to host a pane. Update it, then restart its daemon with 'tuios kill-server'")
		}
		return "", nil, resp.Error
	}
	if resp.Result == nil || resp.Result.Pane == "" {
		return "", nil, fmt.Errorf("the reply named no pane")
	}
	return resp.Result.Pane, br, nil
}

// Read returns the process's output. It ends when the far side closes the
// stream, which is what the process exiting and the link dropping both look
// like from here, and PTY treats that end the way it treats a local shell
// exiting.
func (p *remotePane) Read(b []byte) (int, error) { return p.br.Read(b) }

// Write sends input to the process.
func (p *remotePane) Write(b []byte) (int, error) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	return p.stream.Write(b)
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
func (p *remotePane) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	return p.stream.Close()
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

// openRemotePaneFor starts this session's window on another machine.
//
// The terminal type and the colour support travel with the request, because
// the pane is drawn by this session's emulator and the program at the far end
// has to be told what it is really talking to. The shell does not travel: see
// below.
func (s *Session) openRemotePaneFor(host string, width, height int, cwd string, command []string) (paneIO, error) {
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
	return p, nil
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
