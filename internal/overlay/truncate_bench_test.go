package overlay

import (
	"strings"
	"testing"
)

// BenchmarkTruncate cuts a line much longer than its budget, the case a
// navigator snippet, a rail row or a dock entry hits on every frame it is
// drawn.
func BenchmarkTruncate(b *testing.B) {
	cases := []struct {
		name  string
		s     string
		width int
	}{
		{"short-over", "a pane name a little too long", 24},
		{"line-80-to-30", strings.Repeat("go test ./internal/... ", 4)[:80], 30},
		{"line-200-to-40", strings.Repeat("building package number 42 ok ", 7)[:200], 40},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = Truncate(c.s, c.width)
			}
		})
	}
}
