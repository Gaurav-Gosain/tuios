//go:build ghostty

package vt

// New returns the libghostty-vt backed implementation. See new_pure.go for
// the default; exactly one of the two is compiled into a binary.
func New(w, h int) Terminal {
	return NewGhosttyTerminal(w, h)
}
