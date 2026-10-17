package composer

import (
	"strings"
	"testing"
)

func TestBlockOpenExpandRemove(t *testing.T) {
	m := New(Options{})
	typeText(m, "see ")
	m.Paste(lines(42), false)

	// The keys act on the block right before the cursor, and only then.
	if hints := m.Hints(false); hints[0].Key != "ctrl+o" {
		t.Fatalf("hints after a block = %+v, want its keys", hints)
	}
	press(m, "left")
	if m.Open() {
		t.Fatal("ctrl+o opened a block the cursor is not after")
	}
	press(m, "right")

	// Open shows the text, read only, and scrolls.
	press(m, "ctrl+o")
	if !m.Opened() {
		t.Fatal("ctrl+o did not open the block")
	}
	st := testStyles()
	frame := strings.Join(m.Render(60, st, testSurface), "\n")
	if !strings.Contains(frame, "line 01 of the paste") || strings.Contains(frame, "line 42 of the paste") {
		t.Fatalf("the open block does not start at its first line:\n%s", frame)
	}
	press(m, "end")
	frame = strings.Join(m.Render(60, st, testSurface), "\n")
	if !strings.Contains(frame, "line 42 of the paste") {
		t.Fatalf("end did not scroll to the last line:\n%s", frame)
	}
	typeText(m, "x")
	if m.Text() != "see "+lines(42) {
		t.Fatalf("a key in the open block changed the draft: %q", m.Text())
	}
	press(m, "esc")
	if m.Opened() {
		t.Fatal("esc did not close the block")
	}

	// Expand makes the text editable in place, as one undo step.
	press(m, "ctrl+e")
	if m.Blocks() != 0 || m.Text() != "see "+lines(42) {
		t.Fatalf("expand: blocks %d text %q", m.Blocks(), m.Text())
	}
	press(m, "ctrl+z")
	if m.Blocks() != 1 || m.Text() != "see "+lines(42) {
		t.Fatal("undo of an expand did not bring the block back")
	}

	// Remove from the open block.
	press(m, "ctrl+o", "backspace")
	if m.Opened() || m.Blocks() != 0 || m.Text() != "see " {
		t.Fatalf("remove from the open block: opened %v text %q", m.Opened(), m.Text())
	}
}
