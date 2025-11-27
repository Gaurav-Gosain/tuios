package tape

import (
	"testing"
	"time"
)

func TestParserBasicCommands(t *testing.T) {
	input := `Type "hello"
Enter
Sleep 500ms
Space`

	commands, errors := ParseFile(input)

	if len(errors) > 0 {
		t.Errorf("Unexpected parse errors: %v", errors)
	}

	if len(commands) != 4 {
		t.Errorf("Expected 4 commands, got %d", len(commands))
	}

	// Check types
	expectedTypes := []CommandType{
		CommandTypeType,
		CommandTypeEnter,
		CommandTypeSleep,
		CommandTypeSpace,
	}

	for i, expectedType := range expectedTypes {
		if commands[i].Type != expectedType {
			t.Errorf("Command %d: expected %v, got %v", i, expectedType, commands[i].Type)
		}
	}
}

func TestParserTypeCommand(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectedArg string
	}{
		{
			name:        "Simple text",
			input:       `Type "hello"`,
			expectedArg: "hello",
		},
		{
			name:        "Text with spaces",
			input:       `Type "hello world"`,
			expectedArg: "hello world",
		},
		{
			name:        "Text with quotes",
			input:       `Type "say \"hi\""`,
			expectedArg: `say "hi"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commands, _ := ParseFile(tt.input)

			if len(commands) == 0 {
				t.Fatal("No commands parsed")
			}

			if commands[0].Type != CommandTypeType {
				t.Errorf("Expected CommandTypeType, got %v", commands[0].Type)
			}

			if len(commands[0].Args) == 0 {
				t.Fatal("No arguments in command")
			}

			if commands[0].Args[0] != tt.expectedArg {
				t.Errorf("Expected %q, got %q", tt.expectedArg, commands[0].Args[0])
			}
		})
	}
}

func TestParserSleepCommand(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		expectedArg      string
		expectedDuration time.Duration
	}{
		{
			name:             "Milliseconds",
			input:            `Sleep 500ms`,
			expectedArg:      "500ms",
			expectedDuration: 500 * time.Millisecond,
		},
		{
			name:             "Seconds",
			input:            `Sleep 2s`,
			expectedArg:      "2s",
			expectedDuration: 2 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commands, _ := ParseFile(tt.input)

			if len(commands) == 0 {
				t.Fatal("No commands parsed")
			}

			cmd := commands[0]
			if cmd.Type != CommandTypeSleep {
				t.Errorf("Expected CommandTypeSleep, got %v", cmd.Type)
			}

			if len(cmd.Args) == 0 {
				t.Fatal("No arguments in command")
			}

			if cmd.Args[0] != tt.expectedArg {
				t.Errorf("Expected %q, got %q", tt.expectedArg, cmd.Args[0])
			}

			if cmd.Delay != tt.expectedDuration {
				t.Errorf("Expected delay %v, got %v", tt.expectedDuration, cmd.Delay)
			}
		})
	}
}

func TestParserKeyCombo(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectedArg string
	}{
		{
			name:        "Ctrl+B",
			input:       `Ctrl+B`,
			expectedArg: "Ctrl+B",
		},
		{
			name:        "Alt+1",
			input:       `Alt+1`,
			expectedArg: "Alt+1",
		},
		{
			name:        "Ctrl+Alt+D",
			input:       `Ctrl+Alt+D`,
			expectedArg: "Ctrl+Alt+D",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commands, _ := ParseFile(tt.input)

			if len(commands) == 0 {
				t.Fatal("No commands parsed")
			}

			cmd := commands[0]
			if cmd.Type != CommandTypeKeyCombo {
				t.Errorf("Expected CommandTypeKeyCombo, got %v", cmd.Type)
			}

			if len(cmd.Args) == 0 {
				t.Fatal("No arguments in command")
			}

			if cmd.Args[0] != tt.expectedArg {
				t.Errorf("Expected %q, got %q", tt.expectedArg, cmd.Args[0])
			}
		})
	}
}

func TestParserTuiosActions(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedType CommandType
	}{
		{
			name:         "NewWindow",
			input:        `NewWindow`,
			expectedType: CommandTypeNewWindow,
		},
		{
			name:         "CloseWindow",
			input:        `CloseWindow`,
			expectedType: CommandTypeCloseWindow,
		},
		{
			name:         "ToggleTiling",
			input:        `ToggleTiling`,
			expectedType: CommandTypeToggleTiling,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commands, _ := ParseFile(tt.input)

			if len(commands) == 0 {
				t.Fatal("No commands parsed")
			}

			if commands[0].Type != tt.expectedType {
				t.Errorf("Expected %v, got %v", tt.expectedType, commands[0].Type)
			}
		})
	}
}

func TestParserSwitchWorkspace(t *testing.T) {
	input := `SwitchWorkspace 2`

	commands, _ := ParseFile(input)

	if len(commands) == 0 {
		t.Fatal("No commands parsed")
	}

	cmd := commands[0]
	if cmd.Type != CommandTypeSwitchWS {
		t.Errorf("Expected CommandTypeSwitchWS, got %v", cmd.Type)
	}

	if len(cmd.Args) == 0 {
		t.Fatal("No arguments in command")
	}

	if cmd.Args[0] != "2" {
		t.Errorf("Expected workspace 2, got %s", cmd.Args[0])
	}
}

func TestParserComments(t *testing.T) {
	input := `# This is a comment
Type "hello"
# Another comment
Enter`

	commands, _ := ParseFile(input)

	if len(commands) != 2 {
		t.Errorf("Expected 2 commands, got %d", len(commands))
	}

	if commands[0].Type != CommandTypeType {
		t.Errorf("Expected CommandTypeType, got %v", commands[0].Type)
	}

	if commands[1].Type != CommandTypeEnter {
		t.Errorf("Expected CommandTypeEnter, got %v", commands[1].Type)
	}
}

func TestParserComplexScript(t *testing.T) {
	input := `# Demo tape script
Type "echo 'Hello World'"
Sleep 500ms
Enter

# Switch workspace
Alt+2
Sleep 1s

# Create new window
NewWindow
Sleep 200ms
Type "vim"
Enter`

	commands, errors := ParseFile(input)

	if len(errors) > 0 {
		t.Errorf("Unexpected parse errors: %v", errors)
	}

	expectedCount := 9 // Type, Sleep, Enter, Alt+2, Sleep, NewWindow, Sleep, Type, Enter
	if len(commands) != expectedCount {
		t.Errorf("Expected %d commands, got %d", expectedCount, len(commands))
	}
}

func TestParserErrorHandling(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
		errorCount  int
	}{
		{
			name:        "Valid script",
			input:       `Type "hello"\nEnter`,
			expectError: false,
			errorCount:  0,
		},
		{
			name:        "Missing argument",
			input:       `Type`,
			expectError: true,
			errorCount:  1,
		},
		{
			name:        "Invalid duration",
			input:       `Sleep invalid`,
			expectError: true,
			errorCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errors := ParseFile(tt.input)

			if tt.expectError && len(errors) == 0 {
				t.Error("Expected parse errors but got none")
			}

			if !tt.expectError && len(errors) > 0 {
				t.Errorf("Unexpected parse errors: %v", errors)
			}

			if len(errors) != tt.errorCount {
				t.Errorf("Expected %d errors, got %d", tt.errorCount, len(errors))
			}
		})
	}
}

func TestParserLineNumbers(t *testing.T) {
	input := `Type "line1"
Enter
Type "line3"`

	commands, _ := ParseFile(input)

	expectedLines := []int{1, 2, 3}

	if len(commands) != len(expectedLines) {
		t.Errorf("Expected %d commands, got %d", len(expectedLines), len(commands))
	}

	for i, expectedLine := range expectedLines {
		if commands[i].Line != expectedLine {
			t.Errorf("Command %d: expected line %d, got %d", i, expectedLine, commands[i].Line)
		}
	}
}

func TestParserDelayModifier(t *testing.T) {
	input := `Type@100ms "hello"
Enter@50ms
Backspace@200ms 3`

	commands, _ := ParseFile(input)

	if len(commands) != 3 {
		t.Errorf("Expected 3 commands, got %d", len(commands))
	}

	expectedDelays := []time.Duration{
		100 * time.Millisecond,
		50 * time.Millisecond,
		200 * time.Millisecond,
	}

	for i, expectedDelay := range expectedDelays {
		if commands[i].Delay != expectedDelay {
			t.Errorf("Command %d: expected delay %v, got %v", i, expectedDelay, commands[i].Delay)
		}
	}
}

func TestParserMultipleRepeat(t *testing.T) {
	input := `Backspace 5
Down 3
Up 10`

	commands, _ := ParseFile(input)

	if len(commands) != 3 {
		t.Errorf("Expected 3 commands, got %d", len(commands))
	}

	expectedArgs := []string{"5", "3", "10"}

	for i, expectedArg := range expectedArgs {
		if len(commands[i].Args) == 0 {
			t.Errorf("Command %d: no arguments", i)
			continue
		}

		if commands[i].Args[0] != expectedArg {
			t.Errorf("Command %d: expected %q, got %q", i, expectedArg, commands[i].Args[0])
		}
	}
}
