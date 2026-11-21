package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// RemoteCommandHandler is a callback for handling remote commands from the CLI.
// It receives the command payload and returns success/error.
type RemoteCommandHandler func(payload *RemoteCommandPayload) error

// StateSyncHandler and the handler types below are the multi-client callbacks.
type StateSyncHandler func(state *SessionState, triggerType, sourceID string)
type ClientJoinedHandler func(clientID string, clientCount int, width, height int)
type ClientLeftHandler func(clientID string, clientCount int)

// SessionResizeHandler is told the session's negotiated size and the chrome
// reserve every client lays its panes out around. The two arrive together
// because they are one answer: the panes' box is the size less the reserve.
type SessionResizeHandler func(width, height, clientCount int, reserve LayoutReserve)

// SessionEndedHandler is called when the daemon reports that the attached
// session was terminated. It runs on the read-loop goroutine, so the handler
// should only signal the UI (e.g. p.Send), never mutate model state directly.
type SessionEndedHandler func(sessionName, reason string)

// DisconnectHandler is called once when the read loop tears the connection down
// because of an unexpected disconnect (daemon crash, reset, or framing desync).
// It is not called for an app-initiated Close.
type DisconnectHandler func(err error)

// TUIClient is used by the TUIOS TUI to communicate with the daemon.
// It handles PTY I/O and state synchronization.
type TUIClient struct {
	// HandshakeTimeout, when set, bounds the dial and each read of the
	// handshake, instead of the 30 seconds an attach is given. A client that
	// dials a daemon that may still be starting sets it. See handshaking.
	HandshakeTimeout time.Duration
	handshaking      bool
	// AttachDeadline, when set, is the latest time the reply to the next
	// AttachSession may come, instead of 30 seconds from the request. A
	// client that waits for a new daemon sets it to the end of its wait: a
	// daemon that says hello and then stalls must not hold it past that.
	// AttachSession clears it.
	AttachDeadline time.Time
	goodbye        atomic.Pointer[Goodbye]

	// Served and AllowNested are sent with every attach. Set them before the
	// first one. See AttachPayload.
	Served      bool
	AllowNested bool
	// StartDir is sent with every attach as AttachPayload.Cwd: the start
	// directory of a session the attach creates. An attach to a session that
	// exists ignores it.
	StartDir string
	// ViewOnly marks a client whose input is dropped, such as a tuios-web
	// viewer started with --read-only. It is sent with every attach. See
	// AttachPayload.ViewOnly.
	ViewOnly bool
	// UpgradeDialog says this client shows a live upgrade's dialog and
	// answers it only with a key the person pressed. It is sent with every
	// attach. See AttachPayload.UpgradeDialog.
	UpgradeDialog bool
	// Kind is the hello's ClientKind: ClientKindSSH or ClientKindWeb for a
	// client a server runs for a remote viewer, "" for a terminal on this
	// machine. Set it before Connect.
	Kind string
	// Passive shows a daemon pane but cannot execute full renderer commands.
	// It accepts input without determining the shared session's size.
	Passive bool
	// DetachOthers is sent with the next AttachSession, as tuios attach -d
	// asks: the other clients of the session are detached. AttachSession
	// clears it, so a later attach on this client (a session switch, a host
	// reconnect) detaches nobody. See AttachPayload.DetachOthers.
	DetachOthers bool
	// Reconnect marks the attach as the client getting back a session it
	// lost, not the person attaching. See AttachPayload.Reconnect.
	Reconnect bool
	// SSHAuthSock is sent in the hello as HelloPayload.SSHAuthSock. Only a
	// client that runs where the person is sets it: tuios attach and tuios
	// new. A server that serves a remote viewer leaves it empty.
	SSHAuthSock string
	// Display is sent in the hello as HelloPayload.Display. Only tuios attach
	// and tuios new set it, from their own environment, and a hello through
	// a host never carries it. See display_follow.go.
	Display ClientDisplay
	// Term and ColorTerm are sent in the hello as HelloPayload.Term and
	// HelloPayload.ColorTerm, and a session this client makes gives them to
	// its panes. Only a client that runs where the person is sets them, with
	// UseCallerTerm: tuios attach and tuios new. A server that serves a remote
	// viewer leaves them empty, and its sessions get the daemon's fallback.
	Term, ColorTerm string

	nestProbe      string
	nestedRefusal  atomic.Pointer[string]
	upgradedTo     atomic.Pointer[string]
	upgradeBinary  atomic.Pointer[string]
	detachedReason atomic.Pointer[string]

	conn   net.Conn
	br     *bufio.Reader
	mu     sync.Mutex
	readMu sync.Mutex

	sessionID   string
	sessionName atomic.Pointer[string]
	humanNonce  atomic.Pointer[string]
	rig         atomic.Bool

	pushMu     sync.Mutex
	pushOrigin atomic.Pointer[string]
	pushSeq    atomic.Uint64
	appliedSeq atomic.Uint64

	availableSessions []SessionInfo

	cacheGen atomic.Uint64

	localSessions []string
	localGen      uint64

	refreshInFlight atomic.Bool

	ptyHandlers   map[string]func([]byte)
	ptyHandlersMu sync.RWMutex

	ptyClosedHandlers   map[string]func()
	ptyClosedHandlersMu sync.RWMutex
	ptyClosedEarly      []string

	ptyResizeHandlers   map[string]func(width, height int)
	ptyResizeHandlersMu sync.RWMutex

	remoteCommandHandler RemoteCommandHandler
	remoteCommandMu      sync.RWMutex
	remoteCommandsEarly  []*RemoteCommandPayload

	stateSyncHandler     StateSyncHandler
	clientJoinedHandler  ClientJoinedHandler
	clientLeftHandler    ClientLeftHandler
	sessionResizeHandler SessionResizeHandler

	pendingStateSync     *StateSyncPayload
	pendingSessionResize *SessionResizePayload

	ownReserve       LayoutReserve
	sessionReserve   LayoutReserve
	sessionLayoutGen uint64

	clientBuild            string
	daemonBuild            string
	hostFocus              atomic.Int32
	focusMu                sync.Mutex
	usedMu                 sync.Mutex
	usedAt                 time.Time
	usedSession            string
	attachGen              atomic.Uint64
	activityMu             sync.Mutex
	lastActivity           time.Time
	sizePolicy             atomic.Pointer[string]
	sizedBy                atomic.Pointer[sessionSizer]
	selfID                 atomic.Pointer[string]
	attachClients          atomic.Int64
	tmlLink                bool
	themeEvents            bool
	dirChangedHandler      func(dir string)
	viaHost                string
	disconnectHandler      DisconnectHandler
	sessionEndedHandler    SessionEndedHandler
	agentMailHandler       AgentMailHandler
	hostsChangedHandler    HostsChangedHandler
	sessionsChangedHandler func()
	paneMarkHandler        func(PaneMarkPayload)
	tmlSurfaceHandler      func(*TMLSurfacePayload)
	pluginAsksHandler      func(PluginApprovalsPayload)
	pluginAsksLast         *PluginApprovalsPayload
	promptHandler          func(PromptPayload)
	promptLast             *PromptPayload
	pluginUIHandler        func(PluginUIPayload)
	pluginUILast           *PluginUIPayload
	tmlBacklog             []*TMLSurfacePayload
	pasteRefusedHandler    PasteRefusedHandler
	pastes                 map[uint64]*pasteRecord
	pasteBarriers          map[uint64]*pasteRecord
	pastesMu               sync.Mutex
	inputHolds             map[string]*inputHold
	lostErr                atomic.Pointer[error]
	sessionEndedOnce       sync.Once
	pendingEnded           *[2]string
	disconnectOnce         sync.Once
	multiClientMu          sync.RWMutex

	pendingResponses   map[MessageType]*pendingReply
	pendingByID        map[uint64]*pendingReply
	pendingResponsesMu sync.Mutex

	nextReqID atomic.Uint64

	roundTripMu sync.Mutex

	readLoopRunning bool
	done            chan struct{}
	doneOnce        sync.Once
	switchMu        sync.Mutex
}

// NewTUIClient creates a new TUI client for daemon communication.
func NewTUIClient() *TUIClient {
	return &TUIClient{
		ptyHandlers:       make(map[string]func([]byte)),
		ptyClosedHandlers: make(map[string]func()),
		ptyResizeHandlers: make(map[string]func(int, int)),
		pendingResponses:  make(map[MessageType]*pendingReply),
		pendingByID:       make(map[uint64]*pendingReply),
		done:              make(chan struct{}),
	}
}

// ClientCapabilities holds terminal graphics capabilities detected from the
// client's terminal, as the hello tells them to the daemon.
//
// Whether the terminal honours kitty a=f frame edits is not here. The client
// decides damage patches itself from app.HostCapabilities.KittyAnimation, so
// the daemon has no use for it.
type ClientCapabilities struct {
	PixelWidth    int
	PixelHeight   int
	CellWidth     int
	CellHeight    int
	KittyGraphics bool
	SixelGraphics bool
	TerminalName  string
	// Term is the terminal's TERM, for the glyph set images are drawn
	// with on a terminal without graphics. Not sent to the daemon.
	Term string
	// KittyAnimation says the host terminal edits image frames. See
	// HelloPayload.KittyAnimation.
	KittyAnimation bool
	// SymbolImages says images are drawn as block glyphs on a terminal
	// without graphics. See HelloPayload.SymbolImages.
	SymbolImages bool
}

// UseCallerTerm sets Term and ColorTerm to this process's terminal, detected
// as the verb client's hello detects it.
func (c *TUIClient) UseCallerTerm() {
	c.Term, c.ColorTerm = helloTerm()
}

// Connect connects to the daemon and performs handshake.
func (c *TUIClient) Connect(version string, width, height int) error {
	return c.ConnectWithCapabilities(version, width, height, nil)
}

// ConnectWithCapabilities connects to the daemon and performs handshake with graphics capabilities.
func (c *TUIClient) ConnectWithCapabilities(version string, width, height int, caps *ClientCapabilities) error {
	socketPath, err := GetSocketPath()
	if err != nil {
		return errfmt.Errorf("failed to get socket path: %w", err)
	}

	dialWait := 5 * time.Second
	if c.HandshakeTimeout > 0 {
		dialWait = min(dialWait, c.HandshakeTimeout)
	}
	conn, err := net.DialTimeout("unix", socketPath, dialWait)
	if err != nil {
		return errfmt.Errorf("failed to connect to daemon: %w", err)
	}
	c.conn = conn
	c.br = nil

	c.handshaking = true
	err = c.handshake(version, width, height, caps)
	c.handshaking = false
	if err != nil {
		_ = conn.Close()
		return err
	}
	return nil
}

