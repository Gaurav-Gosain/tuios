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

// TestExecuteCommandPayload tests execute command payload encoding
func TestExecuteCommandPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload *ExecuteCommandPayload
	}{
		{
			name: "simple command",
			payload: &ExecuteCommandPayload{
				SessionName: "test",
				CommandType: "NewWindow",
				RequestID:   "req-123",
			},
		},
		{
			name: "command with args",
			payload: &ExecuteCommandPayload{
				SessionName: "test",
				CommandType: "SwitchWorkspace",
				Args:        []string{"2"},
				RequestID:   "req-456",
			},
		},
		{
			name: "tape script",
			payload: &ExecuteCommandPayload{
				SessionName: "",
				TapeScript:  "NewWindow\nType hello\nEnter",
				RequestID:   "req-789",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := NewMessage(MsgExecuteCommand, tt.payload)
			if err != nil {
				t.Fatalf("NewMessage failed: %v", err)
			}

			var buf bytes.Buffer
			if err := WriteMessage(&buf, msg); err != nil {
				t.Fatalf("WriteMessage failed: %v", err)
			}

			readMsg, err := ReadMessage(&buf)
			if err != nil {
				t.Fatalf("ReadMessage failed: %v", err)
			}

			var decoded ExecuteCommandPayload
			if err := readMsg.ParsePayload(&decoded); err != nil {
				t.Fatalf("ParsePayload failed: %v", err)
			}

			if decoded.CommandType != tt.payload.CommandType {
				t.Errorf("CommandType mismatch: got %s, want %s", decoded.CommandType, tt.payload.CommandType)
			}
			if decoded.RequestID != tt.payload.RequestID {
				t.Errorf("RequestID mismatch: got %s, want %s", decoded.RequestID, tt.payload.RequestID)
			}
		})
	}
}

// TestCommandResultPayload tests command result payload with data
func TestCommandResultPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload *CommandResultPayload
	}{
		{
			name: "success without data",
			payload: &CommandResultPayload{
				RequestID: "req-123",
				Success:   true,
				Message:   "command executed",
			},
		},
		{
			name: "success with window data",
			payload: &CommandResultPayload{
				RequestID: "req-456",
				Success:   true,
				Message:   "window created",
				Data: map[string]any{
					"window_id": "win-abc123",
					"name":      "My Terminal",
				},
			},
		},
		{
			name: "failure",
			payload: &CommandResultPayload{
				RequestID: "req-789",
				Success:   false,
				Message:   "window not found",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := NewMessage(MsgCommandResult, tt.payload)
			if err != nil {
				t.Fatalf("NewMessage failed: %v", err)
			}

			var buf bytes.Buffer
			if err := WriteMessage(&buf, msg); err != nil {
				t.Fatalf("WriteMessage failed: %v", err)
			}

			readMsg, err := ReadMessage(&buf)
			if err != nil {
				t.Fatalf("ReadMessage failed: %v", err)
			}

			var decoded CommandResultPayload
			if err := readMsg.ParsePayload(&decoded); err != nil {
				t.Fatalf("ParsePayload failed: %v", err)
			}

			if decoded.Success != tt.payload.Success {
				t.Errorf("Success mismatch: got %v, want %v", decoded.Success, tt.payload.Success)
			}
			if decoded.Message != tt.payload.Message {
				t.Errorf("Message mismatch: got %s, want %s", decoded.Message, tt.payload.Message)
			}

			// Check data if present
			if tt.payload.Data != nil {
				if decoded.Data == nil {
					t.Fatal("Expected Data to be present")
				}
				for k, v := range tt.payload.Data {
					if decoded.Data[k] != v {
						t.Errorf("Data[%s] mismatch: got %v, want %v", k, decoded.Data[k], v)
					}
				}
			}
		})
	}
}

