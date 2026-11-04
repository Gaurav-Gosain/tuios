package lint

import (
	"os/exec"
	"strings"
	"testing"
)

// guiOnly are the module paths only the desktop app (cmd/guios) may build
// in. The tuios and tuios-web binaries ship under a size ceiling and run on
// machines with no GTK, so a GUI toolkit in their import graph is a bug even
// when it compiles.
var guiOnly = []string{"github.com/egoist/mygo"}

// TestShippedBinariesLeaveTheGUIToolkitOut is the binary boundary lint. It
// lists every package cmd/tuios and cmd/tuios-web build and fails on any
// package of a module in guiOnly.
func TestShippedBinariesLeaveTheGUIToolkitOut(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("go", "list", "-deps", "./cmd/tuios", "./cmd/tuios-web")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	var found []string
	for pkg := range strings.FieldsSeq(string(out)) {
		for _, mod := range guiOnly {
			if pkg == mod || strings.HasPrefix(pkg, mod+"/") {
				found = append(found, pkg)
			}
		}
	}
	if len(found) > 0 {
		t.Errorf("the tuios binaries build GUI packages, which belong to cmd/guios only:\n  %s",
			strings.Join(found, "\n  "))
	}
}