func (c *TUIClient) handshake(version string, width, height int, caps *ClientCapabilities) error {
	hello := &HelloPayload{
		Version: version,
		Width:   width,
		Height:  height,

		Protocol:    ProtocolVersion,
		Machine:     localHostLabel(),
		ClientKind:  c.Kind,
		Term:        c.Term,
		ColorTerm:   c.ColorTerm,
		ListingPush: true,
		PaneMarks:   true,
		Goodbye:     true,
	}
	hello.TMLSurfaces = true
	hello.PluginApprovals = c.viaHost == ""
	hello.Prompts = c.viaHost == ""

	if caps != nil {
		hello.PixelWidth = caps.PixelWidth
		hello.PixelHeight = caps.PixelHeight
		hello.CellWidth = caps.CellWidth
		hello.CellHeight = caps.CellHeight
		hello.KittyGraphics = caps.KittyGraphics
		hello.SixelGraphics = caps.SixelGraphics
		hello.TerminalName = caps.TerminalName
		hello.KittyAnimation = caps.KittyAnimation
		hello.SymbolImages = caps.SymbolImages
	}

	if c.viaHost == "" {
		hello.SSHAuthSock = c.SSHAuthSock
		hello.Display = c.Display
	}
	hello.LayoutTreeOps = true
	hello.ScratchWorkspaces = true
	hello.HoldOptions = true
	hello.WindowSize = true
	hello.ViewChrome = true

	msg, err := NewMessage(MsgHello, hello)
	if err != nil {
		return err
	}

	if err := c.send(msg); err != nil {
		return err
	}

	resp, err := c.recv()
	if err != nil {
		return err
	}

	if resp.Type == MsgError {
		var errPayload ErrorPayload
		_ = resp.ParsePayload(&errPayload)
		if errPayload.Code == ErrCodeBusy {
			return ErrTooManyConnections
		}
		return &helloRefused{msg: errPayload.Message}
	}
	if resp.Type != MsgWelcome {
		return numberingMismatch(version, resp.Type)
	}

	var welcome WelcomePayload
	if err := resp.ParsePayload(&welcome); err != nil {
		return errfmt.Errorf("failed to parse welcome: %w", err)
	}

	if protocolMismatch(welcome.Protocol) {
		return daemonProtocolMismatch(version, &welcome)
	}

	c.noteDaemonBuild(version, welcome.Version)
	c.tmlLink = welcome.TMLLink
	c.themeEvents = welcome.ThemeEvents

	infos := make([]SessionInfo, 0, len(welcome.SessionNames))
	for _, name := range welcome.SessionNames {
		infos = append(infos, SessionInfo{Name: name})
	}
	c.UpdateSessionCache(infos)

	return nil
}

func (c *TUIClient) noteDaemonBuild(clientVersion, daemonVersion string) {
	c.multiClientMu.Lock()
	c.clientBuild, c.daemonBuild = clientVersion, daemonVersion
	c.multiClientMu.Unlock()
}

// ClientVersion is the build this client announced at the handshake.
func (c *TUIClient) ClientVersion() string { return c.clientBuild }

// DaemonVersion is the build the daemon named at the handshake.
func (c *TUIClient) DaemonVersion() string {
	c.multiClientMu.RLock()
	defer c.multiClientMu.RUnlock()
	return c.daemonBuild
}

// BuildMismatch reports the daemon's build and this client's when they differ,
// and empty strings when they agree or when either is unknown.
func (c *TUIClient) BuildMismatch() (clientBuild, daemonBuild string) {
	c.multiClientMu.RLock()
	defer c.multiClientMu.RUnlock()
	if c.clientBuild == "" || c.daemonBuild == "" || c.clientBuild == c.daemonBuild {
		return "", ""
	}
	return c.clientBuild, c.daemonBuild
}

// AttachSession attaches to a session (creates if createNew is true).
// Returns the session state for restoration.
func (c *TUIClient) AttachSession(name string, createNew bool, width, height int) (*SessionState, error) {
	probe := c.nestProbe
	msg, err := NewMessage(MsgAttach, &AttachPayload{
		SessionName:   name,
		CreateNew:     createNew,
		Width:         width,
		Height:        height,
		Reserve:       c.OwnLayoutReserve(),
		Served:        c.Served,
		AllowNested:   c.AllowNested,
		NestProbe:     probe,
		ViewOnly:      c.viewOnly(),
		UpgradeDialog: c.UpgradeDialog,
		Passive:       c.Passive,
		Cwd:           c.StartDir,
		DetachOthers:  c.DetachOthers,
		Reconnect:     c.Reconnect,
	})
	if err != nil {
		return nil, err
	}
	c.DetachOthers = false

	deadline := c.AttachDeadline
	c.AttachDeadline = time.Time{}

	if err := c.send(msg); err != nil {
		return nil, err
	}

	var resp *Message
	if deadline.IsZero() {
		resp, err = c.recv()
	} else {
		resp, err = c.recvUntil(deadline)
	}
	if err != nil {
		return nil, err
	}

	switch resp.Type {
	case MsgAttached:
		return c.takeAttachReply(resp)

	case MsgError:
		var errPayload ErrorPayload
		_ = resp.ParsePayload(&errPayload)
		return nil, refusalOf(errPayload)

	default:
		return nil, errfmt.Errorf("unexpected response: %d", resp.Type)
	}
}

func (c *TUIClient) takeAttachReply(resp *Message) (*SessionState, error) {
	var payload AttachedPayload
	if err := resp.ParsePayload(&payload); err != nil {
		return nil, err
	}
	c.sessionID = payload.SessionID
	c.setSessionName(payload.SessionName)
	c.humanNonce.Store(&payload.HumanNonce)
	c.rig.Store(payload.Rig)
	if !c.readLoopRunning {
		c.attachGen.Store(attachCounter.Add(1))
	}
	c.startPushes()
	c.appliedSeq.Store(stateSeq(payload.State))
	if err := validateSessionState(payload.State); err != nil {
		return nil, errfmt.Errorf("attach: the session state the daemon sent was refused: %w", err)
	}
	c.NoteSession(payload.SessionName)
	c.multiClientMu.Lock()
	c.sessionLayoutGen = 0
	c.multiClientMu.Unlock()
	c.noteSessionLayout(payload.Generation, payload.Reserve)
	c.noteSizePolicy(payload.Policy)
	c.sizedBy.Store(&sessionSizer{machine: payload.SizedBy, client: payload.SizedByClient, kind: payload.SizedByKind})
	c.selfID.Store(&payload.ClientID)
	c.attachClients.Store(int64(payload.ClientCount))
	return payload.State, nil
}

type attachRefused struct {
	msg     string
	code    int
	session string
	unnamed bool
}

func refusalOf(p ErrorPayload) *attachRefused {
	return &attachRefused{msg: p.Message, code: p.Code, session: p.Session, unnamed: p.Unnamed}
}

func (e *attachRefused) Error() string { return "attach failed: " + e.msg }

type helloRefused struct{ msg string }

func (e *helloRefused) Error() string { return "the daemon refused this client: " + e.msg }

// ProtocolRefused reports a daemon this client cannot talk to: one that
// refused the handshake, or one whose welcome is outside the protocols this
// build speaks. Either is found before the client attaches, so nothing of that
// daemon was drawn, and asking again cannot help.
func ProtocolRefused(err error) bool {
	if _, ok := errors.AsType[*helloRefused](err); ok {
		return true
	}
	_, ok := errors.AsType[*ProtocolMismatchError](err)
	return ok
}

// AttachRefused reports an attach the daemon answered and declined, which a
// client that is trying to get a lost session back needs to tell apart from an
// attach that never reached anyone. A refusal means the session is not there,
// so asking again cannot help. Anything else is worth another attempt.
func AttachRefused(err error) bool {
	var refused *attachRefused
	return errors.As(err, &refused)
}

// Detach detaches from the current session.
func (c *TUIClient) Detach() error {
	msg, err := NewMessage(MsgDetach, nil)
	if err != nil {
		return err
	}
	return c.send(msg)
}

// SwitchRollback reports a session switch that failed after the detach had
// already been taken, which is the only failure a caller cannot simply ignore:
// at that instant the connection holds no session.
//
// State non-nil means the client is back on Prev and the caller must rebuild
// from that state, because detaching drops every PTY subscription: the panes it
// still holds are pictures of shells, not shells. State nil means the return
// failed too and the connection is attached to nothing.
type SwitchRollback struct {
	Target string
	Prev   string
	// State is the previous session as the daemon handed it back, when the
	// return attach succeeded.
	State *SessionState
	// Err is why the switch failed.
	Err error
	// Recovery is why the return failed, set only when State is nil.
	Recovery error
}

func (e *SwitchRollback) Error() string {
	if e.State != nil {
		return fmt.Sprintf("switch to %q failed: %v; still on %q", e.Target, e.Err, e.Prev)
	}
	return fmt.Sprintf("switch to %q failed: %v; could not return to %q either (%v). Reattach with 'tuios attach %s'",
		e.Target, e.Err, e.Prev, e.Recovery, e.Prev)
}

func (e *SwitchRollback) Unwrap() error { return e.Err }

// SwitchSession detaches from the current session and attaches to another.
// Safe to call while the read loop is running. Serialized via mutex.
//
// The detach cannot be taken back on its own: the daemon drops every
// subscription and every resume position with it. So an attach that fails after
// it does not simply return; it attaches back to where the client came from and
// says so in a *SwitchRollback, and the caller rebuilds from the state that
// comes with it. A switch the daemon refuses costs the user the switch, not the
// session.
func (c *TUIClient) SwitchSession(targetName string, width, height int) (*SessionState, error) {
	c.switchMu.Lock()
	defer c.switchMu.Unlock()

	prevName := c.SessionName()
	debugLog("[SWITCH] Starting session switch to %q", targetName)
	c.noteSizePolicy("")

	if err := c.detachForSwitch(); err != nil {
		if errors.Is(err, errDetachUnconfirmed) {
			return nil, &SwitchRollback{Target: targetName, Prev: prevName, Err: err,
				Recovery: errors.New("the daemon did not confirm the detach in time, so the client may hold no session")}
		}
		return nil, err
	}

	debugLog("[SWITCH] Attaching to session %q (%dx%d)", targetName, width, height)
	state, err := c.attachWhileReading(targetName, true, width, height)
	if err == nil {
		windowCount := 0
		if state != nil {
			windowCount = len(state.Windows)
		}
		debugLog("[SWITCH] Attached to %q (%d windows)", c.SessionName(), windowCount)
		return state, nil
	}

	rollback := &SwitchRollback{Target: targetName, Prev: prevName, Err: err}
	if prevName == "" {
		rollback.Recovery = errors.New("the client held no named session to return to")
		return nil, rollback
	}
	if _, ok := errors.AsType[*attachRefused](err); !ok {
		rollback.Recovery = errors.New("the daemon is not answering, so no return was attempted")
		return nil, rollback
	}
	prevState, rerr := c.attachWhileReading(prevName, false, width, height)
	if rerr != nil {
		rollback.Recovery = rerr
		debugLog("[SWITCH] Return to %q failed: %v", prevName, rerr)
		return nil, rollback
	}
	rollback.State = prevState
	debugLog("[SWITCH] Returned to %q", prevName)
	return nil, rollback
}