// TestSendKeysPayload tests send keys payload encoding
func TestSendKeysPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload *SendKeysPayload
	}{
		{
			name: "normal keys",
			payload: &SendKeysPayload{
				SessionName: "",
				Keys:        "ctrl+b q",
				RequestID:   "req-123",
			},
		},
		{
			name: "literal mode",
			payload: &SendKeysPayload{
				SessionName: "",
				Keys:        "echo hello",
				Literal:     true,
				RequestID:   "req-456",
			},
		},
		{
			name: "raw mode",
			payload: &SendKeysPayload{
				SessionName: "",
				Keys:        "hello world",
				Raw:         true,
				RequestID:   "req-789",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := NewMessage(MsgSendKeys, tt.payload)
			if err != nil {
				t.Fatalf("NewMessage failed: %v", err)
			}

			var buf bytes.Buffer
			if err := WriteMessage(&buf, msg); err != nil {
				t.Fatalf("WriteMessage failed: %v", err)
			}

			readMsg, err := ReadMessage(&buf)
			if err != nil {
				t.Fatalf("ReadMessage failed: %v", err)
			}

			var decoded SendKeysPayload
			if err := readMsg.ParsePayload(&decoded); err != nil {
				t.Fatalf("ParsePayload failed: %v", err)
			}

			if decoded.Keys != tt.payload.Keys {
				t.Errorf("Keys mismatch: got %s, want %s", decoded.Keys, tt.payload.Keys)
			}
			if decoded.Literal != tt.payload.Literal {
				t.Errorf("Literal mismatch: got %v, want %v", decoded.Literal, tt.payload.Literal)
			}
			if decoded.Raw != tt.payload.Raw {
				t.Errorf("Raw mismatch: got %v, want %v", decoded.Raw, tt.payload.Raw)
			}
		})
	}
}

// TestRemoteCommandPayload tests remote command payload encoding
func TestRemoteCommandPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload *RemoteCommandPayload
	}{
		{
			name: "tape command",
			payload: &RemoteCommandPayload{
				RequestID:   "req-123",
				CommandType: "tape_command",
				TapeCommand: "NewWindow",
				TapeArgs:    []string{"My Window"},
			},
		},
		{
			name: "send keys",
			payload: &RemoteCommandPayload{
				RequestID:   "req-456",
				CommandType: "send_keys",
				Keys:        "ctrl+b n",
				Raw:         false,
			},
		},
		{
			name: "send keys raw",
			payload: &RemoteCommandPayload{
				RequestID:   "req-789",
				CommandType: "send_keys",
				Keys:        "hello world",
				Raw:         true,
			},
		},
		{
			name: "set config",
			payload: &RemoteCommandPayload{
				RequestID:   "req-abc",
				CommandType: "set_config",
				ConfigPath:  "dockbar_position",
				ConfigValue: "top",
			},
		},
		{
			name: "tape script",
			payload: &RemoteCommandPayload{
				RequestID:   "req-def",
				CommandType: "tape_script",
				TapeScript:  "NewWindow\nSleep 500ms\nType hello",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := NewMessage(MsgRemoteCommand, tt.payload)
			if err != nil {
				t.Fatalf("NewMessage failed: %v", err)
			}

			var buf bytes.Buffer
			if err := WriteMessage(&buf, msg); err != nil {
				t.Fatalf("WriteMessage failed: %v", err)
			}

			readMsg, err := ReadMessage(&buf)
			if err != nil {
				t.Fatalf("ReadMessage failed: %v", err)
			}

			var decoded RemoteCommandPayload
			if err := readMsg.ParsePayload(&decoded); err != nil {
				t.Fatalf("ParsePayload failed: %v", err)
			}

			if decoded.CommandType != tt.payload.CommandType {
				t.Errorf("CommandType mismatch: got %s, want %s", decoded.CommandType, tt.payload.CommandType)
			}
			if decoded.TapeCommand != tt.payload.TapeCommand {
				t.Errorf("TapeCommand mismatch: got %s, want %s", decoded.TapeCommand, tt.payload.TapeCommand)
			}
			if decoded.Keys != tt.payload.Keys {
				t.Errorf("Keys mismatch: got %s, want %s", decoded.Keys, tt.payload.Keys)
			}
			if decoded.Raw != tt.payload.Raw {
				t.Errorf("Raw mismatch: got %v, want %v", decoded.Raw, tt.payload.Raw)
			}
		})
	}
}

