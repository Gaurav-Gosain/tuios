package text

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"tuios.dev/sketch"
)

var update = flag.Bool("update", false, "rewrite the pinned drawings under testdata/pinned")

// TestPinnedText draws each source under testdata/pinned and wants the lines
// pinned beside it, without colour. A source is NAME-COLS.mmd: COLS is
// Options.Cols. Its drawing is NAME-COLS.txt. A source named
// NAME-COLS-color.mmd is drawn with colour, and pinned without the SGR
// sequences, for the drawings that change with colour. Run with -update to
// write the drawings again, then read the diff.
func TestPinnedText(t *testing.T) {
	sources, err := filepath.Glob("testdata/pinned/*.mmd")
	if err != nil || len(sources) == 0 {
		t.Fatalf("no sources: %v", err)
	}
	for _, path := range sources {
		name := strings.TrimSuffix(filepath.Base(path), ".mmd")
		t.Run(name, func(t *testing.T) {
			base, color := strings.CutSuffix(name, "-color")
			cols, err := strconv.Atoi(base[strings.LastIndexByte(base, '-')+1:])
			if err != nil {
				t.Fatalf("no column count in the name: %v", err)
			}
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			d, err := sketch.Parse(string(src), sketch.ParseOptions{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			lines, err := Draw(d, Options{Cols: cols, Color: color})
			if err != nil {
				t.Fatalf("draw: %v", err)
			}
			got := strings.Join(lines, "\n") + "\n"
			if color {
				got = reSGR.ReplaceAllString(got, "")
			}
			want := strings.TrimSuffix(path, ".mmd") + ".txt"
			if *update {
				if err := os.WriteFile(want, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			data, err := os.ReadFile(want)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(data) {
				t.Errorf("drawing differs from %s:\n%s", want, got)
			}
		})
	}
}
