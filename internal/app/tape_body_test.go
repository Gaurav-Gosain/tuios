package app

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// TestCompileProjectBody pins what a project tape's body compiles to.
//
//   - `Type "x" Enter` is Type then a real Enter, so the command runs in the
//     pane instead of the text mashing together; `Run` is the same pair.
//   - `Split` keeps its direction (the recorder parser dropped it, which is why
//     no panes were created) and is followed by a settle Sleep, so the async
//     daemon pane creation is not raced.
//   - `Focus "name"` targets FocusWindow, which the executor resolves by pane
//     name; the recorder's `Focus` is a directional command it does not run.
//   - Comments, blank lines and unknown commands are skipped.
func TestCompileProjectBody(t *testing.T) {
	type cmd struct {
		typ  tape.CommandType
		args []string
	}
	for _, tc := range []struct {
		in   string
		want []cmd
	}{
		{`Type "echo hi" Enter`, []cmd{{tape.CommandTypeType, []string{"echo hi"}}, {tape.CommandTypeEnter, nil}}},
		{`Run "make dev"`, []cmd{{tape.CommandTypeType, []string{"make dev"}}, {tape.CommandTypeEnter, nil}}},
		{"Split vertical", []cmd{{tape.CommandTypeSplit, []string{"vertical"}}, {tape.CommandTypeSleep, nil}}},
		{"Split v", []cmd{{tape.CommandTypeSplit, []string{"vertical"}}, {tape.CommandTypeSleep, nil}}},
		{"Split horizontal", []cmd{{tape.CommandTypeSplit, []string{"horizontal"}}, {tape.CommandTypeSleep, nil}}},
		{"Split h", []cmd{{tape.CommandTypeSplit, []string{"horizontal"}}, {tape.CommandTypeSleep, nil}}},
		{"Split", []cmd{{tape.CommandTypeSplit, []string{"vertical"}}, {tape.CommandTypeSleep, nil}}},
		{`Focus "editor"`, []cmd{{tape.CommandTypeFocusWindow, []string{"editor"}}}},
		{"# a comment\n\nBogusCommand foo\nType \"x\" Enter\n", []cmd{{tape.CommandTypeType, []string{"x"}}, {tape.CommandTypeEnter, nil}}},
	} {
		got := compileProjectBody(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("%q compiled to %d commands, want %d: %v", tc.in, len(got), len(tc.want), got)
			continue
		}
		for i, w := range tc.want {
			if got[i].Type != w.typ {
				t.Errorf("%q: command %d is %v, want %v", tc.in, i, got[i].Type, w.typ)
			}
			if w.args != nil && (len(got[i].Args) < len(w.args) || !slices.Equal(got[i].Args[:len(w.args)], w.args)) {
				t.Errorf("%q: command %d args %v, want %v", tc.in, i, got[i].Args, w.args)
			}
		}
	}

	if got, want := tokenizeTapeLine(`Type "echo hello world" Enter`), []string{"Type", "echo hello world", "Enter"}; !slices.Equal(got, want) {
		t.Errorf("tokenizeTapeLine = %q, want %q", got, want)
	}
}
