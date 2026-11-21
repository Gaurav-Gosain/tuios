package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const remotePaneOpenBudget = 20 * time.Second

const remotePaneResizeBudget = 10 * time.Second

const maxRemotePaneReply = 64 * 1024

type paneFederation interface {
	OpenConnection(ctx context.Context, host string) (io.ReadWriteCloser, error)
	Call(ctx context.Context, host, verb string, params any) (json.RawMessage, error)
}

type remotePane struct {
	host string
	id   string
	fed  paneFederation

	stream io.ReadWriteCloser
	br     *bufio.Reader

	mu          sync.Mutex
	closed      bool
	cwd         string
	cwdAt       time.Time
	cwdInflight bool
	onCwdChange func()
	fg          foregroundInfo
	fgRunning   bool
	fgAt        time.Time
	fgInflight  bool

	callsToken string
	done       chan struct{}

	resumeToken    string
	grace          time.Duration
	received       int64
	pendingErr     error
	ended          string
	link           string
	linkUntil      time.Time
	onLinkChange   func()
	width, height  int
	xpixel, ypixel int
	pixels         bool
	kitty          *kittyLinkNear
}

const remotePaneLinkReconnecting = "reconnecting"

const remotePaneLinkEnded = "ended"

type remotePaneEndedError struct {
	host, why string
}

func (e *remotePaneEndedError) Error() string {
	return "the pane on " + e.host + " ended: " + e.why
}

const (
	remoteEndedForgotten = "its daemon there no longer has it: it restarted, or the pane's process ended there"
	remoteEndedGrace     = "the link stayed down past the time the machine kept the pane for"
)

func (p *remotePane) linkState() (string, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.link, p.linkUntil
}

func (p *remotePane) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func openRemotePane(ctx context.Context, fed paneFederation, host string, spec hostedPaneSpec) (*remotePane, error) {
	if fed == nil {
		return nil, errfmt.Errorf("this daemon has no links, so it cannot put a pane on %s", host)
	}
	stream, err := fed.OpenConnection(ctx, host)
	if err != nil {
		return nil, err
	}

	opened, br, err := openPaneReply(stream, spec)
	if err != nil {
		_ = stream.Close()
		return nil, errfmt.Errorf("tuios on %s could not start a pane: %w", host, err)
	}
	p := &remotePane{
		host: host, id: opened.Pane, fed: fed, stream: stream, br: br,
		callsToken: opened.CallsToken, done: make(chan struct{}),
		width: spec.Width, height: spec.Height, pixels: opened.Pixels,
	}
	if opened.ResumeToken != "" && opened.Grace > 0 {
		p.resumeToken = opened.ResumeToken
		p.grace = time.Duration(opened.Grace) * time.Second
	}
	if token := opened.KittyMediaToken; token != "" {
		p.kitty = &kittyLinkNear{fetch: func(id string) (io.ReadCloser, int64, error) {
			return p.fetchKittyMedium(token, id)
		}}
	}
	return p, nil
}

type paneOpened struct {
	Pane            string `json:"pane"`
	CallsToken      string `json:"calls_token"`
	ResumeToken     string `json:"resume_token"`
	Grace           int64  `json:"grace"`
	Resumed         bool   `json:"resumed"`
	Gap             bool   `json:"gap"`
	KittyMediaToken string `json:"kitty_media_token"`
	Pixels          bool   `json:"pixels"`
}

func openPaneReply(stream io.ReadWriteCloser, spec hostedPaneSpec) (paneOpened, *bufio.Reader, error) {
	br := bufio.NewReader(stream)
	resp, err := sendOpenPane(stream, br, spec)
	if err != nil {
		return paneOpened{}, nil, err
	}
	if resp.Result != nil {
		if !spec.Resumable && spec.Resume == nil {
			resp.Result.ResumeToken, resp.Result.Grace = "", 0
		}
		if !spec.KittyMedia {
			resp.Result.KittyMediaToken = ""
		}
	}
	if resp.Error != nil {
		if resp.Error.Code == ErrVerbUnknownVerb {
			return paneOpened{}, nil, errfmt.Errorf("that machine's tuios is too old to host a pane. Update it, then restart its daemon with 'tuios kill-server'")
		}
		return paneOpened{}, nil, resp.Error
	}
	if resp.Result == nil || resp.Result.Pane == "" {
		return paneOpened{}, nil, errfmt.Errorf("the reply named no pane")
	}
	return *resp.Result, br, nil
}

