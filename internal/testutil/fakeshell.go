// Package testutil provides testing utilities for TUIOS, including a fake shell
// that produces predictable output and sends/receives ANSI sequences.
package testutil

import (
	"fmt"
	"strings"
)

// =============================================================================
// ANSI Escape Sequence Helpers
// =============================================================================

// ANSI escape sequence constants
const (
	ESC = "\x1b"
	CSI = ESC + "["
	OSC = ESC + "]"
	DCS = ESC + "P"
	APC = ESC + "_"
	ST  = ESC + "\\" // String Terminator
	BEL = "\x07"     // Bell (also terminates OSC)
)

// ANSIBuilder helps construct ANSI escape sequences.
type ANSIBuilder struct {
	buf strings.Builder
}

// NewANSIBuilder creates a new ANSI builder.
func NewANSIBuilder() *ANSIBuilder {
	return &ANSIBuilder{}
}

// Text appends plain text.
func (a *ANSIBuilder) Text(s string) *ANSIBuilder {
	a.buf.WriteString(s)
	return a
}

// Newline appends a newline.
func (a *ANSIBuilder) Newline() *ANSIBuilder {
	a.buf.WriteString("\r\n")
	return a
}

// SGR sends a Select Graphic Rendition sequence.
func (a *ANSIBuilder) SGR(params ...int) *ANSIBuilder {
	if len(params) == 0 {
		a.buf.WriteString(CSI + "m")
		return a
	}

	a.buf.WriteString(CSI)
	for i, p := range params {
		if i > 0 {
			a.buf.WriteString(";")
		}
		fmt.Fprintf(&a.buf, "%d", p)
	}
	a.buf.WriteString("m")
	return a
}

// Reset resets all attributes.
func (a *ANSIBuilder) Reset() *ANSIBuilder {
	return a.SGR(0)
}

// FgColor sets foreground to a basic color (30-37, 90-97).
func (a *ANSIBuilder) FgColor(color int) *ANSIBuilder {
	return a.SGR(color)
}

// String returns the built string.
func (a *ANSIBuilder) String() string {
	return a.buf.String()
}
