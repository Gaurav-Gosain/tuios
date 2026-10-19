package text

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"tuios.dev/sketch"
)

// FuzzDraw draws diagram text as box drawing. The seeds are every flowchart and
// sequence source of the corpus. A draw may fail, but must not panic, must take
// bounded time, and must keep within the columns it was given.
func FuzzDraw(f *testing.F) {
	for _, name := range []string{"flowchart", "sequence"} {
		data, err := os.ReadFile("../testdata/corpus/" + name + "-docs.json")
		if err != nil {
			f.Fatal(err)
		}
		var sources []string
		if err := json.Unmarshal(data, &sources); err != nil {
			f.Fatal(err)
		}
		for _, s := range sources {
			f.Add(s, 120)
		}
	}
	f.Fuzz(func(t *testing.T, src string, cols int) {
		if len(src) > 4096 {
			return
		}
		cols = cols%300 + 20
		if cols < 20 {
			cols = 20
		}
		d, err := sketch.Parse(src, sketch.ParseOptions{})
		if err != nil {
			return
		}
		start := time.Now()
		lines, err := Draw(d, Options{Cols: cols})
		if time.Since(start) > 5*time.Second {
			t.Fatalf("draw took %v", time.Since(start))
		}
		if err != nil {
			if !errors.Is(err, ErrTooWide) && !errors.Is(err, ErrUnsupported) {
				var pe *sketch.Error
				if !errors.As(err, &pe) || strings.HasPrefix(pe.Message, "sketch: internal error") {
					t.Fatal(err)
				}
			}
			return
		}
		for _, l := range lines {
			if w := width(l); w > cols {
				t.Fatalf("line of %d cells past %d: %q", w, cols, l)
			}
			if !utf8.ValidString(l) {
				t.Fatalf("invalid UTF-8: %q", l)
			}
		}
	})
}
