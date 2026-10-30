// Package textbuf is strings.Builder with its write methods out of line, to
// keep the binary small.
//
// The compiler inlines WriteString, WriteByte and WriteRune at every call site,
// with the copy check and the append. Over the packages that use this one, each
// write through it saves about 270 bytes of the file.
//
// Use it where text is built once per action: a CLI command's output, a config
// file, a verb's answer. Keep strings.Builder on a path that writes per cell or
// per frame, where the call would show.
package textbuf

import "strings"

// Builder is a strings.Builder. Its zero value is ready to use, and it must not
// be copied after the first write.
type Builder struct {
	strings.Builder
}

// WriteString appends s, like strings.Builder.WriteString.
//
//go:noinline
func (b *Builder) WriteString(s string) (int, error) {
	return b.Builder.WriteString(s)
}

// WriteByte appends c, like strings.Builder.WriteByte.
//
//go:noinline
func (b *Builder) WriteByte(c byte) error {
	return b.Builder.WriteByte(c)
}

// WriteRune appends r, like strings.Builder.WriteRune.
//
//go:noinline
func (b *Builder) WriteRune(r rune) (int, error) {
	return b.Builder.WriteRune(r)
}
