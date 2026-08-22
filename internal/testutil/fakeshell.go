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

// CR appends a carriage return.
func (a *ANSIBuilder) CR() *ANSIBuilder {
	a.buf.WriteString("\r")
	return a
}

// LF appends a line feed.
func (a *ANSIBuilder) LF() *ANSIBuilder {
	a.buf.WriteString("\n")
	return a
}

// CursorTo moves cursor to position (1-based).
func (a *ANSIBuilder) CursorTo(row, col int) *ANSIBuilder {
	fmt.Fprintf(&a.buf, "%s%d;%dH", CSI, row, col)
	return a
}

// CursorHome moves cursor to home position (1,1).
func (a *ANSIBuilder) CursorHome() *ANSIBuilder {
	a.buf.WriteString(CSI + "H")
	return a
}

// CursorUp moves cursor up n lines.
func (a *ANSIBuilder) CursorUp(n int) *ANSIBuilder {
	if n == 1 {
		a.buf.WriteString(CSI + "A")
	} else {
		fmt.Fprintf(&a.buf, "%s%dA", CSI, n)
	}
	return a
}

// CursorDown moves cursor down n lines.
func (a *ANSIBuilder) CursorDown(n int) *ANSIBuilder {
	if n == 1 {
		a.buf.WriteString(CSI + "B")
	} else {
		fmt.Fprintf(&a.buf, "%s%dB", CSI, n)
	}
	return a
}

// CursorForward moves cursor forward n columns.
func (a *ANSIBuilder) CursorForward(n int) *ANSIBuilder {
	if n == 1 {
		a.buf.WriteString(CSI + "C")
	} else {
		fmt.Fprintf(&a.buf, "%s%dC", CSI, n)
	}
	return a
}

// CursorBackward moves cursor backward n columns.
func (a *ANSIBuilder) CursorBackward(n int) *ANSIBuilder {
	if n == 1 {
		a.buf.WriteString(CSI + "D")
	} else {
		fmt.Fprintf(&a.buf, "%s%dD", CSI, n)
	}
	return a
}

// ClearScreen clears the entire screen.
func (a *ANSIBuilder) ClearScreen() *ANSIBuilder {
	a.buf.WriteString(CSI + "2J")
	return a
}

// ClearLine clears the entire current line.
func (a *ANSIBuilder) ClearLine() *ANSIBuilder {
	a.buf.WriteString(CSI + "2K")
	return a
}

// ClearToEndOfLine clears from cursor to end of line.
func (a *ANSIBuilder) ClearToEndOfLine() *ANSIBuilder {
	a.buf.WriteString(CSI + "K")
	return a
}