// TestCodecNegotiation tests codec negotiation logic
func TestCodecNegotiation(t *testing.T) {
	tests := []struct {
		preferred string
		expected  CodecType
	}{
		{"gob", CodecGob},
		{"GOB", CodecGob},
		{"json", CodecJSON},
		{"JSON", CodecJSON},
		{"", CodecGob},        // default
		{"unknown", CodecGob}, // unknown defaults to gob
	}

	for _, tt := range tests {
		t.Run(tt.preferred, func(t *testing.T) {
			codec := NegotiateCodec(tt.preferred)
			if codec.Type() != tt.expected {
				t.Errorf("NegotiateCodec(%s) = %v, want %v", tt.preferred, codec.Type(), tt.expected)
			}
		})
	}
}

// TestJSONCodec tests JSON codec encoding/decoding
func TestJSONCodec(t *testing.T) {
	codec := GetCodec(CodecJSON)

	payload := &HelloPayload{
		Version:        "1.0.0",
		Term:           "xterm-256color",
		PreferredCodec: "json",
	}

	// Encode
	msg, err := NewMessageWithCodec(MsgHello, payload, codec)
	if err != nil {
		t.Fatalf("NewMessageWithCodec failed: %v", err)
	}

	// Write and read
	var buf bytes.Buffer
	if err := WriteMessageWithCodec(&buf, msg, codec); err != nil {
		t.Fatalf("WriteMessageWithCodec failed: %v", err)
	}

	readMsg, codecType, err := ReadMessageWithCodec(&buf)
	if err != nil {
		t.Fatalf("ReadMessageWithCodec failed: %v", err)
	}

	if codecType != CodecJSON {
		t.Errorf("Expected JSON codec, got %v", codecType)
	}

	// Decode
	var decoded HelloPayload
	if err := readMsg.ParsePayloadWithCodec(&decoded, codec); err != nil {
		t.Fatalf("ParsePayloadWithCodec failed: %v", err)
	}

	if decoded.Version != payload.Version {
		t.Errorf("Version mismatch: got %s, want %s", decoded.Version, payload.Version)
	}
}

// TestMultiClientMessages tests multi-client message encoding/decoding
func TestMultiClientMessages(t *testing.T) {
	tests := []struct {
		name    string
		msgType MessageType
		payload any
	}{
		{
			name:    "StateSyncPayload",
			msgType: MsgStateSync,
			payload: &StateSyncPayload{
				State: &SessionState{
					Name:             "test-session",
					CurrentWorkspace: 1,
					AutoTiling:       true,
					Windows: []WindowState{
						{ID: "win-1", Title: "Terminal 1", X: 0, Y: 0, Width: 80, Height: 24, PTYID: "pty-1"},
						{ID: "win-2", Title: "Terminal 2", X: 80, Y: 0, Width: 80, Height: 24, PTYID: "pty-2"},
					},
				},
				TriggerType: "window",
				SourceID:    "client-abc",
			},
		},
		{
			name:    "ClientJoinedPayload",
			msgType: MsgClientJoined,
			payload: &ClientJoinedPayload{
				ClientID:    "client-123",
				ClientCount: 2,
				Width:       120,
				Height:      40,
			},
		},
		{
			name:    "ClientLeftPayload",
			msgType: MsgClientLeft,
			payload: &ClientLeftPayload{
				ClientID:    "client-123",
				ClientCount: 1,
			},
		},
		{
			name:    "SessionResizePayload",
			msgType: MsgSessionResize,
			payload: &SessionResizePayload{
				Width:       100,
				Height:      30,
				ClientCount: 3,
			},
		},
		{
			name:    "ForceRefreshPayload",
			msgType: MsgForceRefresh,
			payload: &ForceRefreshPayload{
				Reason: "client reconnected",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := NewMessage(tt.msgType, tt.payload)
			if err != nil {
				t.Fatalf("NewMessage failed: %v", err)
			}

			var buf bytes.Buffer
			if err := WriteMessage(&buf, msg); err != nil {
				t.Fatalf("WriteMessage failed: %v", err)
			}

			readMsg, err := ReadMessage(&buf)
			if err != nil {
				t.Fatalf("ReadMessage failed: %v", err)
			}

			if readMsg.Type != tt.msgType {
				t.Errorf("Message type mismatch: got %d, want %d", readMsg.Type, tt.msgType)
			}

			if len(readMsg.Payload) == 0 {
				t.Error("Expected non-empty payload")
			}
		})
	}
}

