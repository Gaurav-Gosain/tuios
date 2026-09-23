package session

import "testing"

// TestLogBufferAllocatesOnFirstAdd holds the lazy allocation: a new buffer
// reads as empty without its entries, and the first Add makes them and wraps
// as before.
func TestLogBufferAllocatesOnFirstAdd(t *testing.T) {
	b := NewLogBuffer(3)
	if b.entries != nil {
		t.Fatal("a new buffer allocated its entries before any Add")
	}
	if got := b.GetAll(); got != nil {
		t.Fatalf("GetAll on a new buffer = %v, want nil", got)
	}
	if got := b.GetLast(5); got != nil {
		t.Fatalf("GetLast on a new buffer = %v, want nil", got)
	}
	b.Clear()

	for _, msg := range []string{"a", "b", "c", "d"} {
		b.Add("info", msg)
	}
	if len(b.entries) != 3 {
		t.Fatalf("entries has %d slots, want 3", len(b.entries))
	}
	var got []string
	for _, e := range b.GetAll() {
		got = append(got, e.Message)
	}
	if len(got) != 3 || got[0] != "b" || got[1] != "c" || got[2] != "d" {
		t.Fatalf("GetAll after wrapping = %q, want [b c d]", got)
	}
	if last := b.GetLast(1); len(last) != 1 || last[0].Message != "d" {
		t.Fatalf("GetLast(1) = %v, want d", last)
	}
}