var errDetachUnconfirmed = errors.New("detach timeout")

func (c *TUIClient) detachForSwitch() error {
	detachMsg, err := NewMessage(MsgDetach, nil)
	if err != nil {
		return errfmt.Errorf("detach encode: %w", err)
	}

	c.roundTripMu.Lock()
	defer c.roundTripMu.Unlock()

	resp := make(chan *Message, 1)
	p := &pendingReply{ch: resp, accept: func(m *Message) bool {
		if m.Type != MsgError {
			return true
		}
		var e ErrorPayload
		return m.ParsePayload(&e) == nil && e.Code == ErrCodeNotAttached
	}}
	c.pendingResponsesMu.Lock()
	c.pendingResponses[MsgDetached] = p
	c.pendingResponses[MsgError] = p
	c.pendingResponsesMu.Unlock()
	defer func() {
		c.pendingResponsesMu.Lock()
		for _, t := range []MessageType{MsgDetached, MsgError} {
			if c.pendingResponses[t] == p {
				delete(c.pendingResponses, t)
			}
		}
		c.pendingResponsesMu.Unlock()
	}()

	if err := c.send(detachMsg); err != nil {
		return errfmt.Errorf("detach send: %w", err)
	}
	debugLog("[SWITCH] Detach sent, waiting for confirmation...")

	select {
	case m := <-resp:
		if m.Type == MsgError {
			debugLog("[SWITCH] Not attached to any session, nothing to detach")
		} else {
			debugLog("[SWITCH] Detach confirmed")
		}
		return nil
	case <-time.After(5 * time.Second):
		return errDetachUnconfirmed
	case <-c.done:
		return errfmt.Errorf("client closed")
	}
}

func (c *TUIClient) attachWhileReading(name string, createNew bool, width, height int) (*SessionState, error) {
	probe := c.nestProbe
	msg, err := NewMessage(MsgAttach, &AttachPayload{
		SessionName:   name,
		CreateNew:     createNew,
		Width:         width,
		Height:        height,
		Reserve:       c.OwnLayoutReserve(),
		Served:        c.Served,
		AllowNested:   c.AllowNested,
		NestProbe:     probe,
		ViewOnly:      c.viewOnly(),
		UpgradeDialog: c.UpgradeDialog,
		Passive:       c.Passive,
	})
	if err != nil {
		return nil, errfmt.Errorf("attach encode: %w", err)
	}

	resp, err := c.sendAndWaitResponse(msg, MsgAttached, MsgError)
	if err != nil {
		return nil, errfmt.Errorf("attach: %w", err)
	}

	switch resp.Type {
	case MsgAttached:
		return c.takeAttachReply(resp)

	case MsgError:
		var errPayload ErrorPayload
		_ = resp.ParsePayload(&errPayload)
		return nil, refusalOf(errPayload)

	default:
		return nil, errfmt.Errorf("unexpected response: %d", resp.Type)
	}
}

// CreatePTY creates a new PTY in the session. windowID, if non-empty, is the
// client-side window UUID exported to the shell as TUIOS_WINDOW_ID.
func (c *TUIClient) CreatePTY(title, windowID string, width, height int) (string, error) {
	msg, err := NewMessage(MsgCreatePTY, &CreatePTYPayload{
		Title:    title,
		Width:    width,
		Height:   height,
		WindowID: windowID,
	})
	if err != nil {
		return "", err
	}

	resp, err := c.sendAndWaitResponse(msg, MsgPTYCreated, MsgError)
	if err != nil {
		return "", err
	}

	switch resp.Type {
	case MsgPTYCreated:
		var payload PTYCreatedPayload
		if err := resp.ParsePayload(&payload); err != nil {
			return "", err
		}
		return payload.ID, nil

	case MsgError:
		var errPayload ErrorPayload
		_ = resp.ParsePayload(&errPayload)
		return "", errfmt.Errorf("create PTY failed: %s", errPayload.Message)

	default:
		return "", errfmt.Errorf("unexpected response: %d", resp.Type)
	}
}

// ReadDir asks the daemon to list a directory on the machine it is running on.
//
// The rail's file section used to read the filesystem itself, which was the same
// machine as the pane only for as long as a pane could not be anywhere else.
// Going through the daemon makes the answer come from the disk the pane is
// actually on, and it is asked for the same way for a local session as for one
// attached on another host, so there is one path and not two that can disagree.
//
// windowID names the pane the listing is about, so the daemon can also say
// whether that pane announced a directory its shell is not in. Empty for a
// directory the user walked to by hand, which is theirs whatever a pane says.
func (c *TUIClient) ReadDir(windowID, dir string, max int, pinned bool) (*DirListingPayload, error) {
	msg, err := NewMessage(MsgReadDir, &ReadDirPayload{
		WindowID: windowID, Dir: dir, Max: max, Pinned: pinned,
	})
	if err != nil {
		return nil, err
	}

	resp, err := c.sendAndWaitResponse(msg, MsgDirListing, MsgError)
	if err != nil {
		return nil, err
	}

	switch resp.Type {
	case MsgDirListing:
		var payload DirListingPayload
		if err := resp.ParsePayload(&payload); err != nil {
			return nil, err
		}
		return &payload, nil

	case MsgError:
		var errPayload ErrorPayload
		_ = resp.ParsePayload(&errPayload)
		return nil, errfmt.Errorf("read dir failed: %s", errPayload.Message)

	default:
		return nil, errfmt.Errorf("unexpected response: %d", resp.Type)
	}
}

// ClosePTY closes a PTY.
func (c *TUIClient) ClosePTY(ptyID string) error {
	msg, err := NewMessage(MsgClosePTY, &ClosePTYPayload{PTYID: ptyID})
	if err != nil {
		return err
	}
	return c.send(msg)
}

// SubscribePTY subscribes to PTY output and registers a handler.
// The handler receives raw byte streams (MsgPTYOutput), each call in a slice
// of its own that the client never touches again, so the handler may keep it
// without copying (terminal.Window.WriteOutputAsync does). fromSeq is the stream
// position the caller's emulator has been restored to, so the daemon replays
// only what came after it; zero leaves the resume position to the daemon.
// fromSnapshot tells the daemon the emulator was just laid down from an
// authoritative snapshot ending at fromSeq, so a rolled catch-up must not
// clear it (issue #123).
func (c *TUIClient) SubscribePTY(ptyID string, fromSeq int64, fromSnapshot bool, handler func([]byte)) error {
	c.ptyHandlersMu.Lock()
	c.ptyHandlers[ptyID] = handler
	c.ptyHandlersMu.Unlock()

	msg, err := NewMessage(MsgSubscribePTY, &SubscribePTYPayload{PTYID: ptyID, FromSeq: fromSeq, FromSnapshot: fromSnapshot})
	if err != nil {
		return err
	}
	return c.send(msg)
}

// UnsubscribePTY removes the PTY output handler and tells the daemon to stop streaming.
func (c *TUIClient) UnsubscribePTY(ptyID string) {
	c.ptyHandlersMu.Lock()
	delete(c.ptyHandlers, ptyID)
	c.ptyHandlersMu.Unlock()

	c.ptyResizeHandlersMu.Lock()
	delete(c.ptyResizeHandlers, ptyID)
	c.ptyResizeHandlersMu.Unlock()

	msg, err := NewMessage(MsgUnsubscribePTY, &UnsubscribePTYPayload{PTYID: ptyID})
	if err != nil {
		return
	}
	_ = c.send(msg)
}

// OnPTYResized registers a handler for the size the daemon's emulator took.
// It is called on the read loop, in the same order as the output handler, so a
// caller that applies both in the order it is told them lays out every byte at
// the width the daemon laid it out at.
func (c *TUIClient) OnPTYResized(ptyID string, handler func(width, height int)) {
	c.ptyResizeHandlersMu.Lock()
	c.ptyResizeHandlers[ptyID] = handler
	c.ptyResizeHandlersMu.Unlock()
}

// OnPTYClosed registers a handler to be called when the PTY process exits.
//
// When the PTY closed before this call, handler runs at once, on the caller's
// goroutine. See ptyClosedEarly.
func (c *TUIClient) OnPTYClosed(ptyID string, handler func()) {
	c.ptyClosedHandlersMu.Lock()
	if i := slices.Index(c.ptyClosedEarly, ptyID); i >= 0 {
		c.ptyClosedEarly = slices.Delete(c.ptyClosedEarly, i, i+1)
		c.ptyClosedHandlersMu.Unlock()
		if handler != nil {
			handler()
		}
		return
	}
	c.ptyClosedHandlers[ptyID] = handler
	c.ptyClosedHandlersMu.Unlock()
}

const ptyClosedEarlyMax = 32

const remoteCommandsEarlyMax = 32

// OnRemoteCommand registers a handler for remote commands from the CLI.
// The handler should execute the command and return an error if it fails.
// The commands that arrived before any handler was registered are handed to
// it here, in the order they came.
func (c *TUIClient) OnRemoteCommand(handler RemoteCommandHandler) {
	c.remoteCommandMu.Lock()
	c.remoteCommandHandler = handler
	var early []*RemoteCommandPayload
	if handler != nil {
		early = c.remoteCommandsEarly
		c.remoteCommandsEarly = nil
	}
	c.remoteCommandMu.Unlock()
	for _, payload := range early {
		if err := handler(payload); err != nil {
			_ = c.SendCommandResult(payload.RequestID, false, err.Error())
		}
	}
}

// OnStateSync registers a handler for state sync messages from other clients.
// A sync that arrived before anything was registered is delivered to the new
// handler here, on the registering goroutine; without that, a peer's push
// racing this client's attach was lost until the peer's next change.
func (c *TUIClient) OnStateSync(handler StateSyncHandler) {
	c.multiClientMu.Lock()
	c.stateSyncHandler = handler
	pending := c.pendingStateSync
	c.pendingStateSync = nil
	c.multiClientMu.Unlock()
	if handler != nil && pending != nil {
		handler(pending.State, pending.TriggerType, pending.SourceID)
	}
}

// OnClientJoined registers a handler for when another client joins the session.
func (c *TUIClient) OnClientJoined(handler ClientJoinedHandler) {
	c.multiClientMu.Lock()
	c.clientJoinedHandler = handler
	c.multiClientMu.Unlock()
}

// OnClientLeft registers a handler for when another client leaves the session.
func (c *TUIClient) OnClientLeft(handler ClientLeftHandler) {
	c.multiClientMu.Lock()
	c.clientLeftHandler = handler
	c.multiClientMu.Unlock()
}

// OwnLayoutReserve returns the chrome this client last told the daemon it draws
// around the panes.
func (c *TUIClient) OwnLayoutReserve() LayoutReserve {
	c.multiClientMu.RLock()
	defer c.multiClientMu.RUnlock()
	return c.ownReserve
}

