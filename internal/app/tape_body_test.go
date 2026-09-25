package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// findCmd returns the indices of commands of the given type.
func typesOf(cmds []tape.Command) []tape.CommandType {
	out := make([]tape.CommandType, len(cmds))
	for i, c := range cmds {
		out[i] = c.Type
	}
	return out
}

func TestCompileTypeEnter(t *testing.T) {
	// `Type "x" Enter` must compile to Type followed by a real Enter, so the
	// command actually runs in the pane instead of the text mashing together.
	cmds := compileProjectBody(`Type "echo hi" Enter`)
	if len(cmds) != 2 {
		t.Fatalf("got %d commands, want 2: %v", len(cmds), typesOf(cmds))
	}
	if cmds[0].Type != tape.CommandTypeType || cmds[0].Args[0] != "echo hi" {
		t.Fatalf("cmd0 = %v %v, want Type \"echo hi\"", cmds[0].Type, cmds[0].Args)
	}
	if cmds[1].Type != tape.CommandTypeEnter {
		t.Fatalf("cmd1 = %v, want Enter", cmds[1].Type)
	}
}

func TestCompileRunIsTypeEnter(t *testing.T) {
	cmds := compileProjectBody(`Run "make dev"`)
	if len(cmds) != 2 || cmds[0].Type != tape.CommandTypeType || cmds[1].Type != tape.CommandTypeEnter {
		t.Fatalf("Run did not compile to Type+Enter: %v", typesOf(cmds))
	}
	if cmds[0].Args[0] != "make dev" {
		t.Fatalf("Run arg = %q, want \"make dev\"", cmds[0].Args[0])
	}
}

func TestCompileSplitKeepsDirectionAndSettles(t *testing.T) {
	// `Split vertical` must keep its direction (the recorder parser dropped it,
	// which is why no panes were created), and a settle Sleep must follow so the
	// async daemon pane creation is not raced.
	for _, tc := range []struct{ in, want string }{
		{"Split vertical", "vertical"},
		{"Split v", "vertical"},
		{"Split horizontal", "horizontal"},
		{"Split h", "horizontal"},
		{"Split", "vertical"},
	} {
		cmds := compileProjectBody(tc.in)
		if len(cmds) != 2 {
			t.Fatalf("%q: got %d commands, want split+settle: %v", tc.in, len(cmds), typesOf(cmds))
		}
		if cmds[0].Type != tape.CommandTypeSplit || cmds[0].Args[0] != tc.want {
			t.Fatalf("%q: split = %v %v, want direction %q", tc.in, cmds[0].Type, cmds[0].Args, tc.want)
		}
		if cmds[1].Type != tape.CommandTypeSleep {
			t.Fatalf("%q: no settle Sleep after Split: %v", tc.in, typesOf(cmds))
		}
	}
}

func TestCompileFocusMapsToFocusWindow(t *testing.T) {
	// The recorder's `Focus` is a directional command the executor does not
	// implement; a project tape's `Focus "name"` must target FocusWindow, which
	// the executor resolves by pane name.
	cmds := compileProjectBody(`Focus "editor"`)
	if len(cmds) != 1 || cmds[0].Type != tape.CommandTypeFocusWindow || cmds[0].Args[0] != "editor" {
		t.Fatalf("Focus compiled to %v %v, want FocusWindow \"editor\"", typesOf(cmds), cmds)
	}
}

func TestCompileIgnoresCommentsBlanksAndUnknown(t *testing.T) {
	cmds := compileProjectBody("# a comment\n\nBogusCommand foo\nType \"x\" Enter\n")
	if len(cmds) != 2 || cmds[0].Type != tape.CommandTypeType || cmds[1].Type != tape.CommandTypeEnter {
		t.Fatalf("comments/blanks/unknown not skipped cleanly: %v", typesOf(cmds))
	}
}

func TestTokenizeQuotedArgs(t *testing.T) {
	got := tokenizeTapeLine(`Type "echo hello world" Enter`)
	want := []string{"Type", "echo hello world", "Enter"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token %d = %q, want %q", i, got[i], want[i])
		}
	}
}
