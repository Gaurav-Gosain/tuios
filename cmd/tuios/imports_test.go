package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The fuzzer is test-only, and the binary users install must not carry it. That
// is the whole reason visual mode is a separate command rather than a
// `tuios fuzz` subcommand: a subcommand would link the engine, the observer,
// the display and the whole action alphabet into the shipped program, where
// none of it can ever run.
//
// Nothing enforces that except this test. The check is on the import graph
// rather than on the linked binary because that is where the decision is made:
// one import added anywhere under internal/app pulls the lot back in, and a
// human reading the diff would not see it.
var forbidden = []string{
	"github.com/Gaurav-Gosain/tuios/internal/fuzz",
	"github.com/Gaurav-Gosain/tuios/internal/fuzz/vis",
	"github.com/Gaurav-Gosain/tuios/internal/fuzz/apptarget",
}

func TestShippedBinaryDoesNotLinkTheFuzzer(t *testing.T) {
	deps := depsOf(t, "github.com/Gaurav-Gosain/tuios/cmd/tuios")
	for _, bad := range forbidden {
		if deps[bad] {
			t.Errorf("the tuios binary imports %s\n\n%s", bad, why(t, bad))
		}
	}
}

// The other half of the same claim: the fuzz binary really does link them, so a
// pass above means the split is working rather than that the packages have
// quietly stopped existing.
func TestFuzzBinaryLinksTheFuzzer(t *testing.T) {
	deps := depsOf(t, "github.com/Gaurav-Gosain/tuios/cmd/tuios-fuzz")
	for _, want := range forbidden {
		if !deps[want] {
			t.Errorf("tuios-fuzz does not import %s, so this test proves nothing", want)
		}
	}
}

func depsOf(t *testing.T, pkg string) map[string]bool {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	deps := map[string]bool{}
	for l := range strings.SplitSeq(string(out), "\n") {
		deps[strings.TrimSpace(l)] = true
	}
	return deps
}

// why names a package inside tuios that imports the forbidden one, so a failure
// points at the edge to cut rather than at the whole graph.
func why(t *testing.T, bad string) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f",
		"{{.ImportPath}} imports {{join .Imports \" \"}}",
		"github.com/Gaurav-Gosain/tuios/...").Output()
	if err != nil {
		return ""
	}
	var hits []string
	for l := range strings.SplitSeq(string(out), "\n") {
		path, imports, ok := strings.Cut(l, " imports ")
		if !ok || strings.HasPrefix(path, "github.com/Gaurav-Gosain/tuios/cmd/tuios-fuzz") {
			continue
		}
		for imp := range strings.FieldsSeq(imports) {
			if imp == bad {
				hits = append(hits, "  "+path)
			}
		}
	}
	if len(hits) == 0 {
		return ""
	}
	return "imported by:\n" + strings.Join(hits, "\n")
}