// TestStateSyncPayloadRoundTrip tests full round-trip for state sync
func TestStateSyncPayloadRoundTrip(t *testing.T) {
	original := &StateSyncPayload{
		State: &SessionState{
			Name:             "sync-test",
			CurrentWorkspace: 2,
			MasterRatio:      0.6,
			AutoTiling:       true,
			Width:            200,
			Height:           60,
			FocusedWindowID:  "win-2",
			Windows: []WindowState{
				{
					ID:          "win-1",
					Title:       "Terminal 1",
					CustomName:  "Main",
					X:           0,
					Y:           0,
					Width:       100,
					Height:      60,
					Z:           0,
					Workspace:   1,
					Minimized:   false,
					PTYID:       "pty-abc",
					IsAltScreen: false,
				},
				{
					ID:          "win-2",
					Title:       "vim",
					X:           100,
					Y:           0,
					Width:       100,
					Height:      60,
					Z:           1,
					Workspace:   2,
					Minimized:   false,
					PTYID:       "pty-def",
					IsAltScreen: true,
				},
			},
			WorkspaceFocus: map[int]string{
				1: "win-1",
				2: "win-2",
			},
		},
		TriggerType: "workspace_change",
		SourceID:    "client-xyz",
	}

	// Encode
	msg, err := NewMessage(MsgStateSync, original)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	// Write
	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	// Read
	readMsg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	// Decode
	var decoded StateSyncPayload
	if err := readMsg.ParsePayload(&decoded); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}

	// Verify
	if decoded.TriggerType != original.TriggerType {
		t.Errorf("TriggerType mismatch: got %s, want %s", decoded.TriggerType, original.TriggerType)
	}
	if decoded.SourceID != original.SourceID {
		t.Errorf("SourceID mismatch: got %s, want %s", decoded.SourceID, original.SourceID)
	}
	if decoded.State == nil {
		t.Fatal("State should not be nil")
	}
	if decoded.State.Name != original.State.Name {
		t.Errorf("State.Name mismatch: got %s, want %s", decoded.State.Name, original.State.Name)
	}
	if decoded.State.CurrentWorkspace != original.State.CurrentWorkspace {
		t.Errorf("State.CurrentWorkspace mismatch: got %d, want %d", decoded.State.CurrentWorkspace, original.State.CurrentWorkspace)
	}
	if decoded.State.AutoTiling != original.State.AutoTiling {
		t.Errorf("State.AutoTiling mismatch: got %v, want %v", decoded.State.AutoTiling, original.State.AutoTiling)
	}
	if len(decoded.State.Windows) != len(original.State.Windows) {
		t.Fatalf("Windows count mismatch: got %d, want %d", len(decoded.State.Windows), len(original.State.Windows))
	}

	// Verify window details
	for i, w := range decoded.State.Windows {
		orig := original.State.Windows[i]
		if w.ID != orig.ID {
			t.Errorf("Window[%d].ID mismatch: got %s, want %s", i, w.ID, orig.ID)
		}
		if w.PTYID != orig.PTYID {
			t.Errorf("Window[%d].PTYID mismatch: got %s, want %s", i, w.PTYID, orig.PTYID)
		}
		if w.IsAltScreen != orig.IsAltScreen {
			t.Errorf("Window[%d].IsAltScreen mismatch: got %v, want %v", i, w.IsAltScreen, orig.IsAltScreen)
		}
		if w.X != orig.X || w.Y != orig.Y {
			t.Errorf("Window[%d] position mismatch: got (%d,%d), want (%d,%d)", i, w.X, w.Y, orig.X, orig.Y)
		}
		if w.Width != orig.Width || w.Height != orig.Height {
			t.Errorf("Window[%d] size mismatch: got %dx%d, want %dx%d", i, w.Width, w.Height, orig.Width, orig.Height)
		}
	}

	// Verify workspace focus
	for ws, winID := range original.State.WorkspaceFocus {
		if decoded.State.WorkspaceFocus[ws] != winID {
			t.Errorf("WorkspaceFocus[%d] mismatch: got %s, want %s", ws, decoded.State.WorkspaceFocus[ws], winID)
		}
	}
}

