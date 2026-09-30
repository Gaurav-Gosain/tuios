package session

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
)

// Manager manages all persistent sessions for a user.
// It handles session creation, lookup, and lifecycle management.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session // Sessions by name
	byID     map[string]*Session // Sessions by ID (for quick lookup)
	// aliases maps a name a session was renamed from to that session's ID. A
	// pane started before a rename keeps TUIOS_SESSION set to the old name,
	// and a running process cannot be given a new environment, so a verb that
	// names the old session still reaches it through here. A live session of
	// that name always wins, and an alias goes when its session does.
	aliases map[string]string

	// Configuration
	socketPath string // Path to socket
	// scrollbackLines is stamped into every session made here, so each pane
	// keeps the history depth the daemon was configured with.
	scrollbackLines int
	// inheritCwd is appearance.new_window_inherit_cwd, stamped into every
	// session this manager makes.
	inheritCwd bool
	// preferredShell is appearance.preferred_shell. Every session this manager
	// makes reads it at spawn time through PreferredShell, so a config reload
	// reaches the next pane of a session that already exists. It is atomic so
	// a spawn never needs m.mu.
	preferredShell atomic.Pointer[string]
	// herdrSocket is the herdr protocol socket the daemon listens on, "" when
	// it does not. herdrMode is [agents] herdr_protocol. Both are read at
	// spawn time through HerdrEnv. See herdr_compat.go.
	herdrSocket atomic.Pointer[string]
	herdrMode   atomic.Pointer[string]
	// herdrBin is HERDR_BIN_PATH, this tuios. See SetHerdrBin.
	herdrBin atomic.Pointer[string]
	// paneTokenKey signs the TUIOS_PANE_TOKEN every pane is started with. It
	// is picked at random for each manager and never leaves memory. See
	// pane_token.go.
	paneTokenKey []byte
	// grants holds what every local pane may do through tuios, and the
	// [agents.permissions] default. It is stamped into every session made
	// here. See pane_grants.go.
	grants *paneGrantTable

	// Lifecycle hooks (set by the daemon). onCreate fires after a session is
	// registered; onDelete fires after it is removed but before it is stopped.
	// Both run outside m.mu so a hook may safely call back into the manager.
	onCreate func(*Session)
	onDelete func(*Session)
	// onRename fires after a rename, outside m.mu, with the old name.
	onRename func(sess *Session, old string)
}

// SetRenameHook installs the callback RenameSession fires once a session has
// its new name. The daemon uses it to tell every client to list again.
func (m *Manager) SetRenameHook(fn func(sess *Session, old string)) {
	m.mu.Lock()
	m.onRename = fn
	m.mu.Unlock()
}

// SetSessionHooks installs lifecycle callbacks invoked when a session is created
// or deleted. The daemon uses these to install each session's event sink and to
// publish session lifecycle events.
func (m *Manager) SetSessionHooks(onCreate, onDelete func(*Session)) {
	m.mu.Lock()
	m.onCreate = onCreate
	m.onDelete = onDelete
	m.mu.Unlock()
}

// NewManager creates a new session manager.
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		byID:     make(map[string]*Session),
		aliases:  make(map[string]string),
		// The default matches config.DefaultSettings: a daemon nobody
		// configured still opens windows where the user is looking.
		inheritCwd:   true,
		paneTokenKey: newPaneTokenKey(),
		grants:       newPaneGrantTable(),
	}
}

// GetSocketPath and GetPidFilePath are defined in platform-specific files:
// - manager_unix.go for Unix/Linux/macOS
// - manager_windows.go for Windows

// SetScrollbackLines sets the history depth every session made from now on
// gives its panes. Zero means the emulator's default.
func (m *Manager) SetScrollbackLines(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scrollbackLines = n
}

// SetNewWindowInheritCwd sets whether a window made from now on starts in the
// focused pane's directory.
func (m *Manager) SetNewWindowInheritCwd(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inheritCwd = v
}

// SetPreferredShell sets the shell a pane runs when its session names none.
// Empty means $SHELL and then the platform default.
func (m *Manager) SetPreferredShell(shell string) {
	m.preferredShell.Store(&shell)
}

