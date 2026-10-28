// Ported from pele tests/render/mermaid-type.test.ts at 8aff183be57021b533f319a29341a529be934b08.

package sketch

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestMermaidType(t *testing.T) {
	data, err := os.ReadFile("testdata/corpus/railroad-docs.json")
	if err != nil {
		t.Fatal(err)
	}
	var railroad []string
	if err := json.Unmarshal(data, &railroad); err != nil {
		t.Fatal(err)
	}
	railroadWith := func(header string) string {
		for _, src := range railroad {
			if strings.HasPrefix(src, header+"\n") {
				return src
			}
		}
		t.Fatalf("no railroad source starts with %s", header)
		return ""
	}
	// [source, pele's type, what Mermaid 12.1's detectType() returns]
	cases := []struct {
		src string
		typ Type
		mt  MermaidType
	}{
		{"graph TD\n  A --> B", "flowchart", "flowchart-v2"},
		{"flowchart LR\n  A --> B", "flowchart", "flowchart-v2"},
		{"flowchart-elk TD\n  A --> B", "flowchart", "flowchart-elk"},
		{"classDiagram\n  class A", "class", "classDiagram"},
		{"classDiagram-v2\n  class A", "class", "classDiagram"},
		{"stateDiagram\n  [*] --> A", "state", "stateDiagram"},
		{"stateDiagram-v2\n  [*] --> A", "state", "stateDiagram"},
		{railroadWith("railroad-beta"), "railroad", "railroad"},
		{railroadWith("railroad-ebnf-beta"), "railroad", "railroadEbnf"},
		{railroadWith("railroad-abnf-beta"), "railroad", "railroadAbnf"},
		{railroadWith("railroad-peg-beta"), "railroad", "railroadPeg"},
		{"sequenceDiagram\n  A->>B: Hi", "sequence", "sequence"},
		{"pie\n  \"a\": 1", "pie", "pie"},
	}
	for _, c := range cases {
		if typ, _ := Detect(c.src); typ != c.typ {
			t.Errorf("Detect(%q) = %q, want %q", c.src, typ, c.typ)
		}
		if !registered(c.typ) {
			// sketch does not draw this type yet: the error names both types.
			_, err := Render(c.src, RenderOptions{})
			var e *Error
			if !errors.As(err, &e) || e.Code != CodeUnsupported || e.Type != string(c.typ) || e.MermaidType != string(c.mt) {
				t.Errorf("%q: error %#v", c.src, err)
			}
			continue
		}
		out, err := Render(c.src, RenderOptions{})
		if err != nil || out.Type != c.typ || out.MermaidType != c.mt {
			t.Errorf("Render(%q) = %q, %q, %v", c.src, out.Type, out.MermaidType, err)
		}
		d, err := Parse(c.src, ParseOptions{})
		if err != nil || d.MermaidType != c.mt {
			t.Fatalf("Parse(%q): %v", c.src, err)
		}
		out, err = d.RenderSVG(RenderOptions{})
		if err != nil || out.Type != c.typ || out.MermaidType != c.mt {
			t.Errorf("RenderSVG(%q) = %q, %q, %v", c.src, out.Type, out.MermaidType, err)
		}
	}

	// finds the type after front matter, directives and comments
	for src, mt := range map[string]MermaidType{
		"---\ntitle: T\n---\nclassDiagram\n  class A":            "classDiagram",
		"%%{init: {\"theme\": \"dark\"}}%%\ngraph TD\n  A --> B": "flowchart-v2",
		"%% note\nstateDiagram-v2\n  [*] --> A":                  "stateDiagram",
	} {
		if out, err := Render(src, RenderOptions{}); err != nil || out.MermaidType != mt {
			t.Errorf("Render(%q).MermaidType = %q, %v; want %q", src, out.MermaidType, err, mt)
		}
	}

	// leaves the SVG named by pele's type
	out, err := Render("graph TD\n  A --> B", RenderOptions{})
	if err != nil || !strings.Contains(out.Data, `class="pele pele-flowchart"`) || !strings.Contains(out.Data, `aria-roledescription="flowchart"`) {
		t.Errorf("graph TD: %v", err)
	}

	// is on errors once the type is known
	thrown := func(err error) *Error {
		t.Helper()
		var e *Error
		if !errors.As(err, &e) {
			t.Fatalf("not an *Error: %v", err)
		}
		return e
	}
	_, err = Render("flowchart LR\n  A -->", RenderOptions{})
	if e := thrown(err); e.Code != CodeSyntax || e.Type != "flowchart" || e.MermaidType != "flowchart-v2" {
		t.Errorf("syntax error: %#v", e)
	}
	_, err = Render("classDiagram\n  class A", RenderOptions{OutputLimit: 10})
	if e := thrown(err); e.Code != CodeLimit || e.Type != "class" || e.MermaidType != "classDiagram" {
		t.Errorf("limit error: %#v", e)
	}
	_, err = Render("not a diagram", RenderOptions{})
	if e := thrown(err); e.Type != "" || e.MermaidType != "" {
		t.Errorf("no diagram: %#v", e)
	}
	_, err = Parse("graph TD\n  A", ParseOptions{Limit: 5})
	if e := thrown(err); e.Code != CodeLimit || e.MermaidType != "" {
		t.Errorf("source limit: %#v", e)
	}
}
