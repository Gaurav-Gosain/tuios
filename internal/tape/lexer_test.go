package tape

import (
	"testing"
)

func TestLexerBasicTokens(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []TokenType
	}{
		{
			name:     "Type command",
			input:    `Type "hello"`,
			expected: []TokenType{TOKEN_TYPE, TOKEN_STRING, TOKEN_EOF},
		},
		{
			name:     "Sleep command",
			input:    `Sleep 500ms`,
			expected: []TokenType{TOKEN_SLEEP, TOKEN_DURATION, TOKEN_EOF},
		},
		{
			name:     "Enter command",
			input:    `Enter`,
			expected: []TokenType{TOKEN_ENTER, TOKEN_EOF},
		},
		{
			name:     "Space command",
			input:    `Space`,
			expected: []TokenType{TOKEN_SPACE, TOKEN_EOF},
		},
		{
			name:     "Key combination",
			input:    `Ctrl+B`,
			expected: []TokenType{TOKEN_CTRL, TOKEN_PLUS, TOKEN_IDENTIFIER, TOKEN_EOF},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := Tokenize(tt.input)

			if len(tokens) != len(tt.expected) {
				t.Errorf("Expected %d tokens, got %d", len(tt.expected), len(tokens))
			}

			for i, expectedType := range tt.expected {
				if tokens[i].Type != expectedType {
					t.Errorf("Token %d: expected %v, got %v", i, expectedType, tokens[i].Type)
				}
			}
		})
	}
}

