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
			expected: []TokenType{TokenTypeCmd, TokenString, TokenEOF},
		},
		{
			name:     "Sleep command",
			input:    `Sleep 500ms`,
			expected: []TokenType{TokenSleep, TokenDuration, TokenEOF},
		},
		{
			name:     "Enter command",
			input:    `Enter`,
			expected: []TokenType{TokenEnter, TokenEOF},
		},
		{
			name:     "Space command",
			input:    `Space`,
			expected: []TokenType{TokenSpace, TokenEOF},
		},
		{
			name:     "Key combination",
			input:    `Ctrl+B`,
			expected: []TokenType{TokenCtrl, TokenPlus, TokenIdentifier, TokenEOF},
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
				if tok.Type == TokenString {
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
				if tok.Type == TokenDuration {
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

	// Should skip comments but include newlines
	var types []TokenType
	for _, tok := range tokens {
		types = append(types, tok.Type)
	}

	expected := []TokenType{TokenNewline, TokenTypeCmd, TokenString, TokenNewline, TokenNewline, TokenEnter, TokenEOF}

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
		TokenNewWindow,
		TokenNewline,
		TokenCloseWindow,
		TokenNewline,
		TokenFocus,
		TokenNumber,
		TokenNewline,
		TokenSwitchWS,
		TokenNumber,
		TokenEOF,
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
		if tok.Type == TokenTypeCmd {
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
		if tok.Type == TokenAt {
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
		{"Type", "Type", TokenTypeCmd},
		{"Sleep", "Sleep", TokenSleep},
		{"Enter", "Enter", TokenEnter},
		{"NewWindow", "NewWindow", TokenNewWindow},
		{"Unknown", "UnknownKeyword", TokenIdentifier},
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
		if !TokenTypeCmd.IsCommand() {
			t.Error("TokenType should be a command")
		}
		if TokenString.IsCommand() {
			t.Error("TokenString should not be a command")
		}
	})

	t.Run("IsModifier", func(t *testing.T) {
		if !TokenCtrl.IsModifier() {
			t.Error("TokenCtrl should be a modifier")
		}
		if !TokenAlt.IsModifier() {
			t.Error("TokenAlt should be a modifier")
		}
		if TokenTypeCmd.IsModifier() {
			t.Error("TokenType should not be a modifier")
		}
	})

	t.Run("IsNavigationKey", func(t *testing.T) {
		if !TokenUp.IsNavigationKey() {
			t.Error("TokenUp should be a navigation key")
		}
		if !TokenDown.IsNavigationKey() {
			t.Error("TokenDown should be a navigation key")
		}
		if TokenTypeCmd.IsNavigationKey() {
			t.Error("TokenType should not be a navigation key")
		}
	})
}
