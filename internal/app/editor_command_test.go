package app

import (
	"os"
	"os/exec"
	"testing"
)

// How TestEditorCommandStdout could pass wrongly, written down first:
//   - Stdout could be set to the wrapper itself. The wrapper is not an
//     *os.File, so os/exec would still make a pipe. The test requires the
//     file under it.
//   - A client with no wrapper could get os.Stdout. It must stay unset, so
//     Bubble Tea hands the editor the program's own output.
func TestEditorCommandStdout(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	c := (&OS{PostRenderWriter: NewPostRenderWriter(f)}).editorCommand(exec.Command("true"))
	got, ok := c.Stdout.(*os.File)
	if !ok {
		t.Fatalf("Stdout is %T, want the *os.File under the wrapper, which os/exec passes on as it is", c.Stdout)
	}
	if got != f {
		t.Errorf("Stdout is %v, want the wrapper's file %v", got.Name(), f.Name())
	}

	if c := (&OS{}).editorCommand(exec.Command("true")); c.Stdout != nil {
		t.Errorf("Stdout is %T with no wrapper, want it left unset", c.Stdout)
	}
}
