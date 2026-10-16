package composer

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// BenchmarkKeystroke16KiB is the composer's perf budget: one typed key and
// the frame it causes, in a draft of 16 KiB of text held inline, the most the
// Inbox reply sends. The design's budget is 200 µs a key.
func BenchmarkKeystroke16KiB(b *testing.B) {
	m := New(Options{})
	line := "the quick brown fox jumps over the lazy dog, 0123456789 and more\n"
	m.Insert(strings.Repeat(line, (16<<10)/len(line)), false)
	m.cur = len(m.cells) / 2
	pal := theme.UI()
	a := tea.KeyPressMsg{Code: 'a', Text: "a"}
	bs := tea.KeyPressMsg{Code: tea.KeyBackspace}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		if i%2 == 0 {
			m.Update(a, false)
		} else {
			m.Update(bs, false)
		}
		i++
		m.Render(80, pal, pal.Surface)
	}
}