// PreferredShell is what SetPreferredShell last set, or "".
func (m *Manager) PreferredShell() string {
	if p := m.preferredShell.Load(); p != nil {
		return *p
	}
	return ""
}

// SetHerdrSocket records the herdr protocol socket the daemon listens on,
// "" for none.
func (m *Manager) SetHerdrSocket(path string) {
	m.herdrSocket.Store(&path)
}

// SetHerdrProtocol sets which panes are told about the herdr protocol
// socket: config.HerdrProtocolAlways, config.HerdrProtocolAgents or
// config.HerdrProtocolOff.
func (m *Manager) SetHerdrProtocol(mode string) {
	m.herdrMode.Store(&mode)
}

// SetHerdrBin records the program a pane is given as HERDR_BIN_PATH: this
// tuios, which answers herdr's report commands (tuios pane report-agent and
// the rest) over the herdr protocol socket. "" gives no HERDR_BIN_PATH.
func (m *Manager) SetHerdrBin(path string) {
	m.herdrBin.Store(&path)
}

// HerdrEnv is the herdr environment a pane that runs command (nil for the
// user's shell) is started with, nil for none: HERDR_ENV, HERDR_SOCKET_PATH
// naming tuios's own socket, HERDR_PANE_ID naming the pane, and
// HERDR_BIN_PATH naming this tuios, for a reporter that calls herdr's CLI
// rather than the socket. A pane gets it when the daemon listens on the
// socket and [agents] herdr_protocol is not off: every pane by default, as
// in herdr, or with "agents" only a pane that starts a harness known to
// report this way. See herdr_compat.go.
func (m *Manager) HerdrEnv(windowID string, command []string) []string {
	sock := ""
	if p := m.herdrSocket.Load(); p != nil {
		sock = *p
	}
	mode := config.HerdrProtocolAlways
	if p := m.herdrMode.Load(); p != nil {
		mode = config.NormalizeHerdrProtocol(*p)
	}
	if sock == "" || windowID == "" || mode == config.HerdrProtocolOff {
		return nil
	}
	if mode != config.HerdrProtocolAlways && !guestenv.SpeaksHerdrProtocol(command) {
		return nil
	}
	env := []string{"HERDR_ENV=1", "HERDR_SOCKET_PATH=" + sock, "HERDR_PANE_ID=" + windowID}
	if p := m.herdrBin.Load(); p != nil && *p != "" {
		env = append(env, "HERDR_BIN_PATH="+*p)
	}
	return env
}

// HostName is the name this machine gives itself, for TUIOS_HOST: the
// hostname the operating system reports, or "" when it reports none.
func (m *Manager) HostName() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// SetSocketPath sets the socket path (for testing).
func (m *Manager) SetSocketPath(path string) {
	m.socketPath = path
}

// SocketPath returns the configured socket path.
func (m *Manager) SocketPath() string {
	if m.socketPath != "" {
		return m.socketPath
	}
	path, _ := GetSocketPath()
	return path
}

// CreateSession creates a new session with the given name.
func (m *Manager) CreateSession(name string, cfg *SessionConfig, width, height int) (*Session, error) {
	// Validated before the session exists, so a name that could never be saved
	// is refused rather than producing a session that runs and never persists.
	if err := ValidateSessionName(name); err != nil {
		return nil, err
	}

	m.mu.Lock()

	// Check if name already exists
	if name != "" {
		if _, exists := m.sessions[name]; exists {
			m.mu.Unlock()
			return nil, fmt.Errorf("session '%s' already exists", name)
		}
	}

	// Stamp the daemon socket path so shells spawned in this session can find the
	// daemon (exported as TUIOS_SOCKET) without every caller having to know it.
	if cfg == nil {
		cfg = &SessionConfig{}
	}
	if cfg.SocketPath == "" {
		cfg.SocketPath = m.SocketPath()
	}
	if cfg.ScrollbackLines == 0 {
		cfg.ScrollbackLines = m.scrollbackLines
	}
	if cfg.HostName == "" {
		cfg.HostName = m.HostName()
	}
	// Read directly, not through a helper: m.mu is already held here, and the
	// fields above are read the same way for the same reason.
	cfg.InheritCwd = m.inheritCwd
	if cfg.PreferredShell == nil {
		cfg.PreferredShell = m.PreferredShell
	}
	if cfg.PaneToken == nil {
		cfg.PaneToken = m.PaneToken
	}
	if cfg.HerdrEnv == nil {
		cfg.HerdrEnv = m.HerdrEnv
	}
	if cfg.grants == nil {
		cfg.grants = m.grants
	}

	// Create the session
	session, err := NewSession(name, cfg, width, height)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}

	// If no name was provided, one was auto-generated
	name = session.Name()

	// Register the session. A name that used to be an alias now names this
	// session, so the alias stops pointing at the one renamed away from it.
	m.sessions[name] = session
	delete(m.aliases, name)
	m.byID[session.ID] = session
	onCreate := m.onCreate
	m.mu.Unlock()

	// Fire the create hook outside the lock so it may install the event sink and
	// publish a session-created event without risking a manager re-entry deadlock.
	if onCreate != nil {
		onCreate(session)
	}
	return session, nil
}

