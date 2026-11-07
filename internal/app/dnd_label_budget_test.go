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
	// 30 allocations per frame with the names kept for the list, against
	// 2,769 when the whole list is read again on every frame.
	allocsAtMost(t, "renderDnDLabel/900 files", 45, func() {
		_ = m.renderDnDLabel()
	})

	// 60 files is the common drag: parseDroppedPaths refuses more than 64
	// words, so the 900-file list above is labelled from its raw URIs and
	// this one takes the parsed-paths branch.
	b.Reset()
	for i := range 60 {
		fmt.Fprintf(&b, "file:///t/f%d.txt\n", i)
	}
	drag([]byte(b.String()))
	if m.renderDnDLabel() == nil {
		t.Fatal("no label for a started drag")
	}
	// 21 allocations per frame, against 277 when the list is read again on
	// every frame.
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
	// The same slice is the same list: the source's data is decoded into a
	// fresh slice for every list it sends.
	copy(first, "file:///t/x.txt\nfile:///t/y.txt")
	label("same slice", "a.txt, b.txt")
	m.dndDrag.presented[0] = []byte("file:///t/c.txt\nfile:///t/d.txt")
	label("new list of the same length", "c.txt, d.txt")
	m.dndDrag.presented[0] = []byte("file:///t/a.txt\nfile:///t/b.txt\nfile:///t/c.txt\nfile:///t/d.txt")
	label("4 files", "a.txt, b.txt, c.txt +1")
	// A list that decoded to no bytes is a list with no files, and must not
	// be taken for the previous one.
	m.dndDrag.presented[0] = []byte{}
	label("empty list", "no files")
}