// SetOwnLayoutReserve records this client's chrome and reports whether it moved.
// It only records: announcing it is NotifyTerminalSize's job, so a caller sends
// one message for the whole of "my viewport and my chrome" rather than two that
// can disagree for a frame.
func (c *TUIClient) SetOwnLayoutReserve(r LayoutReserve) bool {
	c.multiClientMu.Lock()
	defer c.multiClientMu.Unlock()
	if c.ownReserve == r {
		return false
	}
	c.ownReserve = r
	return true
}

// SessionLayoutReserve returns the reserve the daemon settled on for every
// client of this session.
func (c *TUIClient) SessionLayoutReserve() LayoutReserve {
	c.multiClientMu.RLock()
	defer c.multiClientMu.RUnlock()
	return c.sessionReserve
}

func (c *TUIClient) noteSessionLayout(generation uint64, r LayoutReserve) bool {
	c.multiClientMu.Lock()
	defer c.multiClientMu.Unlock()
	if generation != 0 {
		if generation <= c.sessionLayoutGen {
			return false
		}
		c.sessionLayoutGen = generation
	}
	c.sessionReserve = r
	return true
}

// OnSessionResize registers a handler for session resize messages.
// This is called when the effective session size changes (min of all clients).
// A resize that arrived before anything was registered is delivered to the new
// handler here, the way OnStateSync replays a missed sync: the message carries
// the box the panes go in, and a client that misses it lays panes out in a box
// the session has moved on from.
func (c *TUIClient) OnSessionResize(handler SessionResizeHandler) {
	c.multiClientMu.Lock()
	c.sessionResizeHandler = handler
	pending := c.pendingSessionResize
	c.pendingSessionResize = nil
	c.multiClientMu.Unlock()
	if handler != nil && pending != nil {
		handler(pending.Width, pending.Height, pending.ClientCount, pending.Reserve)
	}
}

// OnSessionEnded registers a handler invoked when the daemon reports that the
// attached session was terminated. It fires at most once per client.
func (c *TUIClient) OnSessionEnded(handler SessionEndedHandler) {
	c.multiClientMu.Lock()
	c.sessionEndedHandler = handler
	pending := c.pendingEnded
	if handler != nil {
		c.pendingEnded = nil
	}
	c.multiClientMu.Unlock()
	if handler != nil && pending != nil {
		c.sessionEndedOnce.Do(func() { handler(pending[0], pending[1]) })
	}
}

// AgentMailHandler takes one MsgAgentMail push: a message from the session's
// agent ring as the daemon stores it, or a read receipt for messages an inbox
// read just marked. It runs on the read-loop goroutine, so it only queues.
type AgentMailHandler func(payload AgentMailPayload)

// OnAgentMail registers the handler for MsgAgentMail, the daemon's push of
// every message an agent leaves in the attached session's ring.
func (c *TUIClient) OnAgentMail(handler AgentMailHandler) {
	c.multiClientMu.Lock()
	c.agentMailHandler = handler
	c.multiClientMu.Unlock()
}

// HostsChangedHandler takes one MsgHostsChanged push: the daemon's [hosts]
// table changed under this client. It runs on the read-loop goroutine, so it
// only queues.
type HostsChangedHandler func(payload HostsChangedPayload)

// OnHostsChanged registers the handler for MsgHostsChanged, the daemon's push
// when a host is added, removed or redialed while this client is attached.
func (c *TUIClient) OnHostsChanged(handler HostsChangedHandler) {
	c.multiClientMu.Lock()
	c.hostsChangedHandler = handler
	c.multiClientMu.Unlock()
}

// OnSessionsChanged registers the handler for MsgSessionsChanged, the
// daemon's push when the session listing changed. It runs on the read-loop
// goroutine, so it only queues.
func (c *TUIClient) OnSessionsChanged(handler func()) {
	c.multiClientMu.Lock()
	c.sessionsChangedHandler = handler
	c.multiClientMu.Unlock()
}

// OnPaneMark registers the handler for MsgPaneMark, the daemon's push when a
// pane this client does not stream rang the bell or printed. It runs on the
// read-loop goroutine, so it only queues. See pane_marks.go.
func (c *TUIClient) OnPaneMark(handler func(PaneMarkPayload)) {
	c.multiClientMu.Lock()
	c.paneMarkHandler = handler
	c.multiClientMu.Unlock()
}

// OnPluginUI registers the handler for MsgPluginUI. Cells that came before
// the handler are handed to it now. It runs on the read-loop goroutine, so it
// only queues.
func (c *TUIClient) OnPluginUI(handler func(PluginUIPayload)) {
	c.multiClientMu.Lock()
	c.pluginUIHandler = handler
	last := c.pluginUILast
	c.pluginUILast = nil
	c.multiClientMu.Unlock()
	if handler != nil && last != nil {
		handler(*last)
	}
}

// OnPluginApprovals registers the handler for MsgPluginApprovals, the
// daemon's list of native plugins that wait for the person. A push that
// came before the handler is handed to it now. It runs on the read-loop
// goroutine, so it only queues. See plugin_approval.go.
func (c *TUIClient) OnPluginApprovals(handler func(PluginApprovalsPayload)) {
	c.multiClientMu.Lock()
	c.pluginAsksHandler = handler
	last := c.pluginAsksLast
	c.pluginAsksLast = nil
	c.multiClientMu.Unlock()
	if handler != nil && last != nil {
		handler(*last)
	}
}

// OnPrompt registers the handler for MsgPrompt, the daemon's open prompt
// or the mark that it closed. A push that came before the handler is handed
// to it now. It runs on the read-loop goroutine, so it only queues. See
// prompt.go.
func (c *TUIClient) OnPrompt(handler func(PromptPayload)) {
	c.multiClientMu.Lock()
	c.promptHandler = handler
	last := c.promptLast
	c.promptLast = nil
	c.multiClientMu.Unlock()
	if handler != nil && last != nil {
		handler(*last)
	}
}

// DrawsTML reports whether this client draws TML surfaces: a client of this
// machine's daemon, or one through a host whose daemon takes what the person
// does on a surface over the link. See the hello.
func (c *TUIClient) DrawsTML() bool { return c.viaHost == "" || c.tmlLink }

// TMLHostTooOld reports whether this client is attached through a host whose
// daemon does not serve TML surfaces over a link. Its TML windows say so in
// place of the surface.
func (c *TUIClient) TMLHostTooOld() bool { return c.viaHost != "" && !c.tmlLink }

// OnTMLSurface registers the handler for MsgTMLSurface, the daemon's push of
// a TML surface when it opens, changes or closes, and of every open one when
// this client attaches. It runs on the read-loop goroutine, so it only
// queues. See tml_surface.go.
func (c *TUIClient) OnTMLSurface(handler func(*TMLSurfacePayload)) {
	c.multiClientMu.Lock()
	c.tmlSurfaceHandler = handler
	backlog := c.tmlBacklog
	if handler != nil {
		c.tmlBacklog = nil
	}
	c.multiClientMu.Unlock()
	if handler != nil {
		for _, p := range backlog {
			handler(p)
		}
	}
}

// WatchDir asks the daemon to push MsgDirChanged when the names in dir change,
// for the folder listed for windowID. It replaces the last watch, and an empty
// dir ends it.
func (c *TUIClient) WatchDir(windowID, dir string) error {
	msg, err := NewMessage(MsgWatchDir, &WatchDirPayload{WindowID: windowID, Dir: dir})
	if err != nil {
		return err
	}
	return c.send(msg)
}

// OnDirChanged registers the handler for MsgDirChanged. It runs on the
// read-loop goroutine, so it only signals.
func (c *TUIClient) OnDirChanged(handler func(dir string)) {
	c.multiClientMu.Lock()
	c.dirChangedHandler = handler
	c.multiClientMu.Unlock()
}

// OnDisconnect registers a handler invoked when the daemon connection is torn
// down unexpectedly (crash, reset, or framing desync). It fires at most once and
// runs on the read-loop goroutine, so the handler should only signal the UI
// (e.g. p.Send), never mutate model state directly.
func (c *TUIClient) OnDisconnect(handler DisconnectHandler) {
	c.multiClientMu.Lock()
	c.disconnectHandler = handler
	c.multiClientMu.Unlock()
}

func (c *TUIClient) handleDisconnect(err error) {
	select {
	case <-c.done:
		return
	default:
	}
	if err != nil {
		c.lostErr.Store(&err)
	}
	_ = c.Close()
	c.disconnectOnce.Do(func() {
		c.multiClientMu.RLock()
		handler := c.disconnectHandler
		c.multiClientMu.RUnlock()
		if handler != nil {
			handler(err)
		}
	})
}

// SendCommandResult sends the result of a remote command execution back to the daemon.
func (c *TUIClient) SendCommandResult(requestID string, success bool, message string) error {
	return c.SendCommandResultWithData(requestID, success, message, nil)
}

// SendCommandResultWithData sends the result with optional structured data.
func (c *TUIClient) SendCommandResultWithData(requestID string, success bool, message string, data map[string]any) error {
	msg, err := NewMessage(MsgCommandResult, &CommandResultPayload{
		RequestID: requestID,
		Success:   success,
		Message:   message,
		Data:      data,
	})
	if err != nil {
		return err
	}
	if len(msg.Payload) > maxCommandResultBytes {
		tooLarge := &FrameTooLargeError{Type: MsgCommandResult, Size: uint32(len(msg.Payload)) + 2, Limit: uint32(maxCommandResultBytes) + 2}
		msg, err = NewMessage(MsgCommandResult, &CommandResultPayload{
			RequestID: requestID,
			Message:   "the result was not sent: " + tooLarge.Error(),
		})
		if err != nil {
			return err
		}
		if err := c.send(msg); err != nil {
			return err
		}
		return tooLarge
	}
	return c.send(msg)
}

