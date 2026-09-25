package webshell

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// TestEveryTapeParses keeps the tapes in the demo's filesystem playable.
func TestEveryTapeParses(t *testing.T) {
	for _, p := range tapeFiles() {
		script, _ := readFile(p)
		if cmds, errs := tape.ParseFile(script); len(errs) > 0 || len(cmds) == 0 {
			t.Errorf("%s does not parse: %v", p, errs)
		}
	}
}
