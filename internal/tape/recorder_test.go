package tape

import (
	"strings"
	"testing"
)

func TestRecorder_BasicRecording(t *testing.T) {
	r := NewRecorder()

	// Start recording
	r.Start()
	if !r.IsRecording() {
		t.Error("Expected recording to be active")
	}

	// Record some typing
	r.RecordType("h")
	r.RecordType("e")
	r.RecordType("l")
	r.RecordType("l")
	r.RecordType("o")

	// Record a special key (this should flush typing buffer)
	r.RecordKey("enter")

	// Stop recording
	r.Stop()

	// Check commands
	commands := r.GetCommands()
	if len(commands) != 2 {
		t.Errorf("Expected 2 commands, got %d", len(commands))
	}

	// First command should be Type "hello"
	if commands[0].Type != CommandTypeType {
		t.Errorf("Expected Type command, got %v", commands[0].Type)
	}
	if len(commands[0].Args) == 0 || commands[0].Args[0] != "hello" {
		t.Errorf("Expected args ['hello'], got %v", commands[0].Args)
	}

	// Second command should be Enter
	if commands[1].Type != CommandTypeEnter {
		t.Errorf("Expected Enter command, got %v", commands[1].Type)
	}
}

func TestRecorder_TypeThenStop(t *testing.T) {
	r := NewRecorder()
	r.Start()

	// Record typing without a special key after
	r.RecordType("test")

	// Stop should flush the buffer
	r.Stop()

	commands := r.GetCommands()
	if len(commands) != 1 {
		t.Errorf("Expected 1 command, got %d", len(commands))
	}

	if commands[0].Type != CommandTypeType {
		t.Errorf("Expected Type command, got %v", commands[0].Type)
	}
}

func TestRecorder_SpecialKeys(t *testing.T) {
	r := NewRecorder()
	r.Start()

	r.RecordKey("enter")
	r.RecordKey("backspace")
	r.RecordKey("tab")
	r.RecordKey("up")
	r.RecordKey("down")
	r.RecordKey("left")
	r.RecordKey("right")

	r.Stop()

	commands := r.GetCommands()
	if len(commands) != 7 {
		t.Errorf("Expected 7 commands, got %d", len(commands))
	}

	expected := []CommandType{
		CommandTypeEnter,
		CommandTypeBackspace,
		CommandTypeTab,
		CommandTypeUp,
		CommandTypeDown,
		CommandTypeLeft,
		CommandTypeRight,
	}

	for i, cmd := range commands {
		if cmd.Type != expected[i] {
			t.Errorf("Command %d: expected %v, got %v", i, expected[i], cmd.Type)
		}
	}
}

func TestRecorder_ModifierCombos(t *testing.T) {
	r := NewRecorder()
	r.Start()

	r.RecordKey("ctrl+c")
	r.RecordKey("ctrl+v")
	r.RecordKey("alt+tab")

	r.Stop()

	commands := r.GetCommands()
	if len(commands) != 3 {
		t.Errorf("Expected 3 commands, got %d", len(commands))
	}

	for _, cmd := range commands {
		if cmd.Type != CommandTypeKeyCombo {
			t.Errorf("Expected KeyCombo command, got %v", cmd.Type)
		}
	}
}

func TestRecorder_String(t *testing.T) {
	r := NewRecorder()
	r.Start()

	r.RecordType("echo hello")
	r.RecordKey("enter")

	r.Stop()

	output := r.String("Test Recording")

	// Should contain header
	if !strings.Contains(output, "# Test Recording") {
		t.Error("Expected header in output")
	}

	// Should contain Type command
	if !strings.Contains(output, `Type "echo hello"`) {
		t.Errorf("Expected Type command in output, got: %s", output)
	}

	// Should contain Enter command
	if !strings.Contains(output, "Enter") {
		t.Error("Expected Enter command in output")
	}
}

func TestRecorder_Clear(t *testing.T) {
	r := NewRecorder()
	r.Start()

	r.RecordType("test")
	r.RecordKey("enter")

	r.Clear()

	if len(r.GetCommands()) != 0 {
		t.Error("Expected empty commands after clear")
	}
}

func TestRecorder_NotRecording(t *testing.T) {
	r := NewRecorder()

	// Try to record without starting
	r.RecordType("test")
	r.RecordKey("enter")

	if len(r.GetCommands()) != 0 {
		t.Error("Should not record when not enabled")
	}
}

func TestRecorder_CommandCount(t *testing.T) {
	r := NewRecorder()
	r.Start()

	r.RecordType("a")
	r.RecordType("b")
	r.RecordType("c")
	r.RecordKey("enter") // Flushes "abc"
	r.RecordKey("backspace")

	r.Stop()

	if r.CommandCount() != 3 { // Type "abc", Enter, Backspace
		t.Errorf("Expected 3 commands, got %d", r.CommandCount())
	}
}

func TestRecorder_Stats(t *testing.T) {
	r := NewRecorder()
	r.Start()

	stats := r.GetStats()
	if !stats.IsRecording {
		t.Error("Expected IsRecording to be true")
	}

	r.Stop()

	stats = r.GetStats()
	if stats.IsRecording {
		t.Error("Expected IsRecording to be false")
	}
}
