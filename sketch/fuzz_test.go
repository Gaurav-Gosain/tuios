package sketch

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// FuzzRender drives the whole pipeline with text: preprocess, detect, each
// ported type's lexer and parser, layout and SVG. The seeds are the corpus of
// every ported type and the security templates of each. A render may fail with a PeleError, but must not panic, must
// stay inert, and must finish in bounded time.
func FuzzRender(f *testing.F) {
	for _, c := range loadGoldens(f) {
		if done[c.name] {
			f.Add(c.source)
		}
	}
	for _, templates := range [][]func(string) string{flowchartTemplates, sequenceTemplates, stateTemplates,
		classTemplates, erTemplates, ganttTemplates, mindmapTemplates} {
		for _, template := range templates {
			f.Add(template(PAYLOADS[1]))
			f.Add(template("a&b"))
		}
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
	})
}
