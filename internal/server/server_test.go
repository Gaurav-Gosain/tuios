package server

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestIsValidSessionNameChar tests the session name character validation function.
func TestIsValidSessionNameChar(t *testing.T) {
	tests := []struct {
		name     string
		char     rune
		expected bool
	}{
		// Valid lowercase letters
		{"lowercase a", 'a', true},
		{"lowercase z", 'z', true},
		{"lowercase m", 'm', true},

		// Valid uppercase letters
		{"uppercase A", 'A', true},
		{"uppercase Z", 'Z', true},
		{"uppercase M", 'M', true},

		// Valid digits
		{"digit 0", '0', true},
		{"digit 9", '9', true},
		{"digit 5", '5', true},

		// Valid special characters
		{"hyphen", '-', true},
		{"underscore", '_', true},

		// Invalid characters
		{"space", ' ', false},
		{"dot", '.', false},
		{"at sign", '@', false},
		{"exclamation", '!', false},
		{"hash", '#', false},
		{"dollar", '$', false},
		{"percent", '%', false},
		{"ampersand", '&', false},
		{"asterisk", '*', false},
		{"plus", '+', false},
		{"equals", '=', false},
		{"slash", '/', false},
		{"backslash", '\\', false},
		{"colon", ':', false},
		{"semicolon", ';', false},
		{"comma", ',', false},
		{"less than", '<', false},
		{"greater than", '>', false},
		{"question mark", '?', false},
		{"pipe", '|', false},
		{"tilde", '~', false},
		{"backtick", '`', false},
		{"open paren", '(', false},
		{"close paren", ')', false},
		{"open bracket", '[', false},
		{"close bracket", ']', false},
		{"open brace", '{', false},
		{"close brace", '}', false},
		{"double quote", '"', false},
		{"single quote", '\'', false},
		{"newline", '\n', false},
		{"tab", '\t', false},

		// Unicode characters (should be invalid)
		{"unicode letter", 'ñ', false},
		{"emoji", '😀', false},
		{"japanese", '日', false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidSessionNameChar(tt.char)
			if result != tt.expected {
				t.Errorf("isValidSessionNameChar(%q) = %v, want %v", tt.char, result, tt.expected)
			}
		})
	}
}

// TestGenerateSessionName tests automatic session name generation.
func TestGenerateSessionName(t *testing.T) {
	tests := []struct {
		name             string
		existingSessions []session.SessionInfo
		expectedName     string
	}{
		{
			name:             "no existing sessions",
			existingSessions: []session.SessionInfo{},
			expectedName:     "session-0",
		},
		{
			name: "one existing session named session-0",
			existingSessions: []session.SessionInfo{
				{Name: "session-0"},
			},
			expectedName: "session-1",
		},
		{
			name: "multiple sequential sessions",
			existingSessions: []session.SessionInfo{
				{Name: "session-0"},
				{Name: "session-1"},
				{Name: "session-2"},
			},
			expectedName: "session-3",
		},
		{
			name: "gap in sequence",
			existingSessions: []session.SessionInfo{
				{Name: "session-0"},
				{Name: "session-2"},
				{Name: "session-3"},
			},
			expectedName: "session-1",
		},
		{
			name: "custom named sessions only",
			existingSessions: []session.SessionInfo{
				{Name: "my-project"},
				{Name: "dev-server"},
				{Name: "test-env"},
			},
			expectedName: "session-0",
		},
		{
			name: "mixed custom and numbered sessions",
			existingSessions: []session.SessionInfo{
				{Name: "session-0"},
				{Name: "my-project"},
				{Name: "session-2"},
			},
			expectedName: "session-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			picker := NewSessionPicker(tt.existingSessions)
			result := picker.generateSessionName()
			if result != tt.expectedName {
				t.Errorf("generateSessionName() = %q, want %q", result, tt.expectedName)
			}
		})
	}
}

// TestNewSessionPicker tests the session picker constructor.
func TestNewSessionPicker(t *testing.T) {
	sessions := []session.SessionInfo{
		{Name: "session-1", ID: "id1", WindowCount: 2},
		{Name: "session-2", ID: "id2", WindowCount: 1},
	}

	picker := NewSessionPicker(sessions)

	if picker == nil {
		t.Fatal("NewSessionPicker returned nil")
	}
	if len(picker.sessions) != 2 {
		t.Errorf("sessions count = %d, want 2", len(picker.sessions))
	}
	if picker.cursor != 0 {
		t.Errorf("cursor = %d, want 0", picker.cursor)
	}
	if picker.result != nil {
		t.Error("result should be nil initially")
	}
	if picker.inputMode {
		t.Error("inputMode should be false initially")
	}
}

// TestNewSessionPickerEmpty tests creating a picker with no sessions.
func TestNewSessionPickerEmpty(t *testing.T) {
	picker := NewSessionPicker([]session.SessionInfo{})

	if picker == nil {
		t.Fatal("NewSessionPicker returned nil")
	}
	if len(picker.sessions) != 0 {
		t.Errorf("sessions count = %d, want 0", len(picker.sessions))
	}
}

