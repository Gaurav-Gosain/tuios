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

func TestCompileTypeWithoutEnter(t *testing.T) {
	cmds := compileProjectBody(`Type "partial"`)
	if len(cmds) != 1 || cmds[0].Type != tape.CommandTypeType {
		t.Fatalf("got %v, want a single Type", typesOf(cmds))
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

func TestCompileRenameAndTiling(t *testing.T) {
	cmds := compileProjectBody("RenameWindow \"edit\"\nRename \"srv\"\nEnableTiling\nDisableTiling")
	want := []tape.CommandType{
		tape.CommandTypeRenameWindow, tape.CommandTypeRenameWindow,
		tape.CommandTypeEnableTiling, tape.CommandTypeDisableTiling,
	}
	got := typesOf(cmds)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cmd %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestCompileIgnoresCommentsBlanksAndUnknown(t *testing.T) {
	cmds := compileProjectBody("# a comment\n\nBogusCommand foo\nType \"x\" Enter\n")
	if len(cmds) != 2 || cmds[0].Type != tape.CommandTypeType || cmds[1].Type != tape.CommandTypeEnter {
		t.Fatalf("comments/blanks/unknown not skipped cleanly: %v", typesOf(cmds))
	}
}

func TestCompileFullDemoBody(t *testing.T) {
	// The coordinator's demo body: it must compile into commands that build a
	// three-pane layout with the echoes actually running.
	body := `RenameWindow "editor"
Type "echo this pane is the editor" Enter
Split vertical
RenameWindow "server"
Type "echo this pane runs the server" Enter
Split horizontal
RenameWindow "shell"
Focus "editor"
`
	cmds := compileProjectBody(body)

	var enters, splits, renames, focuses, types int
	for _, c := range cmds {
		switch c.Type {
		case tape.CommandTypeEnter:
			enters++
		case tape.CommandTypeSplit:
			splits++
		case tape.CommandTypeRenameWindow:
			renames++
		case tape.CommandTypeFocusWindow:
			focuses++
		case tape.CommandTypeType:
			types++
		}
	}
	if types != 2 || enters != 2 {
		t.Fatalf("types=%d enters=%d, want 2 and 2 (both echoes must submit)", types, enters)
	}
	if splits != 2 {
		t.Fatalf("splits=%d, want 2 (two panes created)", splits)
	}
	if renames != 3 {
		t.Fatalf("renames=%d, want 3 (editor/server/shell)", renames)
	}
	if focuses != 1 {
		t.Fatalf("focuses=%d, want 1 (Focus editor)", focuses)
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