func TestLexerStrings(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedValue string
	}{
		{
			name:          "Double quoted string",
			input:         `Type "hello world"`,
			expectedValue: "hello world",
		},
		{
			name:          "Single quoted string",
			input:         `Type 'hello world'`,
			expectedValue: "hello world",
		},
		{
			name:          "Backtick string",
			input:         `Type ` + "`hello world`",
			expectedValue: "hello world",
		},
		{
			name:          "Escaped quotes",
			input:         `Type "hello \"world\""`,
			expectedValue: `hello "world"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := Tokenize(tt.input)

			// Find the string token
			var stringToken Token
			for _, tok := range tokens {
				if tok.Type == TOKEN_STRING {
					stringToken = tok
					break
				}
			}

			if stringToken.Literal != tt.expectedValue {
				t.Errorf("Expected %q, got %q", tt.expectedValue, stringToken.Literal)
			}
		})
	}
}

func TestLexerDurations(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedValue string
	}{
		{
			name:          "Milliseconds",
			input:         `Sleep 500ms`,
			expectedValue: "500ms",
		},
		{
			name:          "Seconds",
			input:         `Sleep 2s`,
			expectedValue: "2s",
		},
		{
			name:          "Decimal seconds",
			input:         `Sleep 1.5s`,
			expectedValue: "1.5s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := Tokenize(tt.input)

			// Find the duration token
			var durationToken Token
			for _, tok := range tokens {
				if tok.Type == TOKEN_DURATION {
					durationToken = tok
					break
				}
			}

			if durationToken.Literal != tt.expectedValue {
				t.Errorf("Expected %q, got %q", tt.expectedValue, durationToken.Literal)
			}
		})
	}
}

func TestLexerComments(t *testing.T) {
	input := `# This is a comment
Type "hello"
# Another comment
Enter`

	tokens := Tokenize(input)

	// Should skip comments
	var types []TokenType
	for _, tok := range tokens {
		types = append(types, tok.Type)
	}

	expected := []TokenType{TOKEN_TYPE, TOKEN_STRING, TOKEN_ENTER, TOKEN_EOF}

	if len(types) != len(expected) {
		t.Errorf("Expected %d tokens, got %d", len(expected), len(types))
	}

	for i, expectedType := range expected {
		if types[i] != expectedType {
			t.Errorf("Token %d: expected %v, got %v", i, expectedType, types[i])
		}
	}
}

func TestLexerIdentifiers(t *testing.T) {
	input := `NewWindow
CloseWindow
Focus 1
SwitchWorkspace 2`

	tokens := Tokenize(input)

	expectedTypes := []TokenType{
		TOKEN_NEW_WINDOW,
		TOKEN_CLOSE_WINDOW,
		TOKEN_FOCUS,
		TOKEN_NUMBER,
		TOKEN_SWITCH_WS,
		TOKEN_NUMBER,
		TOKEN_EOF,
	}

	if len(tokens) != len(expectedTypes) {
		t.Errorf("Expected %d tokens, got %d", len(expectedTypes), len(tokens))
	}

	for i, expectedType := range expectedTypes {
		if tokens[i].Type != expectedType {
			t.Errorf("Token %d: expected %v, got %v", i, expectedType, tokens[i].Type)
		}
	}
}

func TestLexerLineNumbers(t *testing.T) {
	input := `Type "line1"
Type "line2"
Type "line3"`

	tokens := Tokenize(input)

	// Filter out newlines to check line numbers
	var typeTokens []Token
	for _, tok := range tokens {
		if tok.Type == TOKEN_TYPE {
			typeTokens = append(typeTokens, tok)
		}
	}

	expectedLines := []int{1, 2, 3}

	if len(typeTokens) != len(expectedLines) {
		t.Errorf("Expected %d TYPE tokens, got %d", len(expectedLines), len(typeTokens))
	}

	for i, expectedLine := range expectedLines {
		if typeTokens[i].Line != expectedLine {
			t.Errorf("Token %d: expected line %d, got %d", i, expectedLine, typeTokens[i].Line)
		}
	}
}

func TestLexerAtModifier(t *testing.T) {
	input := `Type@100ms "hello"
Sleep@2s 500ms`

	tokens := Tokenize(input)

	// Check for @ tokens
	atCount := 0
	for _, tok := range tokens {
		if tok.Type == TOKEN_AT {
			atCount++
		}
	}

	if atCount != 2 {
		t.Errorf("Expected 2 @ tokens, got %d", atCount)
	}
}

func TestKeywordTokenMap(t *testing.T) {
	tests := []struct {
		name     string
		keyword  string
		expected TokenType
	}{
		{"Type", "Type", TOKEN_TYPE},
		{"Sleep", "Sleep", TOKEN_SLEEP},
		{"Enter", "Enter", TOKEN_ENTER},
		{"NewWindow", "NewWindow", TOKEN_NEW_WINDOW},
		{"Unknown", "UnknownKeyword", TOKEN_IDENTIFIER},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenType := LookupKeyword(tt.keyword)
			if tokenType != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, tokenType)
			}
		})
	}
}

func TestTokenTypeHelpers(t *testing.T) {
	t.Run("IsCommand", func(t *testing.T) {
		if !TOKEN_TYPE.IsCommand() {
			t.Error("TOKEN_TYPE should be a command")
		}
		if TOKEN_STRING.IsCommand() {
			t.Error("TOKEN_STRING should not be a command")
		}
	})

	t.Run("IsModifier", func(t *testing.T) {
		if !TOKEN_CTRL.IsModifier() {
			t.Error("TOKEN_CTRL should be a modifier")
		}
		if !TOKEN_ALT.IsModifier() {
			t.Error("TOKEN_ALT should be a modifier")
		}
		if TOKEN_TYPE.IsModifier() {
			t.Error("TOKEN_TYPE should not be a modifier")
		}
	})

	t.Run("IsNavigationKey", func(t *testing.T) {
		if !TOKEN_UP.IsNavigationKey() {
			t.Error("TOKEN_UP should be a navigation key")
		}
		if !TOKEN_DOWN.IsNavigationKey() {
			t.Error("TOKEN_DOWN should be a navigation key")
		}
		if TOKEN_TYPE.IsNavigationKey() {
			t.Error("TOKEN_TYPE should not be a navigation key")
		}
	})
}
