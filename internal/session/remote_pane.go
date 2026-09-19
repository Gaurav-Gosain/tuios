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
	return openRemotePane(ctx, fed, host, spec)
}