// GetSession returns a session by name.
func (m *Manager) GetSession(name string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[name]
}

// GetSessionByID returns a session by ID.
func (m *Manager) GetSessionByID(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byID[id]
}

// ResolveSession returns the session a name addresses: the live session of
// that name, or else the session renamed away from it. renamed is true only
// in the second case, so a caller that must not follow a rename, such as an
// attach by name, can tell the two apart.
func (m *Manager) ResolveSession(name string) (sess *Session, renamed bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s := m.sessions[name]; s != nil {
		return s, false
	}
	if id, ok := m.aliases[name]; ok {
		if s := m.byID[id]; s != nil {
			return s, true
		}
	}
	return nil, false
}

// dropAliasesLocked forgets every old name of the session with this ID. The
// caller holds m.mu.
func (m *Manager) dropAliasesLocked(id string) {
	for alias, target := range m.aliases {
		if target == id {
			delete(m.aliases, alias)
		}
	}
}

// RenameSession gives the session named old the name newName. The name is the
// session's only name: the session map key, the state file, what ls and every
// client list, and what TUIOS_SESSION holds in panes started from now on. It
// also clears the session's display label, so every view shows the new name.
//
// Panes that already run keep TUIOS_SESSION=old. The old name stays an alias
// for this session (see ResolveSession) until the session goes or a new
// session takes that name.
func (m *Manager) RenameSession(old, newName string) (*Session, error) {
	if newName == "" {
		return nil, fmt.Errorf("a session name cannot be empty")
	}
	if err := ValidateSessionName(newName); err != nil {
		return nil, err
	}

	sess := m.GetSession(old)
	if sess == nil {
		return nil, fmt.Errorf("session '%s' not found", old)
	}
	if old == newName {
		return sess, nil
	}
	// Held from before the name changes until the state file has moved, so no
	// save of this session can land in between. See Session.persist. It is
	// taken before m.mu, never while m.mu is held, so a slow save of this
	// session never stalls every other lookup in the daemon.
	sess.persistMu.Lock()
	defer sess.persistMu.Unlock()

	// A saved session that is not running still owns its state file. Taking
	// its name would overwrite that file, and the saved session would be lost.
	if _, err := os.Stat(getResurrectionPath(newName)); err == nil && m.GetSession(newName) == nil {
		return nil, fmt.Errorf("a saved session is named '%s'. Restore it with 'tuios resurrect %s', or pick another name", newName, newName)
	}

	m.mu.Lock()
	if m.sessions[old] != sess {
		// Renamed or killed while this call waited for the save.
		m.mu.Unlock()
		return nil, fmt.Errorf("session '%s' not found", old)
	}
	if _, exists := m.sessions[newName]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("session '%s' already exists", newName)
	}
	delete(m.sessions, old)
	m.sessions[newName] = sess
	delete(m.aliases, newName)
	m.aliases[old] = sess.ID
	sess.setName(newName)
	others := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if s != sess {
			others = append(others, s)
		}
	}
	onRename := m.onRename
	m.mu.Unlock()

	// Through mutateState, so every attached client gets the new name on the
	// same push as any other daemon-side change, and adopts it.
	_ = sess.mutateState(func(st *SessionState) error {
		st.Name = newName
		st.DisplayName = ""
		return nil
	})

	// Sessions a fan started from this one record it by name.
	for _, s := range others {
		if wt := s.Worktree(); wt != nil && wt.LaunchedFrom == old {
			moved := *wt
			moved.LaunchedFrom = newName
			_ = s.SetWorktree(&moved)
		}
	}

	// The new file is written before the old one goes, so a crash between
	// the two leaves the session saved under one name or both, never none.
	if st := sess.ResurrectionState(); st != nil {
		if err := SaveSessionForResurrection(st); err != nil {
			LogError("Resurrection save for renamed session %q failed: %v", newName, err)
		}
	}
	RemoveResurrectionState(old)

	if onRename != nil {
		onRename(sess, old)
	}
	return sess, nil
}

