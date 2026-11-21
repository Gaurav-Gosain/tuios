package app

import (
	"fmt"
	"strings"
	"testing"
)

// TestDnDLabelAllocationsDoNotScaleWithTheList pins the cost of the label a
// drag carries. renderDnDLabel runs on every composed frame while a drag is
// in flight, and the names in a URI list the source sent ahead do not change
// between frames, so the list is read once per list, not once per frame.
// The label must still follow a new list the moment the source sends one.
func TestDnDLabelAllocationsDoNotScaleWithTheList(t *testing.T) {
	m := newTestOS(nil)
	m.Width, m.Height = 200, 60
	m.LastMouseX, m.LastMouseY = 100, 30
	drag := func(list []byte) {
		m.dndDrag = &dndDrag{
			phase:     dndStarted,
			source:    "no-such-pane",
			mimes:     []string{"text/uri-list"},
			presented: map[int][]byte{0: list},
		}
	}

	var b strings.Builder
	for i := range 900 {
		fmt.Fprintf(&b, "file:///t/f%03d.txt\n", i)
	}
	drag([]byte(b.String()))
	if m.renderDnDLabel() == nil {
		t.Fatal("no label for a started drag")
	}
	allocsAtMost(t, "renderDnDLabel/900 files", 45, func() {
		_ = m.renderDnDLabel()
	})

	b.Reset()
	for i := range 60 {
		fmt.Fprintf(&b, "file:///t/f%d.txt\n", i)
	}
	drag([]byte(b.String()))
	if m.renderDnDLabel() == nil {
		t.Fatal("no label for a started drag")
	}
	allocsAtMost(t, "renderDnDLabel/60 files", 32, func() {
		_ = m.renderDnDLabel()
	})

	label := func(step, want string) {
		t.Helper()
		for range 2 {
			if got := m.dndLabelWhat(); got != want {
				t.Errorf("%s: label %q, want %q", step, got, want)
			}
		}
	}
	first := []byte("file:///t/a.txt\nfile:///t/b.txt")
	drag(first)
	label("2 files", "a.txt, b.txt")
	copy(first, "file:///t/x.txt\nfile:///t/y.txt")
	label("same slice", "a.txt, b.txt")
	m.dndDrag.presented[0] = []byte("file:///t/c.txt\nfile:///t/d.txt")
	label("new list of the same length", "c.txt, d.txt")
	m.dndDrag.presented[0] = []byte("file:///t/a.txt\nfile:///t/b.txt\nfile:///t/c.txt\nfile:///t/d.txt")
	label("4 files", "a.txt, b.txt, c.txt +1")
	m.dndDrag.presented[0] = []byte{}
	label("empty list", "no files")
}