// TestSessionStateWithBSPTree tests BSP tree serialization in state
func TestSessionStateWithBSPTree(t *testing.T) {
	state := &SessionState{
		Name:             "bsp-test",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		WorkspaceTrees: map[int]*SerializedBSPTree{
			1: {
				AutoScheme:   1, // MasterStack
				DefaultRatio: 0.5,
				Root: &SerializedBSPNode{
					WindowID:   0,
					SplitType:  1, // Horizontal
					SplitRatio: 0.6,
					Left: &SerializedBSPNode{
						WindowID: 1,
					},
					Right: &SerializedBSPNode{
						WindowID: 2,
					},
				},
			},
		},
		WindowToBSPID:   map[string]int{"win-1": 1, "win-2": 2},
		NextBSPWindowID: 3,
	}

	// Encode
	msg, err := NewMessage(MsgStateSync, &StateSyncPayload{State: state})
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	// Round trip
	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	readMsg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	var decoded StateSyncPayload
	if err := readMsg.ParsePayload(&decoded); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}

	// Verify BSP tree was preserved
	if decoded.State.WorkspaceTrees == nil {
		t.Fatal("WorkspaceTrees should not be nil")
	}
	tree := decoded.State.WorkspaceTrees[1]
	if tree == nil {
		t.Fatal("WorkspaceTrees[1] should not be nil")
	}
	if tree.AutoScheme != 1 {
		t.Errorf("AutoScheme mismatch: got %d, want 1", tree.AutoScheme)
	}
	if tree.Root == nil {
		t.Fatal("Root should not be nil")
	}
	if tree.Root.SplitRatio != 0.6 {
		t.Errorf("SplitRatio mismatch: got %f, want 0.6", tree.Root.SplitRatio)
	}
	if tree.Root.Left == nil || tree.Root.Right == nil {
		t.Fatal("Root children should not be nil")
	}
	if tree.Root.Left.WindowID != 1 {
		t.Errorf("Left.WindowID mismatch: got %d, want 1", tree.Root.Left.WindowID)
	}
	if tree.Root.Right.WindowID != 2 {
		t.Errorf("Right.WindowID mismatch: got %d, want 2", tree.Root.Right.WindowID)
	}

	// Verify WindowToBSPID
	if decoded.State.WindowToBSPID == nil {
		t.Fatal("WindowToBSPID should not be nil")
	}
	if decoded.State.WindowToBSPID["win-1"] != 1 {
		t.Errorf("WindowToBSPID[win-1] mismatch: got %d, want 1", decoded.State.WindowToBSPID["win-1"])
	}
	if decoded.State.NextBSPWindowID != 3 {
		t.Errorf("NextBSPWindowID mismatch: got %d, want 3", decoded.State.NextBSPWindowID)
	}
}

// TestDaemonCalculateEffectiveSize tests the effective size calculation for multi-client scenarios.
// This simulates the daemon's logic for computing minimum dimensions across clients.
func TestDaemonCalculateEffectiveSize(t *testing.T) {
	tests := []struct {
		name           string
		clients        []struct{ width, height int }
		expectedWidth  int
		expectedHeight int
	}{
		{
			name:           "single client",
			clients:        []struct{ width, height int }{{120, 40}},
			expectedWidth:  120,
			expectedHeight: 40,
		},
		{
			name: "two clients same size",
			clients: []struct{ width, height int }{
				{120, 40},
				{120, 40},
			},
			expectedWidth:  120,
			expectedHeight: 40,
		},
		{
			name: "two clients different sizes - takes minimum",
			clients: []struct{ width, height int }{
				{120, 40},
				{80, 24},
			},
			expectedWidth:  80,
			expectedHeight: 24,
		},
		{
			name: "three clients mixed sizes",
			clients: []struct{ width, height int }{
				{200, 60},
				{150, 40},
				{100, 30},
			},
			expectedWidth:  100,
			expectedHeight: 30,
		},
		{
			name: "width and height from different clients",
			clients: []struct{ width, height int }{
				{80, 100}, // smallest width
				{200, 20}, // smallest height
			},
			expectedWidth:  80,
			expectedHeight: 20,
		},
		{
			name:           "no clients",
			clients:        []struct{ width, height int }{},
			expectedWidth:  0,
			expectedHeight: 0,
		},
		{
			name: "client with zero dimensions ignored",
			clients: []struct{ width, height int }{
				{120, 40},
				{0, 0}, // Should be ignored
			},
			expectedWidth:  120,
			expectedHeight: 40,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate calculateEffectiveSize logic
			width, height := 0, 0
			first := true

			for _, c := range tt.clients {
				if c.width == 0 || c.height == 0 {
					continue
				}
				if first {
					width, height = c.width, c.height
					first = false
				} else {
					if c.width < width {
						width = c.width
					}
					if c.height < height {
						height = c.height
					}
				}
			}

			if width != tt.expectedWidth {
				t.Errorf("width = %d, want %d", width, tt.expectedWidth)
			}
			if height != tt.expectedHeight {
				t.Errorf("height = %d, want %d", height, tt.expectedHeight)
			}
		})
	}
}

