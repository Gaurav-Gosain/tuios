package scrollback_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/scrollback"
	"tuios.dev/vt"
)

// The scrollback browser reads a bounded command from the buffer when a C
// mark captured no text.
//
// The failure modes this is written against:
//   - a C mark more than 64 lines after its B captures nothing, so the
//     browser falls back to the buffer, and reads every line from B to C as
//     the command, which grows with the output a program printed after B;
//   - the bound cuts the fallback for a command it should read whole: a
//     command of a few lines with no C mark.

// maxFallbackLines is the most lines the fallback reads. It mirrors the
// unexported maxFallbackCommandLines in parser.go, which this external test
// package cannot read: change both together.
const maxFallbackLines = 64

// parse writes input to an 80 column terminal and returns it and its blocks.
func parse(t *testing.T, input string) (vt.Terminal, []scrollback.CommandBlock) {
	t.Helper()
	term := vt.NewWithScrollback(80, 24, 10000)
	if _, err := term.Write([]byte(input)); err != nil {
		t.Fatalf("write: %v", err)
	}
	return term, scrollback.ParseBlocks(term)
}

// TestFallbackCommandIsBounded is a security boundary: a pane's output
// decides how far the browser reads. It writes a prompt, a command line, 1000
// lines of output and only then the C mark, so the C captures nothing and the
// browser reads the command from the buffer. It must read at most 64 lines,
// and still start at the command. A command of three lines with no C mark is
// read whole.
func TestFallbackCommandIsBounded(t *testing.T) {
	const prompt = "\x1b]133;A\x07$ \x1b]133;B\x07"
	const done = "\x1b]133;D;0\x07"

	t.Run("1000 lines before the C mark", func(t *testing.T) {
		const lines = 1000
		input := prompt + "cat build.log\r\n" +
			strings.Repeat(strings.Repeat("x", 78)+"\r\n", lines) +
			"\x1b]133;C\x07ok\r\n" + done + prompt
		term, blocks := parse(t, input)
		// The browser reads the buffer only for a C mark with no text.
		if c := term.SemanticMarkers().Last(vt.MarkerCommandExecuted); c == nil || c.CapturedText != "" {
			t.Fatalf("the C mark %d lines after B captured text, want none, so the browser reads the buffer", lines)
		}
		if len(blocks) != 1 {
			t.Fatalf("got %d blocks, want 1", len(blocks))
		}
		cmd := blocks[0].Command
		if got := strings.Count(cmd, "\n") + 1; got > maxFallbackLines {
			t.Errorf("the command read from the buffer has %d lines (%d bytes), want at most %d", got, len(cmd), maxFallbackLines)
		}
		if !strings.HasPrefix(cmd, "cat build.log\n") {
			t.Errorf("the command read from the buffer starts %q, want the line after the prompt", cmd[:min(len(cmd), 40)])
		}
	})

	t.Run("a command of three lines with no C mark", func(t *testing.T) {
		_, blocks := parse(t, prompt+"for i in 1 2\r\n> do echo $i\r\n> done\r\n"+done+prompt)
		if len(blocks) != 1 {
			t.Fatalf("got %d blocks, want 1", len(blocks))
		}
		want := "for i in 1 2\n> do echo $i\n> done"
		if got := blocks[0].Command; got != want {
			t.Errorf("the command read from the buffer is %q, want %q", got, want)
		}
	})
}