// WritePTY sends input to a PTY. Input larger than one frame is not sent and
// is reported to OnPasteRefused. To a daemon that tags replies, a large input
// is sent as a paste: later input to the pane waits behind it, and a busy
// refusal is sent again once. See paste_retry.go.
func (c *TUIClient) WritePTY(ptyID string, data []byte) error {
	if len(data) > maxPasteBytes {
		c.pasteRefused(ptyID, PasteRefusedTooLarge)
		return ErrPasteTooLarge
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeInputLocked(ptyID, data)
}

// ResizePTY resizes a PTY.
func (c *TUIClient) ResizePTY(ptyID string, width, height int) error {
	msg, err := NewMessage(MsgResize, &ResizePTYPayload{
		PTYID:  ptyID,
		Width:  width,
		Height: height,
	})
	if err != nil {
		return err
	}
	return c.send(msg)
}

// ReportHostFocus tells the daemon whether this client's host terminal has
// focus. It does nothing on a daemon that did not offer MsgClientFocus, which
// would refuse the type. It may be called from any goroutine: whatever the
// order the calls run in, the last message sent carries the latest focus.
func (c *TUIClient) ReportHostFocus(focused bool) error {
	v := int32(2)
	if focused {
		v = 1
	}
	c.hostFocus.Store(v)
	c.focusMu.Lock()
	defer c.focusMu.Unlock()
	msg, err := NewMessage(MsgClientFocus, &ClientFocusPayload{Focused: c.hostFocus.Load() == 1})
	if err != nil {
		return err
	}
	return c.send(msg)
}

// NotifyTerminalSize notifies the daemon of this client's terminal size.
// This is used for multi-client size calculation (effective size = min of all clients).
// Called when the terminal is resized.
func (c *TUIClient) NotifyTerminalSize(width, height int) error {
	msg, err := NewMessage(MsgResize, &ResizePTYPayload{
		PTYID:   "",
		Width:   width,
		Height:  height,
		Reserve: c.OwnLayoutReserve(),
	})
	if err != nil {
		return err
	}
	return c.send(msg)
}

// SendIntent asks the daemon to perform a session mutation on this client's
// behalf. It is the keyboard's route to the same operations the CLI reaches over
// the verb protocol: commandType and args are the verb vocabulary, so a
// keystroke and a `tuios run-command` do not merely agree, they are the same
// call.
//
// It does not wait for a result. The mutation's effect arrives as a state push
// like any other daemon-side change, which is the only channel a client applies
// state from; a second, synchronous answer would be a second way to learn the
// same news. A send error means the socket is gone, which the read loop is
// already reporting as a disconnect.
func (c *TUIClient) SendIntent(commandType string, args ...string) error {
	return c.SendIntentIn("", commandType, args...)
}

// SendIntentIn is SendIntent with the directory a NewWindow starts in. The
// daemon spawns the shell there, so nothing has to be typed into it.
func (c *TUIClient) SendIntentIn(cwd, commandType string, args ...string) error {
	return c.SendNewWindowIntent(cwd, 0, "", commandType, args...)
}

// SendNewWindowIntent is SendIntentIn with the workspace a NewWindow goes on
// and the window it follows into ssh. A zero workspace is the session's
// current one. An empty sshFrom follows none. See
// ExecuteCommandPayload.SSHFrom.
func (c *TUIClient) SendNewWindowIntent(cwd string, workspace int, sshFrom, commandType string, args ...string) error {
	return c.SendNewWindowIntentSized(cwd, workspace, 0, 0, sshFrom, commandType, args...)
}

// SendNewWindowIntentSized is SendNewWindowIntent for a client that knows the
// size the pane will have: cols and rows are its content size, and the daemon
// starts the process at it. See ExecuteCommandPayload.Cols.
func (c *TUIClient) SendNewWindowIntentSized(cwd string, workspace, cols, rows int, sshFrom, commandType string, args ...string) error {
	return c.sendNewWindow(&ExecuteCommandPayload{
		CommandType: commandType,
		Args:        args,
		Cwd:         cwd,
		Workspace:   workspace,
		Cols:        cols,
		Rows:        rows,
		SSHFrom:     sshFrom,
	})
}

// SendRunIntent asks for a window that runs argv, named name. keepOpen keeps
// the window after argv exits. See ExecuteCommandPayload.KeepOpen. cwd,
// workspace, cols and rows are as SendNewWindowIntentSized's.
func (c *TUIClient) SendRunIntent(cwd string, workspace, cols, rows int, keepOpen bool, name string, argv ...string) error {
	return c.sendNewWindow(&ExecuteCommandPayload{
		CommandType: "NewWindow",
		Args:        append([]string{name}, argv...),
		Cwd:         cwd,
		Workspace:   workspace,
		Cols:        cols,
		Rows:        rows,
		KeepOpen:    keepOpen,
	})
}

// SendNewWindowFrom asks for a shell on workspace that starts in the
// directory of the window cwdFrom, or in the session's start directory
// when that window has none. Empty cwdFrom gives the start directory too.
// The window takes the focus only if the session still shows workspace.
// See ExecuteCommandPayload.CwdFrom and FocusIfShown.
//
// sshFrom, when set, names the window whose ssh the new one follows, as
// SendNewWindowIntent's does. cols and rows are the content size when the
// client knows it, as SendNewWindowIntentSized's are; zero keeps the fallback.
func (c *TUIClient) SendNewWindowFrom(workspace, tabKey, cols, rows int, cwdFrom, sshFrom string) error {
	return c.sendNewWindow(&ExecuteCommandPayload{
		CommandType:  "NewWindow",
		Workspace:    workspace,
		TabKey:       tabKey,
		Cols:         cols,
		Rows:         rows,
		CwdFrom:      cwdFrom,
		SSHFrom:      sshFrom,
		FocusIfShown: true,
	})
}

func (c *TUIClient) sendNewWindow(payload *ExecuteCommandPayload) error {
	payload.SessionName = c.SessionName()
	msg, err := NewMessage(MsgExecuteCommand, payload)
	if err != nil {
		return err
	}
	return c.send(msg)
}

// UpdateState sends a state update to the daemon. It stamps the push's name on
// state (PushOrigin and PushSeq), which the daemon reads and drops.
func (c *TUIClient) UpdateState(state *SessionState) error {
	c.pushMu.Lock()
	defer c.pushMu.Unlock()
	seq := c.pushSeq.Load() + 1
	if origin := c.pushOrigin.Load(); origin != nil {
		state.PushOrigin, state.PushSeq = *origin, seq
	}
	msg, err := NewMessage(MsgUpdateState, state)
	if err != nil {
		return err
	}
	if len(msg.Payload) > maxStateUpdateBytes {
		return &FrameTooLargeError{Type: MsgUpdateState, Size: uint32(len(msg.Payload)) + 2, Limit: uint32(maxStateUpdateBytes) + 2}
	}
	if err := c.send(msg); err != nil {
		return err
	}
	c.pushSeq.Store(seq)
	return nil
}

// SendLayoutTree sends one workspace's tree to the daemon as an op. leaves names
// the window each leaf number in tree stands for; nil tree says the workspace
// has none. base is zero for a change the user made, and otherwise the Version
// of the state the tree was worked out from (see LayoutTreePayload.BaseVersion).
//
// The op is numbered in the same sequence as the state pushes, so every state
// the daemon handed out before it landed reads as predating this client's own
// push and is dropped (see PredatesOwnPush). That is what keeps a drag smooth:
// the answers to the earlier steps of the drag arrive while later steps are in
// flight, and none of them may put the divider back.
func (c *TUIClient) SendLayoutTree(ws int, tree *SerializedBSPTree, leaves map[int]string, base int) error {
	c.pushMu.Lock()
	defer c.pushMu.Unlock()
	seq := c.pushSeq.Load() + 1
	p := &LayoutTreePayload{PushSeq: seq, Workspace: ws, Tree: tree, Leaves: leaves, BaseVersion: base}
	if origin := c.pushOrigin.Load(); origin != nil {
		p.PushOrigin = *origin
	}
	msg, err := NewMessage(MsgLayoutTree, p)
	if err != nil {
		return err
	}
	if len(msg.Payload) > maxStateUpdateBytes {
		return &FrameTooLargeError{Type: MsgLayoutTree, Size: uint32(len(msg.Payload)) + 2, Limit: uint32(maxStateUpdateBytes) + 2}
	}
	if err := c.send(msg); err != nil {
		return err
	}
	c.pushSeq.Store(seq)
	return nil
}

func (c *TUIClient) startPushes() {
	var raw [12]byte
	origin := ""
	if _, err := rand.Read(raw[:]); err == nil {
		origin = hex.EncodeToString(raw[:])
	}
	c.pushMu.Lock()
	defer c.pushMu.Unlock()
	c.pushOrigin.Store(&origin)
	c.pushSeq.Store(0)
}

var attachCounter atomic.Uint64

// AttachGeneration identifies this client's current attach. It is unique in
// the process, not a per-client count: a client that switched machines holds a
// new connection, and a message the left connection queued must not carry a
// stamp equal to the new one's. Zero means no stamp. SnapshotSeq numbers are the
// attached session's own and start over in another session or after a daemon
// restart, both of which take a new attach, so two snapshots are comparable by
// SnapshotSeq only when they arrived under the same generation.
//
// A handler reads it on the read loop to stamp what it hands on, and the model
// drops a stamp that is not current: it came from the session the client left.
// So the count moves on the read loop, at the moment the attach reply is read,
// and not later on the goroutine that waits for the reply. Counted there, the
// first messages of the new session, read before that goroutine woke, carried
// the old count and were dropped as the old session's.
func (c *TUIClient) AttachGeneration() uint64 {
	if c == nil {
		return 0
	}
	return c.attachGen.Load()
}

// AcceptState reports whether a session state that arrived from the daemon
// should be applied, and when it should, records it as the newest applied. A
// state is refused when it is older than one already applied (see
// SessionState.SnapshotSeq) or when it predates this client's own last push
// (see PredatesOwnPush). Either way the client already shows something newer.
// Call it where the state is applied, not where it is received: what matters
// is the order the client applies states and makes pushes in.
func (c *TUIClient) AcceptState(state *SessionState) bool {
	if state == nil {
		return false
	}
	if seq := state.SnapshotSeq; seq != 0 && seq < c.appliedSeq.Load() {
		return false
	}
	if c.PredatesOwnPush(state) {
		return false
	}
	if seq := state.SnapshotSeq; seq > c.appliedSeq.Load() {
		c.appliedSeq.Store(seq)
	}
	return true
}

func stateSeq(state *SessionState) uint64 {
	if state == nil {
		return 0
	}
	return state.SnapshotSeq
}

// PredatesOwnPush reports whether state was handed out by the daemon before it
// had merged this client's newest push.
//
// Such a state is older than what this client is showing, and applying it
// would put the client back to where it was before its own last change. The
// daemon has the push and has sent it to every other client; the one client it
// never sends a push back to is the one that made it, so nothing would ever put
// this client right. That is how a pane stayed zoomed on two clients and not
// on the third: the third unzoomed it while a peer's broadcast was on its way,
// and applied the broadcast after.
//
// It is safe to drop such a state because the push that is newer than it
// reaches the daemon after it: either that push is taken as sent, which is
// what this client is showing, or it is reconciled against something this
// client has not seen, and the reconciled state is sent back to this client
// with the push counted.
//
// A state with no push table is from a daemon that keeps none, and is taken as
// it always was.
func (c *TUIClient) PredatesOwnPush(state *SessionState) bool {
	if state == nil || state.PushSeen == nil {
		return false
	}
	origin := c.pushOrigin.Load()
	if origin == nil || *origin == "" {
		return false
	}
	return state.PushSeen[*origin] < c.pushSeq.Load()
}

// KillSession terminates the currently attached session.
// This should be called when the user wants to quit AND kill the session.
func (c *TUIClient) KillSession() error {
	if c.SessionName() == "" {
		return nil
	}
	return c.KillSessionByName(c.SessionName())
}

// KillSessionByName terminates a session by name (can be any session, not just
// current). It waits for the daemon's post-kill session list and refreshes the
// cached names the UI reads, so a killed session leaves the switcher and sidebar
// at once instead of lingering until the next unrelated refresh. Waiting for the
// real reply also surfaces a daemon rejection (an unknown name) as an error
// rather than the phantom success the old fire-and-forget send always reported.
func (c *TUIClient) KillSessionByName(name string) error {
	if name == "" {
		return errfmt.Errorf("session name cannot be empty")
	}
	msg, err := NewMessage(MsgKill, &KillPayload{
		SessionName: name,
	})
	if err != nil {
		return err
	}
	stamp := c.listingStamp()
	resp, err := c.sendAndWaitResponse(msg, MsgSessionList, MsgError)
	if err != nil {
		return err
	}
	if resp.Type == MsgError {
		var errPayload ErrorPayload
		_ = resp.ParsePayload(&errPayload)
		return errfmt.Errorf("kill session: %s", errPayload.Message)
	}
	var payload SessionListPayload
	if err := resp.ParsePayload(&payload); err != nil {
		return err
	}
	c.applySessionListing(payload.Sessions, stamp)
	return nil
}

// GetTerminalState retrieves the terminal state for a PTY. maxScrollback bounds
// the scrollback rows the daemon includes: negative for none, zero for the
// default, or a count. This is used when attaching to restore terminal content.
//
// have is how many scrollback rows this client's emulator already holds for the
// pane. The daemon sends only the rows past it, which is all this client can
// use: an emulator that survived keeps its own history and merges just what
// scrolled off while it was away. Pass zero for a fresh emulator.
func (c *TUIClient) GetTerminalState(ptyID string, maxScrollback, have int) (*TerminalState, error) {
	msg, err := NewMessage(MsgGetTerminalState, &GetTerminalStatePayload{
		PTYID:              ptyID,
		IncludeScrollback:  maxScrollback >= 0,
		MaxScrollbackLines: max(maxScrollback, 0),
		HaveScrollback:     max(have, 0),
		Packed:             true,
	})
	if err != nil {
		return nil, err
	}

	var checked *TerminalStatePayload
	forThisPane := func(m *Message) bool {
		if m.Type != MsgTerminalState {
			return true
		}
		var p TerminalStatePayload
		if err := m.ParsePayload(&p); err != nil {
			return true
		}
		if p.PTYID != ptyID {
			debugLog("[CLIENT] dropped a terminal state for %s while waiting for %s", shortID(p.PTYID), shortID(ptyID))
			return false
		}
		checked = &p
		return true
	}
	resp, err := c.sendAndWaitMatching(msg, forThisPane, MsgTerminalState, MsgError)
	if err != nil {
		return nil, err
	}

	switch resp.Type {
	case MsgTerminalState:
		payload := checked
		if payload == nil {
			payload = &TerminalStatePayload{}
			if err := resp.ParsePayload(payload); err != nil {
				return nil, err
			}
		}
		if payload.PTYID != ptyID {
			return nil, errfmt.Errorf("get terminal state: the daemon answered for pane %s, not %s", shortID(payload.PTYID), shortID(ptyID))
		}
		if err := payload.State.CheckPacked(); err != nil {
			return nil, errfmt.Errorf("get terminal state: %w", err)
		}
		return payload.State, nil

	case MsgError:
		var errPayload ErrorPayload
		_ = resp.ParsePayload(&errPayload)
		return nil, errfmt.Errorf("get terminal state failed: %s", errPayload.Message)

	default:
		return nil, errfmt.Errorf("unexpected response: %d", resp.Type)
	}
}

// StartReadLoop starts the background goroutine that reads daemon messages.
// PTY output will be dispatched to registered handlers.
func (c *TUIClient) StartReadLoop() {
	c.readLoopRunning = true
	go c.readLoop()
}

func (c *TUIClient) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			debugLog("[CLIENT] PANIC in readLoop: %v", r)
		}
	}()
	for {
		select {
		case <-c.done:
			return
		default:
		}

		c.readMu.Lock()
		msg, err := ReadMessageBuffered(c.conn, c.reader(), 0, 30*time.Second)
		c.readMu.Unlock()

		if err != nil {
			if errors.Is(err, io.EOF) {
				c.handleDisconnect(err)
				return
			}
			debugLog("[CLIENT] readLoop fatal error, disconnecting: %v", err)
			c.handleDisconnect(err)
			return
		}

		if c.routeReply(msg) != replyNone {
			continue
		}

		c.handleMessage(msg)
	}
}