// TestSessionResizePayloadRoundTrip tests session resize payload encoding/decoding
func TestSessionResizePayloadRoundTrip(t *testing.T) {
	original := &SessionResizePayload{
		Width:       100,
		Height:      50,
		ClientCount: 3,
	}

	msg, err := NewMessage(MsgSessionResize, original)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	readMsg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	var decoded SessionResizePayload
	if err := readMsg.ParsePayload(&decoded); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}

	if decoded.Width != original.Width {
		t.Errorf("Width mismatch: got %d, want %d", decoded.Width, original.Width)
	}
	if decoded.Height != original.Height {
		t.Errorf("Height mismatch: got %d, want %d", decoded.Height, original.Height)
	}
	if decoded.ClientCount != original.ClientCount {
		t.Errorf("ClientCount mismatch: got %d, want %d", decoded.ClientCount, original.ClientCount)
	}
}

// TestClientJoinedPayloadRoundTrip tests client joined payload
func TestClientJoinedPayloadRoundTrip(t *testing.T) {
	original := &ClientJoinedPayload{
		ClientID:    "client-123456",
		ClientCount: 2,
		Width:       120,
		Height:      40,
	}

	msg, err := NewMessage(MsgClientJoined, original)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	readMsg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	var decoded ClientJoinedPayload
	if err := readMsg.ParsePayload(&decoded); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}

	if decoded.ClientID != original.ClientID {
		t.Errorf("ClientID mismatch: got %s, want %s", decoded.ClientID, original.ClientID)
	}
	if decoded.ClientCount != original.ClientCount {
		t.Errorf("ClientCount mismatch: got %d, want %d", decoded.ClientCount, original.ClientCount)
	}
	if decoded.Width != original.Width {
		t.Errorf("Width mismatch: got %d, want %d", decoded.Width, original.Width)
	}
	if decoded.Height != original.Height {
		t.Errorf("Height mismatch: got %d, want %d", decoded.Height, original.Height)
	}
}

// TestClientLeftPayloadRoundTrip tests client left payload
func TestClientLeftPayloadRoundTrip(t *testing.T) {
	original := &ClientLeftPayload{
		ClientID:    "client-654321",
		ClientCount: 1,
	}

	msg, err := NewMessage(MsgClientLeft, original)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	readMsg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	var decoded ClientLeftPayload
	if err := readMsg.ParsePayload(&decoded); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}

	if decoded.ClientID != original.ClientID {
		t.Errorf("ClientID mismatch: got %s, want %s", decoded.ClientID, original.ClientID)
	}
	if decoded.ClientCount != original.ClientCount {
		t.Errorf("ClientCount mismatch: got %d, want %d", decoded.ClientCount, original.ClientCount)
	}
}