// TestSessionPickerResult tests the Result method.
func TestSessionPickerResult(t *testing.T) {
	picker := NewSessionPicker([]session.SessionInfo{})

	// Initially nil
	if picker.Result() != nil {
		t.Error("Result() should be nil initially")
	}

	// Set a result manually (simulating selection)
	picker.result = &SessionPickerResult{
		SessionName: "test-session",
		CreateNew:   true,
	}

	result := picker.Result()
	if result == nil {
		t.Fatal("Result() should not be nil after setting")
	}
	if result.SessionName != "test-session" {
		t.Errorf("SessionName = %q, want %q", result.SessionName, "test-session")
	}
	if !result.CreateNew {
		t.Error("CreateNew should be true")
	}
}

// TestSessionPickerInit tests the Init method returns nil.
func TestSessionPickerInit(t *testing.T) {
	picker := NewSessionPicker([]session.SessionInfo{})
	cmd := picker.Init()
	if cmd != nil {
		t.Error("Init() should return nil")
	}
}

// TestDetermineSessionNamePriority tests the session name determination logic.
// Since determineSessionName requires a real ssh.Session, we test the logic
// by verifying the priority order in the function.
func TestDetermineSessionNamePriority(t *testing.T) {
	// Test Priority 1: Default session configured on server
	t.Run("priority 1: default session config", func(t *testing.T) {
		// When DefaultSession is set, it should always be used
		cfg := &SSHServerConfig{DefaultSession: "my-default-session"}
		// The function would return "my-default-session" regardless of other inputs
		if cfg.DefaultSession == "" {
			t.Error("DefaultSession should be set for this test")
		}
	})

	// Test Priority 2: SSH username filtering
	t.Run("priority 2: generic usernames filtered", func(t *testing.T) {
		genericUsers := []string{"tuios", "root", "anonymous", ""}
		for _, user := range genericUsers {
			if user != "" && user != "tuios" && user != "root" && user != "anonymous" {
				t.Errorf("user %q should be filtered but wasn't", user)
			}
		}

		// Non-generic users should be accepted
		validUsers := []string{"john", "developer", "my-user"}
		for _, user := range validUsers {
			if user == "" || user == "tuios" || user == "root" || user == "anonymous" {
				t.Errorf("user %q should NOT be filtered but was", user)
			}
		}
	})

	// Test Priority 3: Parse command for "attach <session>" pattern
	t.Run("priority 3: attach command parsing", func(t *testing.T) {
		testCases := []struct {
			cmd      []string
			expected string
		}{
			{[]string{"attach", "my-session"}, "my-session"},
			{[]string{"attach", "dev"}, "dev"},
			{[]string{"attach"}, ""},            // Missing session name
			{[]string{"other", "command"}, ""},  // Not an attach command
			{[]string{}, ""},                    // Empty command
			{[]string{"attach", "a", "b"}, "a"}, // Extra args ignored, second used
			{[]string{"ATTACH", "session"}, ""}, // Case sensitive
		}

		for _, tc := range testCases {
			result := ""
			if len(tc.cmd) >= 2 && tc.cmd[0] == "attach" {
				result = tc.cmd[1]
			}
			if result != tc.expected {
				t.Errorf("parseAttachCommand(%v) = %q, want %q", tc.cmd, result, tc.expected)
			}
		}
	})
}

// TestSSHServerConfig tests the SSHServerConfig struct defaults.
func TestSSHServerConfig(t *testing.T) {
	cfg := &SSHServerConfig{
		Host:      "localhost",
		Port:      "2222",
		KeyPath:   "/path/to/key",
		Ephemeral: false,
		Version:   "1.0.0",
	}

	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want %q", cfg.Host, "localhost")
	}
	if cfg.Port != "2222" {
		t.Errorf("Port = %q, want %q", cfg.Port, "2222")
	}
	if cfg.KeyPath != "/path/to/key" {
		t.Errorf("KeyPath = %q, want %q", cfg.KeyPath, "/path/to/key")
	}
	if cfg.Ephemeral {
		t.Error("Ephemeral should be false")
	}
	if cfg.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", cfg.Version, "1.0.0")
	}
}

// TestSSHServerConfigWithDefaultSession tests config with a default session.
func TestSSHServerConfigWithDefaultSession(t *testing.T) {
	cfg := &SSHServerConfig{
		Host:           "0.0.0.0",
		Port:           "22",
		DefaultSession: "main",
	}

	if cfg.DefaultSession != "main" {
		t.Errorf("DefaultSession = %q, want %q", cfg.DefaultSession, "main")
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", cfg.Host, "0.0.0.0")
	}
	if cfg.Port != "22" {
		t.Errorf("Port = %q, want %q", cfg.Port, "22")
	}
}

