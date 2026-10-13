package tape

// Seams the tests reach for. Production code calls the functions these wrap.

// Tokenize returns all tokens from the input (useful for testing)
func Tokenize(input string) []Token {
	l := New(input)
	var tokens []Token
	for {
		tok := l.NextToken()
		tokens = append(tokens, tok)
		if tok.Type == TokenEOF {
			break
		}
	}
	return tokens
}