// ClearToEndOfScreen clears from cursor to end of screen.
func (a *ANSIBuilder) ClearToEndOfScreen() *ANSIBuilder {
	a.buf.WriteString(CSI + "J")
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

// Bold enables bold.
func (a *ANSIBuilder) Bold() *ANSIBuilder {
	return a.SGR(1)
}

// Dim enables dim/faint.
func (a *ANSIBuilder) Dim() *ANSIBuilder {
	return a.SGR(2)
}

// Italic enables italic.
func (a *ANSIBuilder) Italic() *ANSIBuilder {
	return a.SGR(3)
}

// Underline enables underline.
func (a *ANSIBuilder) Underline() *ANSIBuilder {
	return a.SGR(4)
}

// Blink enables blink.
func (a *ANSIBuilder) Blink() *ANSIBuilder {
	return a.SGR(5)
}

// Reverse enables reverse video.
func (a *ANSIBuilder) Reverse() *ANSIBuilder {
	return a.SGR(7)
}

// Hidden enables hidden text.
func (a *ANSIBuilder) Hidden() *ANSIBuilder {
	return a.SGR(8)
}

// Strikethrough enables strikethrough.
func (a *ANSIBuilder) Strikethrough() *ANSIBuilder {
	return a.SGR(9)
}

// FgColor sets foreground to a basic color (30-37, 90-97).
func (a *ANSIBuilder) FgColor(color int) *ANSIBuilder {
	return a.SGR(color)
}

// BgColor sets background to a basic color (40-47, 100-107).
func (a *ANSIBuilder) BgColor(color int) *ANSIBuilder {
	return a.SGR(color)
}

// Fg256 sets foreground to a 256-color palette color.
func (a *ANSIBuilder) Fg256(color int) *ANSIBuilder {
	return a.SGR(38, 5, color)
}

// Bg256 sets background to a 256-color palette color.
func (a *ANSIBuilder) Bg256(color int) *ANSIBuilder {
	return a.SGR(48, 5, color)
}

// FgRGB sets foreground to an RGB color.
func (a *ANSIBuilder) FgRGB(r, g, b int) *ANSIBuilder {
	return a.SGR(38, 2, r, g, b)
}

// BgRGB sets background to an RGB color.
func (a *ANSIBuilder) BgRGB(r, g, b int) *ANSIBuilder {
	return a.SGR(48, 2, r, g, b)
}

// SaveCursor saves cursor position.
func (a *ANSIBuilder) SaveCursor() *ANSIBuilder {
	a.buf.WriteString(CSI + "s")
	return a
}

// RestoreCursor restores cursor position.
func (a *ANSIBuilder) RestoreCursor() *ANSIBuilder {
	a.buf.WriteString(CSI + "u")
	return a
}

// ShowCursor shows the cursor.
func (a *ANSIBuilder) ShowCursor() *ANSIBuilder {
	a.buf.WriteString(CSI + "?25h")
	return a
}

// HideCursor hides the cursor.
func (a *ANSIBuilder) HideCursor() *ANSIBuilder {
	a.buf.WriteString(CSI + "?25l")
	return a
}

// AltScreen switches to alternate screen buffer.
func (a *ANSIBuilder) AltScreen() *ANSIBuilder {
	a.buf.WriteString(CSI + "?1049h")
	return a
}

// MainScreen switches back to main screen buffer.
func (a *ANSIBuilder) MainScreen() *ANSIBuilder {
	a.buf.WriteString(CSI + "?1049l")
	return a
}

// EnableBracketedPaste enables bracketed paste mode.
func (a *ANSIBuilder) EnableBracketedPaste() *ANSIBuilder {
	a.buf.WriteString(CSI + "?2004h")
	return a
}

// DisableBracketedPaste disables bracketed paste mode.
func (a *ANSIBuilder) DisableBracketedPaste() *ANSIBuilder {
	a.buf.WriteString(CSI + "?2004l")
	return a
}

// EnableMouse enables mouse tracking.
func (a *ANSIBuilder) EnableMouse() *ANSIBuilder {
	a.buf.WriteString(CSI + "?1000h")
	return a
}

// DisableMouse disables mouse tracking.
func (a *ANSIBuilder) DisableMouse() *ANSIBuilder {
	a.buf.WriteString(CSI + "?1000l")
	return a
}

// EnableSGRMouse enables SGR mouse mode.
func (a *ANSIBuilder) EnableSGRMouse() *ANSIBuilder {
	a.buf.WriteString(CSI + "?1006h")
	return a
}

// DisableSGRMouse disables SGR mouse mode.
func (a *ANSIBuilder) DisableSGRMouse() *ANSIBuilder {
	a.buf.WriteString(CSI + "?1006l")
	return a
}

// ScrollRegion sets the scroll region.
func (a *ANSIBuilder) ScrollRegion(top, bottom int) *ANSIBuilder {
	fmt.Fprintf(&a.buf, "%s%d;%dr", CSI, top, bottom)
	return a
}

// ScrollUp scrolls up n lines.
func (a *ANSIBuilder) ScrollUp(n int) *ANSIBuilder {
	if n == 1 {
		a.buf.WriteString(CSI + "S")
	} else {
		fmt.Fprintf(&a.buf, "%s%dS", CSI, n)
	}
	return a
}

// ScrollDown scrolls down n lines.
func (a *ANSIBuilder) ScrollDown(n int) *ANSIBuilder {
	if n == 1 {
		a.buf.WriteString(CSI + "T")
	} else {
		fmt.Fprintf(&a.buf, "%s%dT", CSI, n)
	}
	return a
}

// InsertLines inserts n blank lines.
func (a *ANSIBuilder) InsertLines(n int) *ANSIBuilder {
	if n == 1 {
		a.buf.WriteString(CSI + "L")
	} else {
		fmt.Fprintf(&a.buf, "%s%dL", CSI, n)
	}
	return a
}

// DeleteLines deletes n lines.
func (a *ANSIBuilder) DeleteLines(n int) *ANSIBuilder {
	if n == 1 {
		a.buf.WriteString(CSI + "M")
	} else {
		fmt.Fprintf(&a.buf, "%s%dM", CSI, n)
	}
	return a
}

// InsertChars inserts n blank characters.
func (a *ANSIBuilder) InsertChars(n int) *ANSIBuilder {
	if n == 1 {
		a.buf.WriteString(CSI + "@")
	} else {
		fmt.Fprintf(&a.buf, "%s%d@", CSI, n)
	}
	return a
}

// DeleteChars deletes n characters.
func (a *ANSIBuilder) DeleteChars(n int) *ANSIBuilder {
	if n == 1 {
		a.buf.WriteString(CSI + "P")
	} else {
		fmt.Fprintf(&a.buf, "%s%dP", CSI, n)
	}
	return a
}

// OSCTitle sets the window title.
func (a *ANSIBuilder) OSCTitle(title string) *ANSIBuilder {
	a.buf.WriteString(OSC + "0;" + title + BEL)
	return a
}

// OSCHyperlink creates a hyperlink.
func (a *ANSIBuilder) OSCHyperlink(url, text string) *ANSIBuilder {
	a.buf.WriteString(OSC + "8;;" + url + ST + text + OSC + "8;;" + ST)
	return a
}

// DeviceStatusReport requests cursor position (DSR).
func (a *ANSIBuilder) DeviceStatusReport() *ANSIBuilder {
	a.buf.WriteString(CSI + "6n")
	return a
}

// RequestTerminalSize requests terminal size (XTWINOPS).
func (a *ANSIBuilder) RequestTerminalSize() *ANSIBuilder {
	a.buf.WriteString(CSI + "18t")
	return a
}

// Raw appends raw bytes.
func (a *ANSIBuilder) Raw(data []byte) *ANSIBuilder {
	a.buf.Write(data)
	return a
}

// RawString appends a raw string.
func (a *ANSIBuilder) RawString(s string) *ANSIBuilder {
	a.buf.WriteString(s)
	return a
}

// String returns the built string.
func (a *ANSIBuilder) String() string {
	return a.buf.String()
}

// Bytes returns the built bytes.
func (a *ANSIBuilder) Bytes() []byte {
	return []byte(a.buf.String())
}

// Reset clears the builder.
func (a *ANSIBuilder) Clear() *ANSIBuilder {
	a.buf.Reset()
	return a
}

// =============================================================================
// Common Test Patterns
// =============================================================================

// ShellPrompt returns a typical shell prompt sequence.
func ShellPrompt(user, host, dir string) string {
	return NewANSIBuilder().
		FgColor(32). // Green
		Text(user + "@" + host).
		Reset().
		Text(":").
		FgColor(34). // Blue
		Text(dir).
		Reset().
		Text("$ ").
		String()
}

// LSOutput simulates `ls` command output with colors.
func LSOutput(files []string, isDir []bool) string {
	b := NewANSIBuilder()
	for i, file := range files {
		if isDir[i] {
			b.FgColor(34).Bold().Text(file).Reset().Text("  ")
		} else {
			b.Text(file).Text("  ")
		}
	}
	return b.Newline().String()
}

// ProgressBar simulates a progress bar update.
func ProgressBar(percent int, width int) string {
	filled := width * percent / 100
	empty := width - filled

	b := NewANSIBuilder().
		CR(). // Return to start of line
		Text("[")

	for range filled {
		b.Text("=")
	}
	if filled < width {
		b.Text(">")
		for range empty - 1 {
			b.Text(" ")
		}
	}

	return b.Text(fmt.Sprintf("] %3d%%", percent)).String()
}
