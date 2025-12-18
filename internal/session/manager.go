package session

import (
	"fmt"
	"sort"
	"sync"
)

// Manager manages all persistent sessions for a user.
// It handles session creation, lookup, and lifecycle management.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session // Sessions by name
	byID     map[string]*Session // Sessions by ID (for quick lookup)

	// Configuration
	socketPath string // Path to socket
}

// NewManager creates a new session manager.
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		byID:     make(map[string]*Session),
	}
}

// GetSocketPath and GetPidFilePath are defined in platform-specific files:
// - manager_unix.go for Unix/Linux/macOS
// - manager_windows.go for Windows

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
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if name already exists
	if name != "" {
		if _, exists := m.sessions[name]; exists {
			return nil, fmt.Errorf("session '%s' already exists", name)
		}
	}

	// Create the session
	session, err := NewSession(name, cfg, width, height)
	if err != nil {
		return nil, err
	}

	// If no name was provided, one was auto-generated
	name = session.Name

	// Register the session
	m.sessions[name] = session
	m.byID[session.ID] = session

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
	m.mu.Unlock()

	// Stop the session (outside lock to avoid deadlock)
	session.Stop()
	return nil
}

// ListSessions returns information about all sessions.
func (m *Manager) ListSessions() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]SessionInfo, 0, len(m.sessions))
	for _, session := range m.sessions {
		infos = append(infos, session.Info())
	}

	// Sort by creation time (oldest first)
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Created < infos[j].Created
	})

	return infos
}

// SessionCount returns the number of active sessions.
func (m *Manager) SessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
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
	m.mu.Unlock()

	// Stop all sessions (outside lock)
	for _, session := range sessions {
		session.Stop()
	}
}

// HasSessions returns true if there are any active sessions.
func (m *Manager) HasSessions() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions) > 0
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