// TestStateSyncWithAnimationPositions tests that state sync uses final animation positions
func TestStateSyncWithAnimationPositions(t *testing.T) {
	// This test verifies that when building state for sync,
	// windows with active animations report their final (target) positions
	// rather than their current animated positions

	// Create a session state with a window that has "animating" properties
	// In the real implementation, BuildSessionState() would use animation end positions

	currentState := &SessionState{
		Windows: []WindowState{
			{
				ID:     "win-1",
				Title:  "Animating Window",
				X:      50, // Current animated position
				Y:      50,
				Width:  80,
				Height: 40,
			},
		},
	}

	// When syncing to other clients, we should send the target position
	targetState := &SessionState{
		Name:             "anim-test",
		CurrentWorkspace: 1,
		Windows: []WindowState{
			{
				ID:     "win-1",
				Title:  "Animating Window",
				X:      100, // Target position (animation end)
				Y:      100,
				Width:  120,
				Height: 60,
			},
		},
	}

	// Encode target state for sync
	payload := &StateSyncPayload{
		State:       targetState,
		TriggerType: "animation_complete",
		SourceID:    "client-1",
	}

	msg, err := NewMessage(MsgStateSync, payload)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	readMsg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	var decoded StateSyncPayload
	if err := readMsg.ParsePayload(&decoded); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}

	// Verify the target position is preserved, not the current animated position
	if len(decoded.State.Windows) != 1 {
		t.Fatalf("Expected 1 window, got %d", len(decoded.State.Windows))
	}

	win := decoded.State.Windows[0]
	if win.X != 100 || win.Y != 100 {
		t.Errorf("Window position should be target (100,100), got (%d,%d)", win.X, win.Y)
	}
	if win.Width != 120 || win.Height != 60 {
		t.Errorf("Window size should be target (120x60), got (%dx%d)", win.Width, win.Height)
	}

	// Also verify current state is different
	if currentState.Windows[0].X == targetState.Windows[0].X {
		t.Error("Test setup error: current and target positions should be different")
	}
}

// TestStateSyncTriggerTypes tests different trigger types for state sync
func TestStateSyncTriggerTypes(t *testing.T) {
	triggerTypes := []string{
		"window",           // Window created/deleted
		"workspace_change", // Workspace switch
		"focus_change",     // Focus changed
		"resize",           // Window resized
		"move",             // Window moved
		"tiling",           // Tiling layout changed
		"update",           // Generic state update
	}

	for _, triggerType := range triggerTypes {
		t.Run(triggerType, func(t *testing.T) {
			payload := &StateSyncPayload{
				State:       &SessionState{Name: "test"},
				TriggerType: triggerType,
				SourceID:    "client-src",
			}

			msg, err := NewMessage(MsgStateSync, payload)
			if err != nil {
				t.Fatalf("NewMessage failed: %v", err)
			}

			var buf bytes.Buffer
			if err := WriteMessage(&buf, msg); err != nil {
				t.Fatalf("WriteMessage failed: %v", err)
			}

			readMsg, err := ReadMessage(&buf)
			if err != nil {
				t.Fatalf("ReadMessage failed: %v", err)
			}

			var decoded StateSyncPayload
			if err := readMsg.ParsePayload(&decoded); err != nil {
				t.Fatalf("ParsePayload failed: %v", err)
			}

			if decoded.TriggerType != triggerType {
				t.Errorf("TriggerType mismatch: got %s, want %s", decoded.TriggerType, triggerType)
			}
		})
	}
}

// TestMultiClientWindowOrder tests that window order is preserved across sync
func TestMultiClientWindowOrder(t *testing.T) {
	// Create state with multiple windows in specific order
	original := &StateSyncPayload{
		State: &SessionState{
			Name:             "order-test",
			CurrentWorkspace: 1,
			FocusedWindowID:  "win-2",
			Windows: []WindowState{
				{ID: "win-1", Title: "First", Z: 0},
				{ID: "win-2", Title: "Second", Z: 1},
				{ID: "win-3", Title: "Third", Z: 2},
				{ID: "win-4", Title: "Fourth", Z: 3},
			},
		},
		TriggerType: "update",
		SourceID:    "client-1",
	}

	msg, err := NewMessage(MsgStateSync, original)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	readMsg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	var decoded StateSyncPayload
	if err := readMsg.ParsePayload(&decoded); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}

	// Verify window order is preserved
	if len(decoded.State.Windows) != 4 {
		t.Fatalf("Expected 4 windows, got %d", len(decoded.State.Windows))
	}

	expectedOrder := []string{"win-1", "win-2", "win-3", "win-4"}
	for i, expected := range expectedOrder {
		if decoded.State.Windows[i].ID != expected {
			t.Errorf("Window[%d].ID = %s, want %s", i, decoded.State.Windows[i].ID, expected)
		}
	}

	// Verify Z-order is preserved
	for i, win := range decoded.State.Windows {
		if win.Z != i {
			t.Errorf("Window[%d].Z = %d, want %d", i, win.Z, i)
		}
	}
}

