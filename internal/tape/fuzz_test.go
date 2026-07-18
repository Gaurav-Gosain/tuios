package tape_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// tapeSeeds are hand-written shapes covering each command form plus the
// malformed variants a hand-edited tape file produces: unterminated strings and
// regexes, truncated commands at EOF, and counts or durations that do not fit a
// machine integer.
var tapeSeeds = []string{
	"",
	"\n\n\n",
	"# just a comment",
	"# comment with no newline",
	"Type \"hello\"\nEnter\n",
	"Type 'single quoted'\n",
	"Type `backtick`\n",
	"Sleep 1s\nSleep 500ms\nSleep 2\nSleep 1.5s\n",
	"Sleep 99999999999999999999s\n",
	"Sleep -1s\n",
	"Sleep\n",
	"Enter 5\nTab 3\nSpace 10\nBackspace 2\nDelete 1\n",
	"Enter 99999999999999999999\n",
	"Ctrl+C\nCtrl+Shift+A\nAlt+Tab\n",
	"Ctrl+\n",
	"Ctrl+++\n",
	"Up\nDown\nLeft\nRight\nEscape\n",
	"Set Width 100\nSet Height 50\nSet TypingSpeed 50ms\n",
	"Set\nSet Width\n",
	"Output demo.gif\n",
	"Output\n",
	"Focus 1\nFocus\n",
	"SwitchWorkspace 2\nMoveToWorkspace 3\nMoveAndFollowWorkspace 4\n",
	"WindowRename 1 \"new name\"\nWindowRename\n",
	"Wait 5s\nWait\n",
	"WaitUntilRegex /foo.*bar/\n",
	"WaitUntilRegex /unterminated\n",
	"WaitUntilRegex //\n",
	"WaitUntilRegex /(((((((((((/\n",
	"WaitUntilRegex /" + strings.Repeat("a*", 512) + "/\n",
	// Unterminated literals.
	"Type \"unterminated",
	"Type 'unterminated",
	"Type `unterminated",
	// Escapes inside strings.
	`Type "a\"b\\c\nd\te"` + "\n",
	`Type "trailing backslash \`,
	// Operators in isolation.
	"+\n@\n,\n/\n(\n)\n",
	"@@@@@@\n",
	"////////\n",
	// Numbers.
	"1234567890\n",
	"1.2.3.4.5\n",
	".....\n",
	"1e999\n",
	// Non-UTF-8 and control bytes in the source text.
	"Type \"\xff\xfe\"\n",
	"\x00\x01\x02\n",
	"Type \"\xe4\xb8\x96\xe7\x95\x8c\"\n",
	// Deeply repeated input.
	strings.Repeat("Enter\n", 2048),
	strings.Repeat("Type \"x\" ", 2048),
	strings.Repeat("+", 4096),
	strings.Repeat("\"", 4096),
	strings.Repeat("/", 4096),
	// Unknown identifiers.
	"NotACommand arg1 arg2\n",
	strings.Repeat("Unknown\n", 1024),
}

// FuzzTapeParse runs the lexer and the parser over arbitrary source text. A
// tape file is user-authored and often generated, so the parser must report
// errors rather than panic or spin, and it must always terminate.
func FuzzTapeParse(f *testing.F) {
	for _, s := range tapeSeeds {
		f.Add(s)
	}
	// Seed with the tape files shipped in the repo, which are the sequences the
	// parser actually sees in practice.
	for _, dir := range []string{"../../examples", "../../assets"} {
		matches, err := filepath.Glob(filepath.Join(dir, "*.tape"))
		if err != nil {
			continue
		}
		for _, m := range matches {
			if b, err := os.ReadFile(m); err == nil {
				f.Add(string(b))
			}
		}
	}

	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 1<<16 {
			src = src[:1<<16]
		}

		done := make(chan []tape.Command, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					done <- nil
					panic(r)
				}
			}()
			p := tape.NewParser(tape.New(src))
			done <- p.Parse()
		}()

		var cmds []tape.Command
		select {
		case cmds = <-done:
		case <-time.After(30 * time.Second):
			t.Fatalf("parsing %d bytes did not terminate", len(src))
		}

		// The parser consumes at least one token per command, so it can never
		// emit more commands than the source has bytes.
		if len(cmds) > len(src)+1 {
			t.Fatalf("parsed %d commands from %d bytes of source", len(cmds), len(src))
		}

		for i, cmd := range cmds {
			if cmd.Line < 0 {
				t.Fatalf("command %d has negative line %d", i, cmd.Line)
			}
			if cmd.Column < 0 {
				t.Fatalf("command %d has negative column %d", i, cmd.Column)
			}
		}
	})
}

// FuzzTapeTokenize exercises the lexer on its own, where the readString,
// readRegex and readNumberWithDecimal scanners each walk the input with their
// own bounds checks and are the most likely place for an index panic.
func FuzzTapeTokenize(f *testing.F) {
	for _, s := range tapeSeeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 1<<16 {
			src = src[:1<<16]
		}

		done := make(chan []tape.Token, 1)
		go func() {
			done <- tape.Tokenize(src)
		}()

		var toks []tape.Token
		select {
		case toks = <-done:
		case <-time.After(30 * time.Second):
			t.Fatalf("tokenizing %d bytes did not terminate", len(src))
		}

		if len(toks) == 0 {
			t.Fatalf("tokenizer returned no tokens, want at least EOF")
		}
		if last := toks[len(toks)-1]; last.Type != tape.TokenEOF {
			t.Fatalf("token stream ends with %v, want EOF", last.Type)
		}
		// Every byte is consumed by at most one token's literal, plus the
		// delimiters the lexer drops, so the stream cannot outgrow the source.
		if len(toks) > len(src)+2 {
			t.Fatalf("produced %d tokens from %d bytes", len(toks), len(src))
		}
		for i, tok := range toks {
			if tok.Line < 0 || tok.Column < 0 {
				t.Fatalf("token %d (%v) has negative position (%d,%d)",
					i, tok.Type, tok.Line, tok.Column)
			}
			if len(tok.Literal) > len(src) {
				t.Fatalf("token %d (%v) literal is %d bytes from %d bytes of source",
					i, tok.Type, len(tok.Literal), len(src))
			}
		}
	})
}