type openPaneResponse struct {
	Result *paneOpened `json:"result"`
	Error  *verbError  `json:"error"`
}

func sendOpenPane(stream io.Writer, br *bufio.Reader, spec hostedPaneSpec) (openPaneResponse, error) {
	params, err := jsonx.Marshal(spec)
	if err != nil {
		return openPaneResponse{}, err
	}
	req, err := jsonx.Marshal(verbRequest{
		ID:     json.RawMessage(`1`),
		Verb:   "open-pane",
		Params: params,
	})
	if err != nil {
		return openPaneResponse{}, err
	}
	if _, err := stream.Write(append(req, '\n')); err != nil {
		return openPaneResponse{}, errfmt.Errorf("cannot ask for a pane: %w", err)
	}
	line, err := readLimitedLine(br, maxRemotePaneReply)
	if err != nil {
		return openPaneResponse{}, errfmt.Errorf("no answer to the pane request: %w", err)
	}
	var resp openPaneResponse
	if err := jsonx.Unmarshal(line, &resp); err != nil {
		return openPaneResponse{}, errfmt.Errorf("the reply cannot be read by this build: %w", err)
	}
	return resp, nil
}

func (p *remotePane) Read(b []byte) (int, error) {
	if p.kitty != nil {
		return p.kitty.read(b, p.readRaw)
	}
	return p.readRaw(b)
}

func (p *remotePane) readRaw(b []byte) (int, error) {
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
		if rerr := p.reconnect(err); rerr != nil {
			if p.isClosed() {
				return 0, err
			}
			return 0, io.EOF
		}
	}
}

var errPaneReconnecting = errors.New("the link to this pane's machine is down and tuios is reconnecting")

func (p *remotePane) Write(b []byte) (int, error) {
	p.mu.Lock()
	closed, stream, link, ended := p.closed, p.stream, p.link, p.ended
	p.mu.Unlock()
	if ended != "" {
		return 0, &remotePaneEndedError{host: p.host, why: ended}
	}
	if closed {
		return 0, io.ErrClosedPipe
	}
	if link != "" {
		return 0, errPaneReconnecting
	}
	return stream.Write(b)
}

func (p *remotePane) Resize(width, height int) error {
	return p.SetWinsize(width, height, 0, 0)
}

func (p *remotePane) SetWinsize(width, height, xpixel, ypixel int) error {
	p.mu.Lock()
	closed, ended := p.closed, p.ended
	p.width, p.height = width, height
	p.xpixel, p.ypixel = xpixel, ypixel
	p.mu.Unlock()
	if ended != "" {
		return &remotePaneEndedError{host: p.host, why: ended}
	}
	if closed {
		return io.ErrClosedPipe
	}
	ctx, cancel := context.WithTimeout(context.Background(), remotePaneResizeBudget)
	defer cancel()
	_, err := p.fed.Call(ctx, p.host, "resize-pane", p.resizeParams(width, height, xpixel, ypixel))
	return err
}

func (p *remotePane) resizeParams(width, height, xpixel, ypixel int) map[string]any {
	params := map[string]any{"pane": p.id, "width": width, "height": height}
	if p.pixels && xpixel > 0 && ypixel > 0 {
		params["xpixel"], params["ypixel"] = xpixel, ypixel
	}
	return params
}

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

const (
	remotePaneRetryMin = 250 * time.Millisecond
	remotePaneRetryMax = 5 * time.Second
)

func (p *remotePane) reconnect(cause error) error {
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
		why := remoteEndedForgotten
		if stream != nil {
			p.mu.Lock()
			old := p.stream
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
			if attempt == 0 && errors.Is(cause, io.EOF) {
				p.setLink("", time.Time{})
				LogBasic("Pane %s on %s ended: its process exited there", shortID(p.id), p.host)
				return errPaneGone
			}
			p.end(why)
			return errPaneGone
		}
		select {
		case <-p.done:
			return io.ErrClosedPipe
		case <-time.After(wait):
		}
		wait = min(wait*2, remotePaneRetryMax)
	}
	p.end(remoteEndedGrace)
	return errPaneGone
}