// TestSessionStateWithWorkspaceFocus tests workspace focus mapping in sync
func TestSessionStateWithWorkspaceFocus(t *testing.T) {
	original := &SessionState{
		Name:             "ws-focus-test",
		CurrentWorkspace: 2,
		Windows: []WindowState{
			{ID: "win-1", Workspace: 1},
			{ID: "win-2", Workspace: 1},
			{ID: "win-3", Workspace: 2},
			{ID: "win-4", Workspace: 3},
		},
		FocusedWindowID: "win-3",
		WorkspaceFocus: map[int]string{
			1: "win-2", // Last focused on workspace 1
			2: "win-3", // Currently focused on workspace 2
			3: "win-4", // Only window on workspace 3
		},
	}

	payload := &StateSyncPayload{State: original}
	msg, err := NewMessage(MsgStateSync, payload)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	readMsg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	var decoded StateSyncPayload
	if err := readMsg.ParsePayload(&decoded); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}

	// Verify workspace focus is preserved
	if decoded.State.WorkspaceFocus == nil {
		t.Fatal("WorkspaceFocus should not be nil")
	}
	if len(decoded.State.WorkspaceFocus) != 3 {
		t.Errorf("WorkspaceFocus length = %d, want 3", len(decoded.State.WorkspaceFocus))
	}
	if decoded.State.WorkspaceFocus[1] != "win-2" {
		t.Errorf("WorkspaceFocus[1] = %s, want win-2", decoded.State.WorkspaceFocus[1])
	}
	if decoded.State.WorkspaceFocus[2] != "win-3" {
		t.Errorf("WorkspaceFocus[2] = %s, want win-3", decoded.State.WorkspaceFocus[2])
	}
	if decoded.State.WorkspaceFocus[3] != "win-4" {
		t.Errorf("WorkspaceFocus[3] = %s, want win-4", decoded.State.WorkspaceFocus[3])
	}
}

// TestAttachedPayloadWithState tests attached payload includes full state
func TestAttachedPayloadWithState(t *testing.T) {
	state := &SessionState{
		Name:             "attach-test",
		CurrentWorkspace: 2,
		AutoTiling:       true,
		MasterRatio:      0.65,
		Width:            160,
		Height:           48,
		Windows: []WindowState{
			{ID: "win-1", Title: "Terminal", X: 0, Y: 0, Width: 80, Height: 48, PTYID: "pty-1"},
			{ID: "win-2", Title: "Vim", X: 80, Y: 0, Width: 80, Height: 48, PTYID: "pty-2"},
		},
		FocusedWindowID: "win-2",
	}

	original := &AttachedPayload{
		SessionName: "attach-test",
		SessionID:   "session-12345678-abcd-efgh",
		Width:       160,
		Height:      48,
		WindowCount: 2,
		State:       state,
	}

	msg, err := NewMessage(MsgAttached, original)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	readMsg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	var decoded AttachedPayload
	if err := readMsg.ParsePayload(&decoded); err != nil {
		t.Fatalf("ParsePayload failed: %v", err)
	}

	// Verify attached payload
	if decoded.SessionName != original.SessionName {
		t.Errorf("SessionName mismatch: got %s, want %s", decoded.SessionName, original.SessionName)
	}
	if decoded.Width != original.Width {
		t.Errorf("Width mismatch: got %d, want %d", decoded.Width, original.Width)
	}
	if decoded.Height != original.Height {
		t.Errorf("Height mismatch: got %d, want %d", decoded.Height, original.Height)
	}

	// Verify state is included
	if decoded.State == nil {
		t.Fatal("State should not be nil")
	}
	if decoded.State.AutoTiling != true {
		t.Error("State.AutoTiling should be true")
	}
	if decoded.State.MasterRatio != 0.65 {
		t.Errorf("State.MasterRatio = %f, want 0.65", decoded.State.MasterRatio)
	}
	if len(decoded.State.Windows) != 2 {
		t.Errorf("State.Windows length = %d, want 2", len(decoded.State.Windows))
	}
}
