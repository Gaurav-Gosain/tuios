package session

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// previewSearchText is size bytes of 'x' with a newline every 80 bytes, with
// each text in plants written at its offset.
func previewSearchText(size int, plants map[int]string) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = 'x'
		if i%80 == 79 {
			b[i] = '\n'
		}
	}
	for at, text := range plants {
		copy(b[at:], text)
	}
	return b
}

// previewLogText is size bytes of log-like ASCII text with capitals and no
// digits, so a lower-case query has to lower real work and a query of digits
// has nothing to find.
func previewLogText(size int) []byte {
	const line = "Alpha Beta GAMMA delta Epsilon Kappa Omega sigma\n"
	b := make([]byte, size)
	for i := range b {
		b[i] = line[i%len(line)]
	}
	return b
}

// TestPreviewSearchAllocatesLittle holds a search in a previewed file to a
// memory budget. A caseless search lowered each 1 MiB chunk into a new slice,
// so it allocated about as many bytes as it read: 21 MiB for a 20 MiB log. A
// query of digits was lowered too, though lowering cannot change what it
// matches. A search or a line seek in a 4 KiB file took a whole 1 MiB chunk
// all the same. The budget is in bytes, which do not depend on the machine's
// speed. The table after it holds the answers the search gives across a
// chunk's edge, at the end of the file, in non-ASCII text, going back and in
// an empty file.
func TestPreviewSearchAllocatesLittle(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int, plants map[int]string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, previewSearchText(size, plants), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	const (
		mi    = 1 << 20
		ki    = 1 << 10
		big   = 20 * mi
		edge  = mi - 3
		short = mi + 100
	)
	logFile := filepath.Join(dir, "log.txt")
	if err := os.WriteFile(logFile, previewLogText(big), 0o600); err != nil {
		t.Fatal(err)
	}
	smallLog := filepath.Join(dir, "small-log.txt")
	if err := os.WriteFile(smallLog, previewLogText(4*ki), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := write("empty.txt", 0, nil)
	overChunk := write("over-chunk.txt", mi+1, map[int]string{0: "edge", mi - 5: "needle"})
	lowerEdge := write("lower-edge.txt", 2*mi, map[int]string{edge: "needle"})
	mixedEdge := write("mixed-edge.txt", 2*mi, map[int]string{edge: "NeEdLe"})
	lowerOnly := write("lower-only.txt", 64*ki, map[int]string{10 * ki: "needle"})
	exact := write("exact.txt", 64*ki, map[int]string{10 * ki: "needle", 40 * ki: "Needle"})
	tail := write("tail.txt", short, map[int]string{short - 6: "needlE"})
	tailZ := write("tail-z.txt", short, map[int]string{short - 3: "xyZ"})
	umlaut := write("umlaut.txt", 64*ki, map[int]string{30 * ki: "Ü"})
	digits := write("digits.txt", 64*ki, map[int]string{20 * ki: "4242"})
	edges := write("edges.txt", 64*ki, map[int]string{16 * ki: "@AZ[`az{"})
	edgesTail := write("edges-tail.txt", short, map[int]string{short - 8: "@AZ[`az{"})
	pair := write("pair.txt", 2*mi, map[int]string{512 * ki: "a", mi + 1: "b"})

	seek := func(t *testing.T, path string, s PreviewSeek) (offset, reached int64) {
		t.Helper()
		type answer struct {
			out map[string]any
			err error
		}
		done := make(chan answer, 1)
		go func() {
			out, err := PreviewLocalSeek(context.Background(), path, s)
			done <- answer{out, err}
		}()
		var out map[string]any
		select {
		case a := <-done:
			if a.err != nil {
				t.Fatal(a.err)
			}
			out = a.out
		case <-time.After(10 * time.Second):
			t.Fatalf("%+v did not return in 10 s", s)
		}
		offset, ok := out["offset"].(int64)
		if !ok {
			t.Fatalf("offset is %T %v, want an int64", out["offset"], out["offset"])
		}
		reached, ok = out["reached"].(int64)
		if !ok {
			t.Fatalf("reached is %T %v, want an int64", out["reached"], out["reached"])
		}
		return offset, reached
	}

	allocated := func() uint64 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.TotalAlloc
	}
	for _, c := range []struct {
		name   string
		path   string
		size   int64
		seek   PreviewSeek
		budget uint64
	}{
		{"zebra", logFile, big, PreviewSeek{Find: "zebra"}, 4 * mi},
		{"4242", logFile, big, PreviewSeek{Find: "4242"}, 3 * mi / 2},
		{"zebra, small file", smallLog, 4 * ki, PreviewSeek{Find: "zebra"}, 64 * ki},
		{"4242, small file", smallLog, 4 * ki, PreviewSeek{Find: "4242"}, 64 * ki},
		{"line, small file", smallLog, 4 * ki, PreviewSeek{Line: 1000}, 64 * ki},
	} {
		t.Run("budget "+c.name, func(t *testing.T) {
			least := uint64(math.MaxUint64)
			for range 3 {
				runtime.GC()
				before := allocated()
				offset, reached := seek(t, c.path, c.seek)
				used := allocated() - before
				if offset != -1 || reached != c.size {
					t.Fatalf("%+v in a file without it: offset %d, reached %d, want -1 and %d", c.seek, offset, reached, c.size)
				}
				least = min(least, used)
			}
			if least > c.budget {
				t.Fatalf("seeking %+v in %d KiB allocated %.1f KiB, the budget is %.1f KiB",
					c.seek, c.size/ki, float64(least)/ki, float64(c.budget)/ki)
			}
		})
	}

	for _, c := range []struct {
		name string
		path string
		seek PreviewSeek
		want int64 // the match's byte, or -1 for not found
		end  int64 // where a search without a match stopped
	}{
		{"lower case across the edge", lowerEdge, PreviewSeek{Find: "needle"}, edge, 0},
		{"lower case across the edge, back", lowerEdge, PreviewSeek{Find: "needle", From: 2 * mi, Back: true}, edge, 0},
		{"mixed case across the edge", mixedEdge, PreviewSeek{Find: "needle"}, edge, 0},
		{"a capital keeps case", lowerOnly, PreviewSeek{Find: "Needle"}, -1, 64 * ki},
		{"a capital finds its own case", exact, PreviewSeek{Find: "Needle"}, 40 * ki, 0},
		{"a hit on the last byte", tail, PreviewSeek{Find: "needle"}, short - 6, 0},
		{"a Z in the last bytes", tailZ, PreviewSeek{Find: "xyz"}, short - 3, 0},
		{"the edges of A-Z", edges, PreviewSeek{Find: "@az[`az{"}, 16 * ki, 0},
		{"the edges of A-Z on the last byte", edgesTail, PreviewSeek{Find: "@az[`az{"}, short - 8, 0},
		{"non-ASCII", umlaut, PreviewSeek{Find: "ü"}, 30 * ki, 0},
		{"digits", digits, PreviewSeek{Find: "4242"}, 20 * ki, 0},
		{"back, apart", pair, PreviewSeek{Find: "ab", From: 3 * mi / 2, Back: true}, -1, 0},
		{"back, apart, capitals", pair, PreviewSeek{Find: "AB", From: 3 * mi / 2, Back: true}, -1, 0},
		{"back, the later one", pair, PreviewSeek{Find: "b", From: 3 * mi / 2, Back: true}, mi + 1, 0},
		{"back, the earlier one", pair, PreviewSeek{Find: "a", From: 3 * mi / 2, Back: true}, 512 * ki, 0},
		{"one byte over a chunk, across the edge", overChunk, PreviewSeek{Find: "needle"}, mi - 5, 0},
		{"one byte over a chunk, across the edge, back", overChunk, PreviewSeek{Find: "edge", From: mi + 1, Back: true}, 0, 0},
		{"an empty file", empty, PreviewSeek{Find: "zebra"}, -1, 0},
		{"an empty file, back", empty, PreviewSeek{Find: "zebra", Back: true}, -1, 0},
		{"an empty file, a line", empty, PreviewSeek{Line: 3}, -1, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			offset, reached := seek(t, c.path, c.seek)
			switch {
			case c.want < 0 && offset != -1:
				t.Fatalf("%q found at %d (line %d), want not found", c.seek.Find, reached, offset)
			case c.want < 0 && reached != c.end:
				t.Fatalf("%q not found, but the search stopped at %d, want %d", c.seek.Find, reached, c.end)
			case c.want >= 0 && (offset == -1 || reached != c.want):
				t.Fatalf("%q: offset %d, reached %d, want found at %d", c.seek.Find, offset, reached, c.want)
			}
		})
	}
}