func (c *TUIClient) handleMessage(msg *Message) {
	switch msg.Type {
	case MsgPTYOutput:
		ptyID, data, err := ParseBinaryPTYMessage(msg.Payload)
		if err != nil || ptyID == "" {
			return
		}

		c.ptyHandlersMu.RLock()
		handler := c.ptyHandlers[ptyID]
		c.ptyHandlersMu.RUnlock()

		if handler != nil {
			handler(data)
		}

	case MsgPTYResized:
		var payload PTYResizedPayload
		if err := msg.ParsePayload(&payload); err != nil {
			return
		}
		c.ptyResizeHandlersMu.RLock()
		resized := c.ptyResizeHandlers[payload.PTYID]
		c.ptyResizeHandlersMu.RUnlock()
		if resized != nil {
			resized(payload.Width, payload.Height)
		}

	case MsgDaemonUpgrading:
		var payload DaemonUpgradingPayload
		if err := msg.ParsePayload(&payload); err == nil {
			v, bin := payload.Version, payload.Binary
			c.upgradeBinary.Store(&bin)
			c.upgradedTo.Store(&v)
		}

	case MsgPTYClosed:
		var payload ClosePTYPayload
		if err := msg.ParsePayload(&payload); err != nil {
			return
		}
		c.ptyClosedHandlersMu.Lock()
		closedHandler, registered := c.ptyClosedHandlers[payload.PTYID]
		if !registered && !slices.Contains(c.ptyClosedEarly, payload.PTYID) {
			if len(c.ptyClosedEarly) >= ptyClosedEarlyMax {
				c.ptyClosedEarly = slices.Delete(c.ptyClosedEarly, 0, 1)
			}
			c.ptyClosedEarly = append(c.ptyClosedEarly, payload.PTYID)
		}
		c.ptyClosedHandlersMu.Unlock()

		c.ptyHandlersMu.Lock()
		delete(c.ptyHandlers, payload.PTYID)
		c.ptyHandlersMu.Unlock()

		c.ptyClosedHandlersMu.Lock()
		delete(c.ptyClosedHandlers, payload.PTYID)
		c.ptyClosedHandlersMu.Unlock()

		c.ptyResizeHandlersMu.Lock()
		delete(c.ptyResizeHandlers, payload.PTYID)
		c.ptyResizeHandlersMu.Unlock()

		if closedHandler != nil {
			closedHandler()
		}

	case MsgGoodbye:
		var payload GoodbyePayload
		_ = msg.ParsePayload(&payload)
		c.goodbye.Store(&Goodbye{Reason: payload.Reason, At: time.Now()})

	case MsgSessionEnded:
		var payload SessionEndedPayload
		if err := msg.ParsePayload(&payload); err != nil {
			debugLog("[CLIENT] Failed to parse session ended: %v", err)
		}
		name := payload.SessionName
		if name == "" {
			name = c.SessionName()
		}
		if payload.Nested {
			reason := payload.Reason
			c.nestedRefusal.Store(&reason)
		}
		if payload.Detached {
			reason := payload.Reason
			if reason == "" {
				reason = DetachedByAttachMessage
			}
			c.detachedReason.Store(&reason)
		}
		c.multiClientMu.Lock()
		handler := c.sessionEndedHandler
		if handler == nil {
			c.pendingEnded = &[2]string{name, payload.Reason}
		}
		c.multiClientMu.Unlock()
		if handler != nil {
			c.sessionEndedOnce.Do(func() { handler(name, payload.Reason) })
		}

	case MsgAgentMail:
		var payload AgentMailPayload
		if err := msg.ParsePayload(&payload); err != nil {
			debugLog("[CLIENT] Failed to parse agent mail: %v", err)
			return
		}
		c.multiClientMu.RLock()
		handler := c.agentMailHandler
		c.multiClientMu.RUnlock()
		if handler != nil {
			handler(payload)
		}

	case MsgDirChanged:
		var payload DirChangedPayload
		if err := msg.ParsePayload(&payload); err != nil {
			debugLog("[CLIENT] Failed to parse a folder change: %v", err)
			return
		}
		c.multiClientMu.RLock()
		handler := c.dirChangedHandler
		c.multiClientMu.RUnlock()
		if handler != nil {
			handler(payload.Dir)
		}

	case MsgHostsChanged:
		if c.viaHost != "" {
			return
		}
		var payload HostsChangedPayload
		if err := msg.ParsePayload(&payload); err != nil {
			debugLog("[CLIENT] Failed to parse a hosts change: %v", err)
			return
		}
		c.multiClientMu.RLock()
		handler := c.hostsChangedHandler
		c.multiClientMu.RUnlock()
		if handler != nil {
			handler(payload)
		}

	case MsgSessionsChanged:
		c.multiClientMu.RLock()
		handler := c.sessionsChangedHandler
		c.multiClientMu.RUnlock()
		if handler != nil {
			handler()
		}

	case MsgPaneMark:
		c.multiClientMu.RLock()
		handler := c.paneMarkHandler
		c.multiClientMu.RUnlock()
		var payload PaneMarkPayload
		if handler != nil && msg.ParsePayload(&payload) == nil {
			handler(payload)
		}

	case MsgPluginUI:
		var payload PluginUIPayload
		if err := msg.ParsePayload(&payload); err != nil {
			debugLog("[CLIENT] Failed to parse plugin UI: %v", err)
			return
		}
		c.multiClientMu.Lock()
		handler := c.pluginUIHandler
		if handler == nil && payload.Notice == "" {
			c.pluginUILast = &payload
		}
		c.multiClientMu.Unlock()
		if handler != nil {
			handler(payload)
		}

	case MsgPrompt:
		var payload PromptPayload
		if err := msg.ParsePayload(&payload); err != nil {
			debugLog("[CLIENT] Failed to parse a prompt: %v", err)
			return
		}
		c.multiClientMu.Lock()
		handler := c.promptHandler
		if handler == nil {
			c.promptLast = &payload
		}
		c.multiClientMu.Unlock()
		if handler != nil {
			handler(payload)
		}

	case MsgPluginApprovals:
		var payload PluginApprovalsPayload
		if err := msg.ParsePayload(&payload); err != nil {
			debugLog("[CLIENT] Failed to parse plugin approvals: %v", err)
			return
		}
		c.multiClientMu.Lock()
		handler := c.pluginAsksHandler
		if handler == nil {
			c.pluginAsksLast = &payload
		}
		c.multiClientMu.Unlock()
		if handler != nil {
			handler(payload)
		}

	case MsgTMLSurface:
		if !c.DrawsTML() {
			return
		}
		var payload TMLSurfacePayload
		if msg.ParsePayload(&payload) != nil {
			return
		}
		c.multiClientMu.Lock()
		handler := c.tmlSurfaceHandler
		if handler == nil {
			c.tmlBacklog = append(c.tmlBacklog, &payload)
		}
		c.multiClientMu.Unlock()
		if handler != nil {
			handler(&payload)
		}

	case MsgDetached:
		debugLog("[CLIENT] Received MsgDetached (no-op in handleMessage)")

	case MsgError:
		var payload ErrorPayload
		if err := msg.ParsePayload(&payload); err == nil {
			debugLog("[CLIENT] daemon error %d: %s", payload.Code, payload.Message)
		}

	case MsgRemoteCommand:
		var payload RemoteCommandPayload
		if err := msg.ParsePayload(&payload); err != nil {
			debugLog("[REMOTE] Failed to parse remote command: %v", err)
			return
		}

		debugLog("[REMOTE] Received command: type=%s, tapeCmd=%s, args=%v, keys=%s", payload.CommandType, payload.TapeCommand, payload.TapeArgs, payload.Keys)

		if why := c.refuseHostCommand(&payload); why != "" {
			debugLog("[REMOTE] Refused a command from host %s: %s", c.viaHost, why)
			_ = c.SendCommandResult(payload.RequestID, false, why)
			return
		}

		c.remoteCommandMu.Lock()
		handler := c.remoteCommandHandler
		if handler == nil {
			if len(c.remoteCommandsEarly) >= remoteCommandsEarlyMax {
				c.remoteCommandsEarly = c.remoteCommandsEarly[1:]
			}
			retained := payload
			c.remoteCommandsEarly = append(c.remoteCommandsEarly, &retained)
		}
		c.remoteCommandMu.Unlock()

		if handler != nil {
			debugLog("[REMOTE] Executing command with handler")
			if err := handler(&payload); err != nil {
				debugLog("[REMOTE] Command handler error: %v", err)
				_ = c.SendCommandResult(payload.RequestID, false, err.Error())
			}
		} else {
			debugLog("[REMOTE] No handler registered yet, kept the command for it")
		}

	case MsgStateSync:
		var payload StateSyncPayload
		if err := msg.ParsePayload(&payload); err != nil {
			debugLog("[MULTICLIENT] Failed to parse state sync: %v", err)
			return
		}
		if err := validateSessionState(payload.State); err != nil {
			debugLog("[MULTICLIENT] Refused state sync: %v", err)
			return
		}

		c.multiClientMu.Lock()
		handler := c.stateSyncHandler
		if handler == nil {
			retained := payload
			c.pendingStateSync = &retained
		}
		c.multiClientMu.Unlock()

		if payload.State != nil && payload.State.Name != "" {
			c.setSessionName(payload.State.Name)
		}

		if handler != nil {
			handler(payload.State, payload.TriggerType, payload.SourceID)
		}

	case MsgClientJoined:
		var payload ClientJoinedPayload
		if err := msg.ParsePayload(&payload); err != nil {
			debugLog("[MULTICLIENT] Failed to parse client joined: %v", err)
			return
		}

		c.multiClientMu.RLock()
		handler := c.clientJoinedHandler
		c.multiClientMu.RUnlock()

		if handler != nil {
			handler(payload.ClientID, payload.ClientCount, payload.Width, payload.Height)
		}

	case MsgClientLeft:
		var payload ClientLeftPayload
		if err := msg.ParsePayload(&payload); err != nil {
			debugLog("[MULTICLIENT] Failed to parse client left: %v", err)
			return
		}

		c.multiClientMu.RLock()
		handler := c.clientLeftHandler
		c.multiClientMu.RUnlock()

		if handler != nil {
			handler(payload.ClientID, payload.ClientCount)
		}

	case MsgSessionResize:
		var payload SessionResizePayload
		if err := msg.ParsePayload(&payload); err != nil {
			debugLog("[MULTICLIENT] Failed to parse session resize: %v", err)
			return
		}

		if !c.noteSessionLayout(payload.Generation, payload.Reserve) {
			debugLog("[MULTICLIENT] dropped a stale session resize at generation %d", payload.Generation)
			return
		}
		if payload.Policy != "" {
			policy := payload.Policy
			c.sizePolicy.Store(&policy)
		}
		c.sizedBy.Store(&sessionSizer{machine: payload.SizedBy, client: payload.SizedByClient, kind: payload.SizedByKind})

		c.multiClientMu.Lock()
		handler := c.sessionResizeHandler
		if handler == nil {
			retained := payload
			c.pendingSessionResize = &retained
		}
		c.multiClientMu.Unlock()

		if handler != nil {
			handler(payload.Width, payload.Height, payload.ClientCount, payload.Reserve)
		}

	}
}

