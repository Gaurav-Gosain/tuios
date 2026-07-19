package tape

import "testing"

// TestLexer_IllegalTokenKeepsTheSourceByte checks that an ILLEGAL token reports
// the byte that was actually in the file.
//
// l.ch is a byte, so string(l.ch) re-encoded it as a rune: a stray 0xA8 came
// back as the two bytes of U+00A8, so the error message pointed at a character
// the author never wrote.
func TestLexer_IllegalTokenKeepsTheSourceByte(t *testing.T) {
	for _, b := range []byte{0xA8, 0xFF, 0x80, 0xC3, 0x01, '~'} {
		src := string([]byte{b})
		toks := Tokenize(src)
		if len(toks) == 0 {
			t.Fatalf("byte %#x: no tokens", b)
		}
		tok := toks[0]
		if tok.Type != TokenIllegal {
			continue // a byte the lexer legitimately understands
		}
		if tok.Literal != src {
			t.Errorf("byte %#x: literal = %q (%d bytes), want %q (1 byte)",
				b, tok.Literal, len(tok.Literal), src)
		}
	}
}
