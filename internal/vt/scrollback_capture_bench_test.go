package vt

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// scrollbackTexter is the optional interface the daemon's plain capture looks
// for. It is repeated here so these benchmarks build against a tree that does
// not have it, which is how the before and after binaries are compared.
type scrollbackTexter interface {
	AppendScrollbackText(*strings.Builder)
}

// captureText is what the daemon's capture-pane and wait-for window-output do
// for plain text with scrollback: every scrollback line, then the screen.
func captureText(t Terminal) string {
	var sb strings.Builder
	if fast, ok := t.(scrollbackTexter); ok {
		fast.AppendScrollbackText(&sb)
	} else {
		for i := range t.ScrollbackLen() {
			sb.WriteString(t.ScrollbackLine(i).String())
			sb.WriteByte('\n')
		}
	}
	sb.WriteString(t.String())
	return sb.String()
}

// filledEmulator is a pane of the given width whose 10000-line scrollback ring
// has wrapped with build-log lines.
func filledEmulator(width int) *Emulator {
	e := NewEmulator(width, 24)
	e.SetScrollbackMaxLines(DefaultScrollbackSize)
	for i := range DefaultScrollbackSize + 2000 {
		fmt.Fprintf(e, "line %d of some build output text here\r\n", i)
	}
	return e
}

// BenchmarkCaptureScrollback is one check of a wait-for window-output waiter on
// a pane with a full scrollback: capture everything, run a regexp that does not
// match.
func BenchmarkCaptureScrollback(b *testing.B) {
	for _, width := range []int{80, 207} {
		b.Run(fmt.Sprintf("w%d", width), func(b *testing.B) {
			e := filledEmulator(width)
			re := regexp.MustCompile("NEVERMATCHES")
			b.ReportAllocs()
			for b.Loop() {
				if re.MatchString(captureText(e)) {
					b.Fatal("matched")
				}
			}
		})
	}
}

// BenchmarkScrollbackLineString decodes every scrollback line into cells and
// back to text, the path the client's scrollback browser and the ANSI capture
// still take.
func BenchmarkScrollbackLineString(b *testing.B) {
	for _, width := range []int{80, 207} {
		b.Run(fmt.Sprintf("w%d", width), func(b *testing.B) {
			e := filledEmulator(width)
			b.ReportAllocs()
			for b.Loop() {
				n := 0
				for i := range e.ScrollbackLen() {
					n += len(e.ScrollbackLine(i).String())
				}
				if n == 0 {
					b.Fatal("empty scrollback")
				}
			}
		})
	}
}
