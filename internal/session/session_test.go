package session

import (
	"bytes"
	"testing"
)

// TestProtocolMessages tests the protocol message encoding/decoding
func TestProtocolMessages(t *testing.T) {
	tests := []struct {
		name    string
		msgType MessageType
		payload any
	}{
		{
			name:    "HelloPayload",
			msgType: MsgHello,
			payload: &HelloPayload{
				Version:   "1.0.0",
				Term:      "xterm-256color",
				ColorTerm: "truecolor",
				Shell:     "/bin/bash",
				Width:     80,
				Height:    24,
			},
		},
		{
			name:    "WelcomePayload",
			msgType: MsgWelcome,
			payload: &WelcomePayload{
				Version:      "1.0.0",
				SessionNames: []string{"session-1", "session-2"},
			},
		},
		{
			name:    "AttachPayload",
			msgType: MsgAttach,
			payload: &AttachPayload{
				SessionName: "test-session",
				CreateNew:   true,
				Width:       120,
				Height:      40,
			},
		},
		{
			name:    "AttachedPayload",
			msgType: MsgAttached,
			payload: &AttachedPayload{
				SessionName: "test-session",
				SessionID:   "abc123",
				Width:       120,
				Height:      40,
				WindowCount: 3,
			},
		},
		{
			name:    "SessionListPayload",
			msgType: MsgSessionList,
			payload: &SessionListPayload{
				Sessions: []SessionInfo{
					{Name: "session-1", ID: "id1", WindowCount: 2},
					{Name: "session-2", ID: "id2", WindowCount: 1},
				},
			},
		},
		{
			name:    "ResizePayload",
			msgType: MsgResize,
			payload: &ResizePayload{Width: 100, Height: 50},
		},
		{
			name:    "ErrorPayload",
			msgType: MsgError,
			payload: &ErrorPayload{Code: ErrCodeSessionNotFound, Message: "session not found"},
		},
		{
			name:    "NilPayload",
			msgType: MsgPing,
			payload: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create message
			msg, err := NewMessage(tt.msgType, tt.payload)
			if err != nil {
				t.Fatalf("NewMessage failed: %v", err)
			}

			// Write to buffer
			var buf bytes.Buffer
			if err := WriteMessage(&buf, msg); err != nil {
				t.Fatalf("WriteMessage failed: %v", err)
			}

			// Read back
			readMsg, err := ReadMessage(&buf)
			if err != nil {
				t.Fatalf("ReadMessage failed: %v", err)
			}

			// Verify type
			if readMsg.Type != tt.msgType {
				t.Errorf("Message type mismatch: got %d, want %d", readMsg.Type, tt.msgType)
			}

			// Verify payload can be parsed (if not nil)
			if tt.payload != nil && len(readMsg.Payload) == 0 {
				t.Error("Expected non-empty payload")
			}
		})
	}
}

// TestRawMessage tests raw message encoding (for input/output)
func TestRawMessage(t *testing.T) {
	data := []byte("hello world")
	msg := NewRawMessage(MsgInput, data)

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	readMsg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	if readMsg.Type != MsgInput {
		t.Errorf("Message type mismatch: got %d, want %d", readMsg.Type, MsgInput)
	}

	if !bytes.Equal(readMsg.Payload, data) {
		t.Errorf("Payload mismatch: got %q, want %q", readMsg.Payload, data)
	}
}

