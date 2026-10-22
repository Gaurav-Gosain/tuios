package sketch

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// FuzzRender drives the whole pipeline with text: preprocess, detect, each
// ported type's lexer and parser, layout and SVG. The seeds are the corpus of
// every ported type, the security templates of each and the extra cases. A render may fail with a PeleError, but must not panic, must
// stay inert, and must finish in bounded time.
func FuzzRender(f *testing.F) {
	for _, c := range loadGoldens(f) {
		if done[c.name] {
			f.Add(c.source)
		}
	}
	for _, templates := range [][]func(string) string{flowchartTemplates, sequenceTemplates, stateTemplates,
		classTemplates, erTemplates, ganttTemplates, mindmapTemplates, xychartTemplates, timelineTemplates, quadrantTemplates, journeyTemplates, kanbanTemplates, sankeyTemplates,
		blockTemplates, architectureTemplates, requirementTemplates, c4Templates, packetTemplates, radarTemplates, treemapTemplates,
		infoTemplates, treeviewTemplates, ishikawaTemplates, cynefinTemplates, eventmodelingTemplates, vennTemplates, wardleyTemplates, railroadTemplates, swimlaneTemplates, usecaseTemplates, agentflowTemplates} {
		for _, template := range templates {
			f.Add(template(PAYLOADS[1]))
			f.Add(template("a&b"))
		}
	}
	// The extra cases reach label markup, entities and layouts the corpus does not.
	extras, _ := filepath.Glob("testdata/extra/*.json")
	for _, file := range extras {
		data, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		var cases []struct {
			Src string `json:"src"`
		}
		if err := json.Unmarshal(data, &cases); err != nil {
			f.Fatalf("%s: %v", file, err)
		}
		for _, c := range cases {
			f.Add(c.Src)
		}
	}
	// $$...$$ labels, which FuzzRender also draws with a resolver.
	for _, typ := range mathTypes {
		for _, template := range typ.templates {
			f.Add(template("a $$" + PAYLOADS[1] + "$$ $$x^2$$ $$none$$"))
		}
	}
	for _, template := range gitTemplates {
		f.Add(template(`"`+PAYLOADS[1]+`"`, PAYLOADS[1]))
		f.Add(template(`"a&b"`, "a&b"))
	}
	f.Fuzz(func(t *testing.T, src string) {
		// Large inputs only slow the search down; the limits cover them.
		if len(src) > 4096 {
			return
		}
		start := time.Now()
		out, err := Render(src, RenderOptions{})
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("render took %v", d)
		}
		if err != nil {
			var pe *Error
			if !errors.As(err, &pe) || strings.HasPrefix(pe.Message, "sketch: internal error") {
				t.Fatalf("%v", err)
			}
			return
		}
		if problem := inertProblem(out.Data); problem != "" {
			t.Fatalf("output is not inert: %s", problem)
		}
		if !strings.Contains(src, "$$") {
			return
		}
		start = time.Now()
		out, err = Render(src, RenderOptions{Math: fuzzMath})
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("render with math took %v", d)
		}
		if err != nil {
			t.Fatalf("with math: %v", err)
		}
		if problem := mathInertProblem(out.Data); problem != "" {
			t.Fatalf("output with math is not inert: %s", problem)
		}
	})
}
