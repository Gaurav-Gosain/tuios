package sketch

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// TestGoldenErrors renders each case of testdata/errors/cases.json and compares
// the error with the one pele throws (testdata/errors/want.json, written by
// testdata/errors/gen.mjs): the message, the code, the type, and the line, column
// and snippet in the source as written. A case pele draws must draw.
func TestGoldenErrors(t *testing.T) {
	type errCase struct {
		Src      string `json:"src"`
		MaxEdges int    `json:"maxEdges"`
	}
	type errWant struct {
		OK          bool   `json:"ok"`
		Message     string `json:"message"`
		Code        string `json:"code"`
		Type        string `json:"type"`
		MermaidType string `json:"mermaidType"`
		Line        int    `json:"line"`
		Column      int    `json:"column"`
		Snippet     string `json:"snippet"`
	}
	var cases []errCase
	var want []errWant
	for path, v := range map[string]any{"testdata/errors/cases.json": &cases, "testdata/errors/want.json": &want} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, v); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if len(cases) != len(want) {
		t.Fatalf("%d cases, %d answers: run testdata/errors/gen.mjs", len(cases), len(want))
	}
	for i, c := range cases {
		w := want[i]
		_, err := Render(c.Src, RenderOptions{Now: goldenNow, Location: goldenZone, MaxEdges: c.MaxEdges})
		if w.OK {
			if err != nil {
				t.Errorf("case %d %q: pele draws it, sketch gives %v", i, c.Src, err)
			}
			continue
		}
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("case %d %q: got %v, want %q", i, c.Src, err, w.Message)
			continue
		}
		got := errWant{Message: e.Message, Code: string(e.Code), Type: e.Type, MermaidType: e.MermaidType, Line: e.Line, Column: e.Column, Snippet: e.Snippet}
		if got != w {
			t.Errorf("case %d %q:\ngot  %+v\nwant %+v", i, c.Src, got, w)
		}
	}
}