// TestSessionManager tests session management operations
func TestSessionManager(t *testing.T) {
	mgr := NewManager()

	// Test creating a session
	cfg := &SessionConfig{
		Term:      "xterm-256color",
		ColorTerm: "truecolor",
		Shell:     "/bin/bash",
	}

	session, err := mgr.CreateSession("test-session", cfg, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	if session.Name != "test-session" {
		t.Errorf("Session name mismatch: got %s, want test-session", session.Name)
	}

	// Test getting session
	retrieved := mgr.GetSession("test-session")
	if retrieved == nil {
		t.Fatal("GetSession returned nil")
	}
	if retrieved.ID != session.ID {
		t.Errorf("Session ID mismatch: got %s, want %s", retrieved.ID, session.ID)
	}

	// Test session count
	if mgr.SessionCount() != 1 {
		t.Errorf("SessionCount mismatch: got %d, want 1", mgr.SessionCount())
	}

	// Test creating duplicate session
	_, err = mgr.CreateSession("test-session", cfg, 80, 24)
	if err == nil {
		t.Error("Expected error when creating duplicate session")
	}

	// Test listing sessions
	sessions := mgr.ListSessions()
	if len(sessions) != 1 {
		t.Errorf("ListSessions count mismatch: got %d, want 1", len(sessions))
	}

	// Test deleting session
	if err := mgr.DeleteSession("test-session"); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	if mgr.SessionCount() != 0 {
		t.Errorf("SessionCount after delete: got %d, want 0", mgr.SessionCount())
	}
}

// TestSessionNameGeneration tests automatic session name generation
func TestSessionNameGeneration(t *testing.T) {
	mgr := NewManager()

	// Generate first name
	name1 := mgr.GenerateSessionName()
	if name1 != "session-0" {
		t.Errorf("First generated name: got %s, want session-0", name1)
	}

	// Create a session with that name
	_, _ = mgr.CreateSession(name1, nil, 80, 24)

	// Generate next name
	name2 := mgr.GenerateSessionName()
	if name2 != "session-1" {
		t.Errorf("Second generated name: got %s, want session-1", name2)
	}
}

// TestGetOrCreateSession tests the get-or-create functionality
func TestGetOrCreateSession(t *testing.T) {
	mgr := NewManager()

	// First call should create
	session1, created, err := mgr.GetOrCreateSession("test", nil, 80, 24)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	if !created {
		t.Error("Expected session to be created")
	}

	// Second call should get existing
	session2, created, err := mgr.GetOrCreateSession("test", nil, 80, 24)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	if created {
		t.Error("Expected to get existing session")
	}
	if session2.ID != session1.ID {
		t.Error("Expected same session to be returned")
	}
}

// TestSessionInfo tests session info generation
func TestSessionInfo(t *testing.T) {
	mgr := NewManager()

	session, _ := mgr.CreateSession("info-test", nil, 100, 50)

	info := session.Info()

	if info.Name != "info-test" {
		t.Errorf("Info name mismatch: got %s, want info-test", info.Name)
	}
	if info.Width != 100 {
		t.Errorf("Info width mismatch: got %d, want 100", info.Width)
	}
	if info.Height != 50 {
		t.Errorf("Info height mismatch: got %d, want 50", info.Height)
	}
	if info.Created == 0 {
		t.Error("Info created time should be set")
	}
}

// TestSessionState tests session state management
func TestSessionState(t *testing.T) {
	session, _ := NewSession("state-test", nil, 80, 24)

	// Initial state should be empty
	state := session.GetState()
	if len(state.Windows) != 0 {
		t.Errorf("Initial windows should be empty, got %d", len(state.Windows))
	}

	// Update state
	newState := &SessionState{
		Name:             "state-test",
		CurrentWorkspace: 2,
		MasterRatio:      0.6,
		Windows: []WindowState{
			{ID: "win-1", Title: "Terminal 1", X: 0, Y: 0, Width: 40, Height: 24},
		},
	}
	session.UpdateState(newState)

	// Verify updated state
	state = session.GetState()
	if state.CurrentWorkspace != 2 {
		t.Errorf("CurrentWorkspace mismatch: got %d, want 2", state.CurrentWorkspace)
	}
	if len(state.Windows) != 1 {
		t.Errorf("Windows count mismatch: got %d, want 1", len(state.Windows))
	}
}

// TestSocketPath tests socket path generation
func TestSocketPath(t *testing.T) {
	path, err := GetSocketPath()
	if err != nil {
		t.Fatalf("GetSocketPath failed: %v", err)
	}

	if path == "" {
		t.Error("GetSocketPath returned empty string")
	}

	// Should contain "tuios" somewhere
	if !bytes.Contains([]byte(path), []byte("tuios")) {
		t.Errorf("Socket path should contain 'tuios': %s", path)
	}
}

// BenchmarkMessageEncode benchmarks message encoding
func BenchmarkMessageEncode(b *testing.B) {
	payload := &HelloPayload{
		Version:   "1.0.0",
		Term:      "xterm-256color",
		ColorTerm: "truecolor",
		Shell:     "/bin/bash",
		Width:     80,
		Height:    24,
	}

	b.ResetTimer()
	for b.Loop() {
		msg, _ := NewMessage(MsgHello, payload)
		var buf bytes.Buffer
		_ = WriteMessage(&buf, msg)
	}
}

// BenchmarkMessageDecode benchmarks message decoding
func BenchmarkMessageDecode(b *testing.B) {
	payload := &HelloPayload{
		Version:   "1.0.0",
		Term:      "xterm-256color",
		ColorTerm: "truecolor",
		Shell:     "/bin/bash",
		Width:     80,
		Height:    24,
	}
	msg, _ := NewMessage(MsgHello, payload)
	var buf bytes.Buffer
	_ = WriteMessage(&buf, msg)
	encoded := buf.Bytes()

	b.ResetTimer()
	for b.Loop() {
		reader := bytes.NewReader(encoded)
		_, _ = ReadMessage(reader)
	}
}