// Close closes the connection to the daemon. Idempotent: the done channel is
// closed at most once, so concurrent or repeated Close calls cannot panic.
func (c *TUIClient) Close() error {
	c.doneOnce.Do(func() { close(c.done) })

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// SessionName returns the attached session name.
func (c *TUIClient) SessionName() string {
	if p := c.sessionName.Load(); p != nil {
		return *p
	}
	return ""
}

func (c *TUIClient) setSessionName(name string) {
	c.sessionName.Store(&name)
}

// Rig reports whether the daemon said, at the last attach, that it is a docs
// rig. See rig.go.
func (c *TUIClient) Rig() bool { return c != nil && c.rig.Load() }

// HumanNonce returns the secret the daemon issued for the current attach, or
// "" when there is none. Pass it as human_nonce on send-agent-message from
// human; see AttachedPayload.HumanNonce.
func (c *TUIClient) HumanNonce() string {
	if p := c.humanNonce.Load(); p != nil {
		return *p
	}
	return ""
}

// AvailableSessionNames returns the list of available sessions from the daemon.
func (c *TUIClient) AvailableSessionNames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.availableSessions))
	for _, s := range c.availableSessions {
		names = append(names, s.Name)
	}
	return names
}

// SessionOrderSet reports whether the cached listing carries the daemon's
// session order (set-session-order): a rank on any session. The listing is
// sorted by it, so a client shows it as it comes.
func (c *TUIClient) SessionOrderSet() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Rank > 0 {
			return true
		}
	}
	return false
}

// SessionLabel returns the cached display name and accent for the named
// session, both empty when it is unknown or carries neither. The caller falls
// back to the session name, which stays the identity in every case.
func (c *TUIClient) SessionLabel(name string) (display, accent string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return s.DisplayName, s.Accent
		}
	}
	return "", ""
}

// SessionPlace returns the directory label and git branch the daemon reports
// for the named session's focused pane, both empty when unknown. Read from the
// cached listing, so it costs no round trip and no file read.
func (c *TUIClient) SessionPlace(name string) (dir, branch string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return s.Dir, s.Branch
		}
	}
	return "", ""
}

// SessionAgentTitle returns the title of the named session's first agent pane
// (see SessionAgentTitle in session_agent_title.go), empty when there is none.
// Read from the cached listing, so it costs no round trip.
func (c *TUIClient) SessionAgentTitle(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return SessionAgentTitle(s.Windows)
		}
	}
	return ""
}

// SessionRestored reports whether the named session came back from saved state
// and has not been attached to since, from the cached listing. False for an
// unknown session and for an older daemon that does not send the field, which
// is the same silence a surface had before the field existed.
func (c *TUIClient) SessionRestored(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return s.Restored
		}
	}
	return false
}

// SessionGlobal reports whether the named session is a global one, from the
// cached listing. False for a session this client has not been told about, and
// false from a daemon too old to send the field, which reads as the ordinary
// session it would have been before global sessions existed.
func (c *TUIClient) SessionGlobal(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return s.Global
		}
	}
	return false
}

// SessionSpace is the space the named session is filed under, from the cached
// listing. Empty for a session in none, for one this client has not been told
// about, and from a daemon too old to send the field, which all read as a
// session in no space.
func (c *TUIClient) SessionSpace(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return s.Space
		}
	}
	return ""
}

// SessionNote is the named session's return note from the cached listing,
// empty when it has none or is unknown.
func (c *TUIClient) SessionNote(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return s.Note
		}
	}
	return ""
}

// SessionWindowNote is the return note of one pane of the named session, from
// the cached listing, empty when it has none or is unknown.
func (c *TUIClient) SessionWindowNote(name, windowID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name != name {
			continue
		}
		for _, w := range s.Windows {
			if w.ID == windowID {
				return w.Note
			}
		}
	}
	return ""
}

// SessionCurrentWorkspace is the workspace the named session is showing, from
// the cached listing, or 0 when it is unknown: an older daemon does not send
// the field, and a surface reading zero simply says nothing about where that
// session's panes live rather than saying something wrong.
func (c *TUIClient) SessionCurrentWorkspace(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return s.CurrentWorkspace
		}
	}
	return 0
}

// SessionCount returns how many sessions the cache currently knows about. The
// client gates its foreign-session poll on this so a lone-session client makes
// no network round trips at idle.
func (c *TUIClient) SessionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.availableSessions)
}

// CachedSessions returns the cached listing as it stands, each session with the
// window summaries it was listed with. A reader needing more than one field of
// an entry takes this instead of one accessor per field. The slice is a copy,
// and a refresh replaces entries rather than editing them, so the summaries it
// shares cannot change underneath the caller.
func (c *TUIClient) CachedSessions() []SessionInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]SessionInfo(nil), c.availableSessions...)
}

// SessionWindows returns the cached per-window summaries for the named session,
// or nil if the session is unknown or its windows have not been fetched yet. The
// sidebar reads this to expand a non-attached session's tree without a blocking
// round trip.
func (c *TUIClient) SessionWindows(name string) []WindowSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return append([]WindowSummary(nil), s.Windows...)
		}
	}
	return nil
}

// TryRefreshSessionList refreshes the cache on the caller's goroutine unless a
// refresh is already running, in which case it returns immediately. It is the
// entry point for the periodic background refresh; call it from a tea.Cmd so it
// never runs on the UI goroutine. Errors are swallowed: a failed refresh leaves
// the last good cache in place.
func (c *TUIClient) TryRefreshSessionList() {
	if !c.refreshInFlight.CompareAndSwap(false, true) {
		return
	}
	defer c.refreshInFlight.Store(false)
	_, _ = c.RefreshSessionList()
}

// CreateDetachedSession asks the daemon for a headless session with an initial
// window, the same request `tuios new` makes over a control connection. The
// daemon answers with the refreshed listing, which is applied to the cache so
// the new session is in the rail before the next poll rather than after it.
func (c *TUIClient) CreateDetachedSession(name string, width, height int) error {
	return c.createSession(name, width, height, false)
}

