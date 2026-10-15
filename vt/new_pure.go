//go:build !ghostty

package vt

// New returns the emulator implementation selected at build time. The default
// build is pure Go so that go install works with no toolchain and CGO_ENABLED=0
// cross-compilation keeps working; the ghostty build tag swaps in the
// libghostty-vt backed implementation.
func New(w, h int) Terminal {
	return NewEmulator(w, h)
}

// NewWithScrollback is New with the scrollback depth fixed at construction.
// Every pane is built through here because the libghostty backend can only
// take the depth when its terminal is made; see the ghostty variant.
func NewWithScrollback(w, h, maxLines int) Terminal {
	e := NewEmulator(w, h)
	e.SetScrollbackMaxLines(maxLines)
	return e
}