func (p *remotePane) end(why string) {
	p.mu.Lock()
	p.ended = why
	p.mu.Unlock()
	LogBasic("Pane %s on %s could not be reattached, so it is closed: %s", shortID(p.id), p.host, why)
	p.setLink(remotePaneLinkEnded, time.Time{})
}

func (p *remotePane) endedWhy() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ended
}

var errPaneGone = errors.New("the pane's process on the other machine is gone")

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

func (p *remotePane) afterReattach(gap bool, width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	p.mu.Lock()
	xpixel, ypixel := p.xpixel, p.ypixel
	p.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), remotePaneResizeBudget)
		defer cancel()
		if gap && height > 1 {
			_, _ = p.fed.Call(ctx, p.host, "resize-pane", p.resizeParams(width, height-1, xpixel, ypixel*(height-1)/height))
		}
		_, _ = p.fed.Call(ctx, p.host, "resize-pane", p.resizeParams(width, height, xpixel, ypixel))
	}()
}

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

func (s *Session) openRemotePaneFor(windowID, host string, width, height int, cwd string, command []string) (paneIO, error) {
	fed := s.fed
	if fed == nil {
		return nil, errfmt.Errorf("this daemon has no links, so a window cannot be put on %s. Add it to the [hosts] table in the config", host)
	}
	spec := hostedPaneSpec{
		Width:   width,
		Height:  height,
		Cwd:     cwd,
		Command: command,
		Session: s.Name(),
	}
	if s.onRemotePane != nil {
		spec.Window = windowID
	}
	spec.Resumable = true
	spec.KittyMedia = true
	if s.config != nil {
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

const remotePaneAgentTTL = 2 * time.Second

func (p *remotePane) refreshForeground() {
	ctx, cancel := context.WithTimeout(context.Background(), remotePaneResizeBudget)
	defer cancel()
	raw, err := p.fed.Call(ctx, p.host, "pane-agent", map[string]any{"pane": p.id})

	p.mu.Lock()
	defer p.mu.Unlock()
	p.fgInflight = false
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
	if jsonx.Unmarshal(raw, &res) != nil {
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

const remotePaneCwdTTL = time.Second

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

func (p *remotePane) askCwd() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.cwdInflight {
		return
	}
	p.cwdInflight = true
	go p.refreshCwd()
}

func (p *remotePane) refreshCwd() {
	ctx, cancel := context.WithTimeout(context.Background(), remotePaneResizeBudget)
	defer cancel()
	raw, err := p.fed.Call(ctx, p.host, "pane-cwd", map[string]any{"pane": p.id})

	p.mu.Lock()
	defer p.mu.Unlock()
	p.cwdInflight = false
	p.cwdAt = time.Now()
	if err != nil {
		return
	}
	var res struct {
		Cwd string `json:"cwd"`
	}
	changed := false
	if jsonx.Unmarshal(raw, &res) == nil && res.Cwd != "" && res.Cwd != p.cwd {
		p.cwd, changed = res.Cwd, true
	}
	notify := p.onCwdChange
	if !changed || notify == nil {
		return
	}
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
//
// The version is recorded with the ops that change no tree, so a client push
// built before it is not stale (see missedMutationLocked). The change is empty,
// so there is nothing in it a push could undo. Counted as a mutation, it made
// the stale merge take each window's workspace from the daemon, and a pane
// moved by a key while its shell reported a new folder went back to the
// workspace it left.
func (s *Session) PublishLiveFacts() {
	s.forgetCwdCache()
	_ = s.mutateState(func(state *SessionState) error {
		s.noteTreeOpLocked(state.Version+1, "", treeOpNoWorkspace)
		return nil
	})
}

func (p *PTY) remoteForeground() (foregroundInfo, bool, bool) {
	rp, ok := p.pty.(*remotePane)
	if !ok {
		return foregroundInfo{}, false, false
	}
	info, running := rp.Foreground()
	return info, running, true
}