// CreateGlobalSession creates a session meant to hold panes from more than one
// machine. It is created with no windows: the first one is the pane the user
// picks a machine for. See NewPayload.Global.
func (c *TUIClient) CreateGlobalSession(name string, width, height int) error {
	return c.createSession(name, width, height, true)
}

func (c *TUIClient) createSession(name string, width, height int, global bool) error {
	msg, err := NewMessage(MsgNew, &NewPayload{
		SessionName: name,
		Width:       width,
		Height:      height,
		Detach:      true,
		Global:      global,
	})
	if err != nil {
		return err
	}
	stamp := c.listingStamp()
	resp, err := c.sendAndWaitResponse(msg, MsgSessionList, MsgError)
	if err != nil {
		return err
	}
	if resp.Type == MsgError {
		var errPayload ErrorPayload
		_ = resp.ParsePayload(&errPayload)
		return errfmt.Errorf("create session: %s", errPayload.Message)
	}
	var payload SessionListPayload
	if err := resp.ParsePayload(&payload); err != nil {
		return err
	}
	c.applySessionListing(payload.Sessions, stamp)
	return nil
}

// RefreshSessionList queries the daemon for an up-to-date session list and
// updates the cached availableSessions. Blocks until response arrives.
// Safe to call while the read loop is running.
func (c *TUIClient) RefreshSessionList() ([]SessionInfo, error) {
	listMsg, err := NewMessage(MsgList, nil)
	if err != nil {
		return nil, err
	}
	stamp := c.listingStamp()
	resp, err := c.sendAndWaitResponse(listMsg, MsgSessionList, MsgError)
	if err != nil {
		return nil, err
	}
	if resp.Type == MsgError {
		var errPayload ErrorPayload
		_ = resp.ParsePayload(&errPayload)
		return nil, errfmt.Errorf("list sessions: %s", errPayload.Message)
	}
	var payload SessionListPayload
	if err := resp.ParsePayload(&payload); err != nil {
		return nil, err
	}
	c.applySessionListing(payload.Sessions, stamp)
	return payload.Sessions, nil
}

// UpdateSessionCache replaces the cached session listing, including each
// session's window summaries. It is the seam that seeds the cache without a live
// daemon; a listing handed in here is treated as current.
func (c *TUIClient) UpdateSessionCache(sessions []SessionInfo) {
	c.applySessionListing(sessions, c.listingStamp())
}

func (c *TUIClient) listingStamp() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.localGen
}

func (c *TUIClient) applySessionListing(sessions []SessionInfo, sinceGen uint64) {
	c.mu.Lock()
	if sinceGen == c.localGen {
		c.localSessions = nil
	} else {
		sessions = appendUnlisted(sessions, c.availableSessions, c.localSessions)
	}
	changed := !listingsAgree(c.availableSessions, sessions)
	c.availableSessions = sessions
	c.mu.Unlock()
	if changed {
		c.cacheGen.Add(1)
	}
}

func appendUnlisted(listing, cached []SessionInfo, local []string) []SessionInfo {
	for _, name := range local {
		if slices.ContainsFunc(listing, func(s SessionInfo) bool { return s.Name == name }) {
			continue
		}
		info := SessionInfo{Name: name}
		if i := slices.IndexFunc(cached, func(s SessionInfo) bool { return s.Name == name }); i >= 0 {
			info = cached[i]
		}
		listing = append(listing, info)
	}
	return listing
}

func listingsAgree(a, b []SessionInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].WindowCount != b[i].WindowCount {
			return false
		}
		if a[i].DisplayName != b[i].DisplayName || a[i].Accent != b[i].Accent || a[i].Note != b[i].Note {
			return false
		}
		if a[i].Restored != b[i].Restored {
			return false
		}
		if a[i].Dir != b[i].Dir || a[i].Branch != b[i].Branch {
			return false
		}
		if a[i].Space != b[i].Space {
			return false
		}
		if !reflect.DeepEqual(a[i].Worktree, b[i].Worktree) {
			return false
		}
		if a[i].Rank != b[i].Rank {
			return false
		}
		if !slices.EqualFunc(a[i].Windows, b[i].Windows, windowSummariesAgree) {
			return false
		}
	}
	return true
}

// NoteSession records a session this client just attached to. Attaching with
// CreateNew is how a session is born, and until the daemon is listed again
// nothing else knows it exists: the sidebar builds foreign rows from this cache,
// so without the note the new session survives only while it is the attached one
// and vanishes the moment the client switches away.
func (c *TUIClient) NoteSession(name string) {
	if name == "" {
		return
	}
	c.mu.Lock()
	if slices.ContainsFunc(c.availableSessions, func(s SessionInfo) bool { return s.Name == name }) {
		c.mu.Unlock()
		return
	}
	c.availableSessions = append(c.availableSessions, SessionInfo{Name: name})
	c.localSessions = append(c.localSessions, name)
	c.localGen++
	c.mu.Unlock()
	c.cacheGen.Add(1)
}

// CacheGen returns a counter that changes whenever the cached session listing
// changes. The sidebar folds it into its render-cache key so foreign-session
// updates rebuild the rail without locking the client mutex every frame.
func (c *TUIClient) CacheGen() uint64 {
	return c.cacheGen.Load()
}

var errNoConnection = errors.New("not connected to the daemon")

func (c *TUIClient) send(msg *Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return errNoConnection
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return WriteMessage(c.conn, msg)
}

func (c *TUIClient) recv() (*Message, error) {
	wait := 30 * time.Second
	if c.handshaking && c.HandshakeTimeout > 0 {
		wait = c.HandshakeTimeout
	}
	return c.recvUntil(time.Now().Add(wait))
}

func (c *TUIClient) recvUntil(deadline time.Time) (*Message, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	if limit := time.Now().Add(30 * time.Second); deadline.After(limit) {
		deadline = limit
	}
	_ = c.conn.SetReadDeadline(deadline)
	return ReadMessage(c.reader())
}

func (c *TUIClient) reader() *bufio.Reader {
	if c.br == nil {
		c.br = bufio.NewReaderSize(c.conn, clientReadBuffer)
	}
	return c.br
}

const clientReadBuffer = 64 * 1024

type pendingReply struct {
	ch     chan *Message
	accept func(*Message) bool
}

type replyRoute int

const (
	replyNone replyRoute = iota
	replyDelivered
	replyStale
)

func (c *TUIClient) routeReply(msg *Message) replyRoute {
	c.pendingResponsesMu.Lock()
	defer c.pendingResponsesMu.Unlock()

	var p *pendingReply
	if msg.ReqID != 0 {
		p = c.pendingByID[msg.ReqID]
		if p == nil && c.takePasteReply(msg) {
			return replyDelivered
		}
		if p == nil {
			debugLog("[CLIENT] dropped %s answering request %d, which nothing waits for", MessageTypeName(msg.Type), msg.ReqID)
			return replyStale
		}
		delete(c.pendingByID, msg.ReqID)
	} else {
		p = c.pendingResponses[msg.Type]
		if p == nil {
			return replyNone
		}
		if p.accept != nil && !p.accept(msg) {
			return replyStale
		}
		delete(c.pendingResponses, msg.Type)
	}
	if msg.Type == MsgAttached {
		c.attachGen.Store(attachCounter.Add(1))
	}
	select {
	case p.ch <- msg:
	default:
	}
	return replyDelivered
}

func (c *TUIClient) sendAndWaitResponse(msg *Message, expectedTypes ...MessageType) (*Message, error) {
	return c.sendAndWaitMatching(msg, nil, expectedTypes...)
}

func (c *TUIClient) sendAndWaitMatching(msg *Message, accept func(*Message) bool, expectedTypes ...MessageType) (*Message, error) {
	c.roundTripMu.Lock()
	defer c.roundTripMu.Unlock()

	msg.ReqID = c.nextReqID.Add(1)

	if !c.readLoopRunning {
		if err := c.send(msg); err != nil {
			return nil, err
		}
		for {
			resp, err := c.recv()
			if err != nil {
				return nil, err
			}
			if resp.ReqID != 0 && resp.ReqID != msg.ReqID {
				continue
			}
			if resp.ReqID == 0 && accept != nil && slices.Contains(expectedTypes, resp.Type) && !accept(resp) {
				continue
			}
			return resp, nil
		}
	}

	respChan := make(chan *Message, 1)
	p := &pendingReply{ch: respChan, accept: accept}

	c.pendingResponsesMu.Lock()
	c.pendingByID[msg.ReqID] = p
	c.pendingResponsesMu.Unlock()

	defer func() {
		c.pendingResponsesMu.Lock()
		delete(c.pendingByID, msg.ReqID)
		c.pendingResponsesMu.Unlock()
	}()

	if err := c.send(msg); err != nil {
		return nil, err
	}

	select {
	case resp := <-respChan:
		return resp, nil
	case <-time.After(roundTripTimeout):
		return nil, errfmt.Errorf("timeout waiting for response")
	case <-c.done:
		return nil, errfmt.Errorf("client closed")
	}
}

func windowSummariesAgree(a, b WindowSummary) bool {
	return a.ID == b.ID && a.Title == b.Title &&
		a.AgentState == b.AgentState && a.AgentStateAt == b.AgentStateAt &&
		a.AgentHarness == b.AgentHarness && a.AgentMessage == b.AgentMessage &&
		a.AgentKind == b.AgentKind && a.AgentReason == b.AgentReason &&
		a.CompletionSeq == b.CompletionSeq &&
		slices.Equal(a.AgentMeta, b.AgentMeta) &&
		a.AgentQueued == b.AgentQueued && a.DraftHeld == b.DraftHeld && a.AgentSettled == b.AgentSettled && a.AgentQuiet == b.AgentQuiet && a.Subagents == b.Subagents &&
		slices.Equal(a.SubagentList, b.SubagentList) &&
		slices.Equal(a.ProgramStatus, b.ProgramStatus) &&
		a.ForegroundCmd == b.ForegroundCmd && a.Workspace == b.Workspace &&
		a.Scratch == b.Scratch && a.Note == b.Note && a.Cwd == b.Cwd &&
		(a.AgentParent == nil) == (b.AgentParent == nil) && (a.AgentParent == nil || *a.AgentParent == *b.AgentParent)
}

// UpgradeBinary is the path of the binary a live upgrade told this client
// the daemon now runs, empty when none did or the daemon did not say.
func (c *TUIClient) UpgradeBinary() string {
	if v := c.upgradeBinary.Load(); v != nil {
		return *v
	}
	return ""
}

// UpgradedTo is the version a live upgrade said the daemon was becoming as
// it closed this client's connection, and whether one did.
func (c *TUIClient) UpgradedTo() (string, bool) {
	if v := c.upgradedTo.Load(); v != nil {
		return *v, true
	}
	return "", false
}
