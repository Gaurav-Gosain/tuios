package session

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestErrorCodeCatalogHasEveryErrVerbConstant reads the package source for
// every ErrVerb* constant and requires its code in the list-verbs catalogue. A
// code defined next to the verbs that raise it, such as the worktree ones, is
// easy to leave out of the catalogue, and list-verbs then never publishes it.
func TestErrorCodeCatalogHasEveryErrVerbConstant(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	catalogued := VerbErrorCodes()
	fset := token.NewFileSet()
	found := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, ident := range vs.Names {
					if !strings.HasPrefix(ident.Name, "ErrVerb") || i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					code, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("%s: %v", ident.Name, err)
					}
					found++
					if !slices.Contains(catalogued, code) {
						t.Errorf("%s (%q) is defined in %s but missing from errorCodeCatalog", ident.Name, code, name)
					}
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("found no ErrVerb constants, so the source scan is broken")
	}
}

// TestListVerbsAcceptedValuesMatchTheImplementation guards the one way a schema
// silently rots: the accepted-value lists must be the same lists the handlers
// actually enforce.
func TestListVerbsAcceptedValuesMatchTheImplementation(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"list-verbs","params":{"verb":"wait-for"}}`))
	verbs, ok := res["verbs"].([]any)
	if !ok || len(verbs) != 1 {
		t.Fatalf("naming a verb should return exactly that verb, got %v", res["verbs"])
	}
	doc := verbs[0].(map[string]any)
	if doc["verb"] != "wait-for" {
		t.Fatalf("described the wrong verb: %v", doc["verb"])
	}

	var accepted []string
	for _, raw := range doc["params"].([]any) {
		p := raw.(map[string]any)
		if p["name"] != "condition" {
			continue
		}
		for _, v := range p["accepted"].([]any) {
			accepted = append(accepted, v.(string))
		}
	}
	if !slices.Equal(accepted, waitConditions) {
		t.Fatalf("documented conditions %v do not match the implemented set %v", accepted, waitConditions)
	}

	// And each documented condition must actually be accepted by the handler:
	// a rejected one comes back as invalid_params naming the condition param.
	for _, condition := range accepted {
		resp := c.call(t, `{"id":2,"verb":"wait-for","params":{"condition":"`+condition+`","timeout":1}}`)
		e, ok := resp["error"].(map[string]any)
		if !ok {
			continue // it matched immediately, which also means it was accepted
		}
		if code, _ := e["code"].(string); code == ErrVerbInvalidParams {
			hint, _ := e["hint"].(map[string]any)
			if param, _ := hint["param"].(string); param == "condition" {
				t.Errorf("condition %q is documented as accepted but the handler rejects it: %v", condition, e)
			}
		}
	}
}

// TestCaptureSourcesMatchTheImplementation is the capture-pane half of the same
// contract: the documented source list must equal the enforced set, and every
// documented source must survive the handler's validation. This is what stops a
// source from being advertised while doing nothing, which is how
// "recent-unwrapped" became a silent alias for "recent".
func TestCaptureSourcesMatchTheImplementation(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"list-verbs","params":{"verb":"capture-pane"}}`))
	doc := res["verbs"].([]any)[0].(map[string]any)

	var accepted []string
	for _, raw := range doc["params"].([]any) {
		p := raw.(map[string]any)
		if p["name"] != "source" {
			continue
		}
		for _, v := range p["accepted"].([]any) {
			accepted = append(accepted, v.(string))
		}
	}
	if !slices.Equal(accepted, captureSources) {
		t.Fatalf("documented sources %v do not match the implemented set %v", accepted, captureSources)
	}

	// Each documented source must capture rather than be rejected, and must be
	// echoed back as the source that was actually used.
	for _, source := range accepted {
		if source == captureLastCommand {
			// It reads a finished command, and the fixture's shell marks
			// none, so here it has to say so rather than return a screen.
			// It captures in verb_run_test.go.
			resp := c.call(t, `{"id":2,"verb":"capture-pane","params":{"session":"work","source":"`+source+`"}}`)
			if code := errCode(t, resp); code != ErrVerbNoShellIntegration {
				t.Errorf("capture with source %q and no marks: code %q, want %q", source, code, ErrVerbNoShellIntegration)
			}
			continue
		}
		res := result(t, c.call(t, `{"id":2,"verb":"capture-pane","params":{"session":"work","source":"`+source+`"}}`))
		if got, _ := res["source"].(string); got != source {
			t.Errorf("capture with source %q reported source %q", source, got)
		}
	}

	// A retired source must not merely be undocumented, it must be refused, so a
	// caller still passing it finds out instead of silently getting "recent".
	for retired := range retiredCaptureSources {
		if slices.Contains(captureSources, retired) {
			t.Errorf("%q is listed as both accepted and retired", retired)
		}
		resp := c.call(t, `{"id":3,"verb":"capture-pane","params":{"session":"work","source":"`+retired+`"}}`)
		e, ok := resp["error"].(map[string]any)
		if !ok {
			t.Errorf("retired source %q was accepted: %v", retired, resp)
			continue
		}
		if code, _ := e["code"].(string); code != ErrVerbInvalidParams {
			t.Errorf("retired source %q rejected with %q, want %q", retired, code, ErrVerbInvalidParams)
		}
	}
}