// GetOrCreateSession returns an existing session or creates a new one.
func (m *Manager) GetOrCreateSession(name string, cfg *SessionConfig, width, height int) (*Session, bool, error) {
	// First try to get existing session
	m.mu.RLock()
	session, exists := m.sessions[name]
	m.mu.RUnlock()

	if exists {
		return session, false, nil
	}

	// Create new session
	session, err := m.CreateSession(name, cfg, width, height)
	if err != nil {
		return nil, false, err
	}

	return session, true, nil
}

// DeleteSession removes and stops a session.
func (m *Manager) DeleteSession(name string) error {
	m.mu.Lock()
	session, exists := m.sessions[name]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("session '%s' not found", name)
	}
	delete(m.sessions, name)
	delete(m.byID, session.ID)
	m.dropAliasesLocked(session.ID)
	onDelete := m.onDelete
	m.mu.Unlock()

	// Fire the delete hook outside the lock (publishes a session-closed event).
	if onDelete != nil {
		onDelete(session)
	}

	// Stop the session (outside lock to avoid deadlock). Stop performs a final
	// resurrection save, so remove the state file afterwards: an explicit kill
	// is a deliberate teardown and must not leave the session resurrectable.
	session.Stop()
	RemoveResurrectionState(name)
	return nil
}

// ListSessions returns information about all sessions.
func (m *Manager) ListSessions() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Order the sessions themselves, not their Info snapshots: Info truncates
	// Created to whole seconds, so two sessions made in the same second tied and
	// took the map's random iteration order. Sort on the full-precision timestamp,
	// with the name as a final tiebreak, so the list is stable everywhere it is
	// read (sidebar, switcher, palette).
	ordered := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		ordered = append(ordered, session)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Created.Equal(ordered[j].Created) {
			return ordered[i].Name() < ordered[j].Name()
		}
		return ordered[i].Created.Before(ordered[j].Created)
	})

	infos := make([]SessionInfo, 0, len(ordered))
	for _, session := range ordered {
		infos = append(infos, session.Info())
	}
	return infos
}

// AllSessions returns every live session. It backs the daemon's agent-state
// stall monitor, which needs the session objects themselves rather than the
// SessionInfo summaries ListSessions returns.
func (m *Manager) AllSessions() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

// GetDefaultSession returns the first/default session, creating one if none exist.
func (m *Manager) GetDefaultSession(cfg *SessionConfig, width, height int) (*Session, error) {
	m.mu.RLock()
	// Return first session if any exist
	for _, session := range m.sessions {
		m.mu.RUnlock()
		return session, nil
	}
	m.mu.RUnlock()

	// No sessions, create default with generated name
	name := m.GenerateSessionName()
	return m.CreateSession(name, cfg, width, height)
}

// Shutdown stops all sessions and cleans up.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = make(map[string]*Session)
	m.byID = make(map[string]*Session)
	m.aliases = make(map[string]string)
	m.mu.Unlock()

	// Stop all sessions (outside lock)
	for _, session := range sessions {
		session.Stop()
	}
}

// GenerateSessionName generates a unique session name in session-N format.
func (m *Manager) GenerateSessionName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Find the lowest available number using "session-N" format
	for i := 0; ; i++ {
		name := fmt.Sprintf("session-%d", i)
		if _, exists := m.sessions[name]; !exists {
			return name
		}
	}
}