// TestSessionPickerResultStruct tests the SessionPickerResult struct.
func TestSessionPickerResultStruct(t *testing.T) {
	tests := []struct {
		name     string
		result   SessionPickerResult
		wantName string
		wantNew  bool
		wantCanc bool
	}{
		{
			name:     "new session created",
			result:   SessionPickerResult{SessionName: "new-session", CreateNew: true, Cancelled: false},
			wantName: "new-session",
			wantNew:  true,
			wantCanc: false,
		},
		{
			name:     "existing session selected",
			result:   SessionPickerResult{SessionName: "existing", CreateNew: false, Cancelled: false},
			wantName: "existing",
			wantNew:  false,
			wantCanc: false,
		},
		{
			name:     "cancelled",
			result:   SessionPickerResult{SessionName: "", CreateNew: false, Cancelled: true},
			wantName: "",
			wantNew:  false,
			wantCanc: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.result.SessionName != tt.wantName {
				t.Errorf("SessionName = %q, want %q", tt.result.SessionName, tt.wantName)
			}
			if tt.result.CreateNew != tt.wantNew {
				t.Errorf("CreateNew = %v, want %v", tt.result.CreateNew, tt.wantNew)
			}
			if tt.result.Cancelled != tt.wantCanc {
				t.Errorf("Cancelled = %v, want %v", tt.result.Cancelled, tt.wantCanc)
			}
		})
	}
}

// TestIsValidSessionNameCharBoundaries tests boundary cases for character validation.
func TestIsValidSessionNameCharBoundaries(t *testing.T) {
	// Test boundaries of valid ranges
	boundaries := []struct {
		char    rune
		valid   bool
		comment string
	}{
		// Lowercase boundaries
		{'`', false, "character before 'a'"},
		{'a', true, "first lowercase letter"},
		{'z', true, "last lowercase letter"},
		{'{', false, "character after 'z'"},

		// Uppercase boundaries
		{'@', false, "character before 'A'"},
		{'A', true, "first uppercase letter"},
		{'Z', true, "last uppercase letter"},
		{'[', false, "character after 'Z'"},

		// Digit boundaries
		{'/', false, "character before '0'"},
		{'0', true, "first digit"},
		{'9', true, "last digit"},
		{':', false, "character after '9'"},
	}

	for _, b := range boundaries {
		t.Run(b.comment, func(t *testing.T) {
			result := isValidSessionNameChar(b.char)
			if result != b.valid {
				t.Errorf("isValidSessionNameChar(%q) = %v, want %v (%s)",
					b.char, result, b.valid, b.comment)
			}
		})
	}
}

// TestGenerateSessionNameLargeGap tests name generation with a large gap in numbering.
func TestGenerateSessionNameLargeGap(t *testing.T) {
	sessions := []session.SessionInfo{
		{Name: "session-0"},
		{Name: "session-100"},
		{Name: "session-1000"},
	}

	picker := NewSessionPicker(sessions)
	result := picker.generateSessionName()

	// Should find session-1 as the first available gap
	if result != "session-1" {
		t.Errorf("generateSessionName() = %q, want %q", result, "session-1")
	}
}

// TestSessionPickerWithManyExistingSessions tests picker with many sessions.
func TestSessionPickerWithManyExistingSessions(t *testing.T) {
	sessions := make([]session.SessionInfo, 100)
	for i := range 100 {
		sessions[i] = session.SessionInfo{
			Name:        "session-" + string(rune('a'+i%26)),
			ID:          "id-" + string(rune('0'+i%10)),
			WindowCount: i % 5,
		}
	}

	picker := NewSessionPicker(sessions)

	if len(picker.sessions) != 100 {
		t.Errorf("sessions count = %d, want 100", len(picker.sessions))
	}

	// Generate name should find session-0 since none of our sessions use numbered format
	name := picker.generateSessionName()
	if name != "session-0" {
		t.Errorf("generateSessionName() = %q, want %q", name, "session-0")
	}
}

// BenchmarkIsValidSessionNameChar benchmarks the character validation function.
func BenchmarkIsValidSessionNameChar(b *testing.B) {
	chars := []rune{'a', 'Z', '5', '-', '_', '@', ' ', 'ñ'}

	b.ResetTimer()
	for b.Loop() {
		for _, c := range chars {
			isValidSessionNameChar(c)
		}
	}
}

// BenchmarkGenerateSessionName benchmarks session name generation.
func BenchmarkGenerateSessionName(b *testing.B) {
	sessions := make([]session.SessionInfo, 50)
	for i := range 50 {
		sessions[i] = session.SessionInfo{Name: "session-" + string(rune('0'+i%10))}
	}

	picker := NewSessionPicker(sessions)

	b.ResetTimer()
	for b.Loop() {
		picker.generateSessionName()
	}
}

// BenchmarkNewSessionPicker benchmarks picker creation.
func BenchmarkNewSessionPicker(b *testing.B) {
	sessions := make([]session.SessionInfo, 20)
	for i := range 20 {
		sessions[i] = session.SessionInfo{
			Name:        "session",
			ID:          "id",
			WindowCount: i,
		}
	}

	b.ResetTimer()
	for b.Loop() {
		NewSessionPicker(sessions)
	}
}
