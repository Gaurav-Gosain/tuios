package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const allowLayer = "//tuios:allow-layer"

// cockpitLayerPredicates are the per-layer checks of the agents view. Each
// says one sub-view is open. Which one is on top is CockpitTop's to decide.
var cockpitLayerPredicates = []string{
	"CockpitCopyOn", "CockpitImageOpen", "CockpitSpawnOpen", "CockpitCompareOpen", "CockpitArtifactFocused",
	"CockpitNewCardOpen", "InboxConfirmOpen", "InboxPeeking", "InboxReplyOpen", "InboxReasonOpen",
	"InboxSelecting", "CockpitMenuOpen", "CockpitShipOpen", "InboxSnoozePicking", "InboxAgentsSheetOpen",
}

// cockpitLayerReaders are the functions that act on the layer on top: the
// key router, the key bar and the click handler.
var cockpitLayerReaders = []struct{ file, fn string }{
	{"internal/input/inbox_input.go", "handleInboxInput"},
	{"internal/app/render_cockpit.go", "cockpitHints"},
	{"internal/app/cockpit.go", "CockpitClick"},
}

// TestCockpitLayersHaveOneDecider is the agents view's layer lint. The view
// has sixteen sub-views that can sit over the list. The key router, the key
// bar and the click handler each picked the top one with their own chain of
// checks, in their own order, so with the group menu open a click could open
// a confirm whose keys the key bar did not show, and a click on another row
// left Ready to merge open on a card the cursor had left. CockpitTop
// (internal/app/cockpit_layers.go) now decides, and those three read it.
//
// Rejected in the three readers: a call of one of the per-layer predicates.
// A deliberate one carries a comment on its line or the line above:
//
//	//tuios:allow-layer <reason>
//
// It fails on an injected call; see TestCockpitLayerLintCatchesAPredicate.
func TestCockpitLayersHaveOneDecider(t *testing.T) {
	root := repoRoot(t)
	var found []string
	for _, r := range cockpitLayerReaders {
		src, err := os.ReadFile(filepath.Join(root, r.file))
		if err != nil {
			t.Fatal(err)
		}
		got, seen := layerPredicateCalls(t, r.file, src, r.fn)
		if !seen {
			t.Errorf("%s has no function %s: the lint reads nothing there", r.file, r.fn)
		}
		found = append(found, got...)
	}
	if len(found) > 0 {
		t.Errorf("a reader of the agents view's layers decides the top layer itself; switch on CockpitTop instead, "+
			"or mark a deliberate one with %s <reason>:\n  %s", allowLayer, strings.Join(found, "\n  "))
	}
}

// TestEveryCockpitLayerHasAPlace holds that every layer has a case in
// CockpitTop, which decides, and in the key router, which gives it the keys.
// A layer with no case in CockpitTop is never on top; one with no case in
// the key router gets the list's keys while it is drawn.
func TestEveryCockpitLayerHasAPlace(t *testing.T) {
	root := repoRoot(t)
	layers := cockpitLayerConsts(t, filepath.Join(root, "internal/app/cockpit_layers.go"))
	if len(layers) < 10 {
		t.Fatalf("read %d layers from cockpit_layers.go, want every CockpitLayer constant", len(layers))
	}
	for _, where := range []struct{ file, fn, qual string }{
		{"internal/app/cockpit_layers.go", "CockpitTop", ""},
		{"internal/input/inbox_input.go", "handleInboxInput", "app."},
	} {
		body := funcSource(t, filepath.Join(root, where.file), where.fn)
		for _, l := range layers {
			if !strings.Contains(body, where.qual+l) {
				t.Errorf("%s in %s has no case for %s", where.fn, where.file, l)
			}
		}
	}
}

// TestCockpitLayerLintCatchesAPredicate feeds the lint the form it rejects
// and the forms it allows.
func TestCockpitLayerLintCatchesAPredicate(t *testing.T) {
	src := `package x

func handleInboxInput(o *app.OS) {
	if o.CockpitShipOpen() {
	}
	if o.InboxPeeking() { //tuios:allow-layer a test of the escape
	}
	switch o.CockpitTop() {
	}
}

func other(o *app.OS) { _ = o.CockpitShipOpen() }
`
	got, seen := layerPredicateCalls(t, "x.go", []byte(src), "handleInboxInput")
	if !seen || len(got) != 1 {
		t.Fatalf("the lint found %d calls (function seen %v), want the one CockpitShipOpen in handleInboxInput:\n%s",
			len(got), seen, strings.Join(got, "\n"))
	}
}

// layerPredicateCalls returns file:line for every call of a per-layer
// predicate inside function fn of src, and whether fn is there.
func layerPredicateCalls(t *testing.T, name string, src []byte, fn string) ([]string, bool) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	allowed := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, allowLayer) {
				line := fset.Position(c.Slash).Line
				allowed[line], allowed[line+1] = true, true
			}
		}
	}
	var out []string
	seen := false
	for _, decl := range f.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if !ok || d.Name.Name != fn || d.Body == nil {
			continue
		}
		seen = true
		ast.Inspect(d.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !slices.Contains(cockpitLayerPredicates, sel.Sel.Name) {
				return true
			}
			if line := fset.Position(call.Pos()).Line; !allowed[line] {
				out = append(out, fmt.Sprintf("%s:%d: %s", name, line, sel.Sel.Name))
			}
			return true
		})
	}
	return out, seen
}

// cockpitLayerConsts are the CockpitLayer constants of file, CockpitBase and
// the unexported count left out.
func cockpitLayerConsts(t *testing.T, file string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, decl := range f.Decls {
		g, ok := decl.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, spec := range g.Specs {
			for _, n := range spec.(*ast.ValueSpec).Names {
				if strings.HasPrefix(n.Name, "CockpitLayer") {
					out = append(out, n.Name)
				}
			}
		}
	}
	return out
}

// funcSource is the source text of function fn in file.
func funcSource(t *testing.T, file, fn string) string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == fn {
			return string(src[fset.Position(d.Pos()).Offset:fset.Position(d.End()).Offset])
		}
	}
	t.Fatalf("%s has no function %s", file, fn)
	return ""
}
