// Package termio writes a client's frames to its terminal: the diff of the
// composed cells, the terminal modes and the cursor.
//
// It does the work of Bubble Tea's renderer for a program run with
// tea.WithoutRenderer. That renderer takes the frame as a string and parses it
// back into cells before it diffs them; a Screen diffs the cells the
// compositor already drew. The mode rules (alternate screen, mouse, focus,
// bracketed paste, keyboard enhancements, title, cursor) are the renderer's,
// in its order, so a terminal sees the same sequences.
package termio

import (
	"bytes"
	"fmt"
	"image/color"
	"io"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Screen is one terminal's view of the frames written to it. Its methods run
// on the program's Update goroutine; only the write happens elsewhere.
type Screen struct {
	scr  *uv.TerminalRenderer
	body bytes.Buffer
	out  *writer

	rb      uv.RenderBuffer
	touched []uv.LineData
	// scratch holds a frame that arrived as a string: the screen saver, the
	// fullscreen fast path, the crash screen.
	scratch uv.ScreenBuffer
	w, h    int

	// started: the alternate screen is entered and its sequences are in
	// pre. query: ask for synchronized output and the unicode core at
	// start. syncd: the terminal has synchronized output.
	started, query, syncd bool
	pre                   bytes.Buffer
	// diffed: body holds the diff of the frame composed last, not yet
	// written.
	diffed bool

	last   *tea.View
	closed bool
}

// Output says how the bytes reach the terminal, which decides how the diff
// may move the cursor. Bubble Tea derives both from its input.
type Output struct {
	// Raw: the program's input is a terminal it puts in raw mode, so the
	// output is not post-processed. When it is not a terminal (an SSH
	// channel with no real PTY, the browser build), a newline reaches the
	// terminal as CR LF, and backspace is not trusted to move the cursor.
	Raw bool
}

// New makes the Screen for a terminal reached through out. environ is the
// program's environment: TERM decides which sequences the diff may use, as it
// does for Bubble Tea's renderer.
func New(out io.Writer, environ []string, profile colorprofile.Profile, o Output) *Screen {
	s := &Screen{out: newWriter(out)}
	s.scr = uv.NewTerminalRenderer(&s.body, environ)
	s.scr.SetColorProfile(profile)
	// Hard tabs stay off: tmux drops the background under one. Backspace
	// moves the cursor on a raw Linux terminal, whose termios leaves BS0
	// set, as Bubble Tea finds it there.
	s.scr.SetTabStops(-1)
	s.scr.SetBackspace(o.Raw && runtime.GOOS == "linux")
	s.scr.SetMapNewline(!o.Raw && runtime.GOOS != "windows")
	s.scr.SetScrollOptim(true)
	s.query = shouldQuerySync(environ)
	return s
}

// shouldQuerySync is Bubble Tea's rule for asking a terminal about
// synchronized output: some terminals answer the query on the screen.
func shouldQuerySync(environ []string) bool {
	env := uv.Environ(environ)
	termType := env.Getenv("TERM")
	termProg, okTermProg := env.LookupEnv("TERM_PROGRAM")
	_, okSSHTTY := env.LookupEnv("SSH_TTY")
	_, okWT := env.LookupEnv("WT_SESSION")
	return (!okTermProg && !okSSHTTY) || okWT ||
		(okTermProg && !strings.Contains(termProg, "Apple") && !okSSHTTY) ||
		strings.Contains(termType, "ghostty") || strings.Contains(termType, "wezterm") ||
		strings.Contains(termType, "alacritty") || strings.Contains(termType, "kitty") ||
		strings.Contains(termType, "rio")
}

// SetColorProfile sets the profile the diff steps colours down to.
func (s *Screen) SetColorProfile(p colorprofile.Profile) { s.scr.SetColorProfile(p) }

// ModeReport takes the terminal's answers to the start query.
func (s *Screen) ModeReport(msg tea.ModeReportMsg) {
	switch msg.Mode {
	case ansi.ModeSynchronizedOutput:
		if msg.Value == ansi.ModeReset {
			s.syncd = true
		}
	case ansi.ModeUnicodeCore:
		if msg.Value == ansi.ModeReset || msg.Value == ansi.ModeSet || msg.Value == ansi.ModePermanentlySet {
			s.scr.SetWidthMethod(ansi.GraphemeWidth)
		}
	}
}

// start enters the alternate screen the way Bubble Tea's first flush does.
func (s *Screen) start() {
	if s.started {
		return
	}
	s.started = true
	s.scr.SaveCursor()
	s.pre.WriteString(ansi.SetModeAltScreenSaveCursor)
	if s.query {
		s.pre.WriteString(ansi.RequestModeSynchronizedOutput + ansi.RequestModeUnicodeCore)
	}
	s.scr.SetFullscreen(true)
	s.scr.SetRelativeCursor(false)
	s.scr.Erase()
}

// Frame diffs buf against what the terminal shows and reports whether the
// terminal needs any of it. A frame that diffs to nothing is a frame equal to
// the last one.
func (s *Screen) Frame(buf *uv.Buffer) bool {
	if s.closed {
		return false
	}
	s.start()
	w, h := buf.Width(), buf.Height()
	if w != s.w || h != s.h {
		if s.w != 0 {
			s.scr.Resize(w, h)
			s.scr.Erase()
		}
		s.w, s.h = w, h
	}
	s.rb.Buffer = buf
	if len(s.touched) != h {
		s.touched = make([]uv.LineData, h)
		s.rb.Touched = make([]*uv.LineData, h)
	}
	for i := range s.rb.Touched {
		s.touched[i] = uv.LineData{FirstCell: 0, LastCell: w}
		s.rb.Touched[i] = &s.touched[i]
	}
	n := s.body.Len()
	s.scr.Render(&s.rb)
	_ = s.scr.Flush()
	s.diffed = true
	return s.body.Len() > n
}

// FrameString is Frame for a frame drawn as a string, w by h cells.
func (s *Screen) FrameString(content string, w, h int) bool {
	w, h = max(w, 1), max(h, 1)
	if s.scratch.Buffer == nil || s.scratch.Width() != w || s.scratch.Height() != h {
		s.scratch = uv.NewScreenBuffer(w, h)
	}
	s.scratch.Clear()
	uv.NewStyledString(content).Draw(s.scratch, s.scratch.Bounds())
	return s.Frame(s.scratch.Buffer)
}

// Flush writes what changed since the last flush: the modes v asks for, the
// cells Frame diffed, and the cursor. framed says a frame was composed since
// the last flush; such a write goes out even when empty, because the writer
// behind it may owe graphics to a frame write.
func (s *Screen) Flush(v tea.View, framed bool) {
	if s.closed {
		return
	}
	if !framed && !s.diffed && s.pre.Len() == 0 && s.body.Len() == 0 && modesEqual(s.last, &v) {
		return
	}
	s.start()
	lv := s.last
	first := lv == nil
	var modes, post bytes.Buffer
	writeModes(&modes, lv, &v)

	if v.Cursor != nil {
		s.scr.MoveTo(v.Cursor.X, v.Cursor.Y)
		_ = s.scr.Flush()
	}

	// Cursor visibility as Bubble Tea writes it: with synchronized output,
	// hidden before and shown after only when it changes; without it, also
	// around every write while the cursor is shown, against flicker.
	didShow := !first && lv.Cursor != nil
	show := v.Cursor != nil
	change := first || didShow != show
	around := !s.syncd && s.body.Len() > 0 && show && didShow
	if (change && !show) || around {
		modes.WriteString(ansi.ResetModeTextCursorEnable)
	}
	if (change && show) || around {
		post.WriteString(ansi.SetModeTextCursorEnable)
	}

	out := make([]byte, 0, s.pre.Len()+modes.Len()+s.body.Len()+post.Len())
	out = append(out, s.pre.Bytes()...)
	out = append(out, modes.Bytes()...)
	out = append(out, s.body.Bytes()...)
	out = append(out, post.Bytes()...)
	if len(out) > 0 || framed {
		s.out.enqueue(out)
	}
	s.pre.Reset()
	s.body.Reset()
	s.diffed = false
	keep := v
	keep.Content = ""
	s.last = &keep
}

// Close puts the terminal back the way Bubble Tea's renderer does when an
// alternate screen program ends, after the queued frames are written. The
// Screen draws nothing after it.
func (s *Screen) Close() {
	if s.closed {
		return
	}
	s.closed = true
	s.out.stop()
	lv := s.last
	if lv == nil {
		return
	}
	var b bytes.Buffer
	b.WriteString(ansi.ResetModifyOtherKeys)
	b.WriteString(ansi.PopKittyKeyboard(1))
	s.scr.MoveTo(0, max(s.h-1, 0))
	_ = s.scr.Flush()
	b.Write(s.body.Bytes())
	s.body.Reset()
	b.WriteString(ansi.ResetModeAltScreenSaveCursor)
	if lv.Cursor == nil {
		b.WriteString(ansi.SetModeTextCursorEnable)
	}
	if !lv.DisableBracketedPasteMode {
		b.WriteString(ansi.ResetModeBracketedPaste)
	}
	if lv.ReportFocus {
		b.WriteString(ansi.ResetModeFocusEvent)
	}
	if lv.MouseMode != tea.MouseModeNone {
		b.WriteString(ansi.ResetModeMouseButtonEvent + ansi.ResetModeMouseAnyEvent + ansi.ResetModeMouseExtSgr)
	}
	if lv.WindowTitle != "" {
		b.WriteString(ansi.SetWindowTitle(""))
	}
	if cursorStyle(lv.Cursor) != 0 {
		b.WriteString(ansi.SetCursorStyle(0))
	}
	if lv.Cursor != nil && lv.Cursor.Color != nil {
		b.WriteString(ansi.ResetCursorColor)
	}
	s.out.writeNow(b.Bytes())
}

// writeModes appends the mode changes from lv to v, in Bubble Tea's order.
// A nil lv is the first frame.
func writeModes(b *bytes.Buffer, lv, v *tea.View) {
	first := lv == nil
	if first || v.DisableBracketedPasteMode != lv.DisableBracketedPasteMode {
		if !v.DisableBracketedPasteMode {
			b.WriteString(ansi.SetModeBracketedPaste)
		} else if !first {
			b.WriteString(ansi.ResetModeBracketedPaste)
		}
	}
	if first || v.ReportFocus != lv.ReportFocus {
		if v.ReportFocus {
			b.WriteString(ansi.SetModeFocusEvent)
		} else if !first {
			b.WriteString(ansi.ResetModeFocusEvent)
		}
	}
	if first || v.MouseMode != lv.MouseMode {
		switch v.MouseMode {
		case tea.MouseModeNone:
			if !first && lv.MouseMode != tea.MouseModeNone {
				b.WriteString(ansi.ResetModeMouseButtonEvent + ansi.ResetModeMouseAnyEvent + ansi.ResetModeMouseExtSgr)
			}
		case tea.MouseModeCellMotion:
			if !first && lv.MouseMode == tea.MouseModeAllMotion {
				b.WriteString(ansi.ResetModeMouseAnyEvent)
			}
			b.WriteString(ansi.SetModeMouseButtonEvent + ansi.SetModeMouseExtSgr)
		case tea.MouseModeAllMotion:
			if !first && lv.MouseMode == tea.MouseModeCellMotion {
				b.WriteString(ansi.ResetModeMouseButtonEvent)
			}
			b.WriteString(ansi.SetModeMouseAnyEvent + ansi.SetModeMouseExtSgr)
		}
	}
	if (first && v.WindowTitle != "") || (!first && v.WindowTitle != lv.WindowTitle) {
		b.WriteString(ansi.SetWindowTitle(v.WindowTitle))
	}
	if first || v.KeyboardEnhancements != lv.KeyboardEnhancements {
		b.WriteString(ansi.SetModifyOtherKeys2)
		flags := keyboardFlags(v.KeyboardEnhancements)
		if first {
			b.WriteString(ansi.PushKittyKeyboard(flags))
		} else {
			b.WriteString(ansi.KittyKeyboard(flags, 1))
		}
		b.WriteString(ansi.RequestKittyKeyboard)
	}

	var cc, lcc, lfg, lbg color.Color
	var lcur *tea.Cursor
	if v.Cursor != nil {
		cc = v.Cursor.Color
	}
	if !first {
		lcur = lv.Cursor
		if lcur != nil {
			lcc = lcur.Color
		}
		lfg, lbg = lv.ForegroundColor, lv.BackgroundColor
	}
	for _, c := range []struct {
		now, was color.Color
		reset    string
		set      func(string) string
	}{
		{cc, lcc, ansi.ResetCursorColor, ansi.SetCursorColor},
		{v.ForegroundColor, lfg, ansi.ResetForegroundColor, ansi.SetForegroundColor},
		{v.BackgroundColor, lbg, ansi.ResetBackgroundColor, ansi.SetBackgroundColor},
	} {
		if c.now == c.was {
			continue
		}
		if c.now == nil {
			b.WriteString(c.reset)
			continue
		}
		r, g, bl, _ := c.now.RGBA()
		b.WriteString(c.set(fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, bl>>8)))
	}
	if cs := cursorStyle(v.Cursor); cs != cursorStyle(lcur) {
		b.WriteString(ansi.SetCursorStyle(cs))
	}
}

// keyboardFlags is Bubble Tea's kitty keyboard flags for ke.
func keyboardFlags(ke tea.KeyboardEnhancements) int {
	flags := 1
	if ke.ReportEventTypes {
		flags |= ansi.KittyReportEventTypes
	}
	if ke.ReportAlternateKeys {
		flags |= ansi.KittyReportAlternateKeys
	}
	if ke.ReportAllKeysAsEscapeCodes {
		flags |= ansi.KittyReportAllKeysAsEscapeCodes
	}
	if ke.ReportAssociatedText {
		flags |= ansi.KittyReportAssociatedKeys
	}
	return flags
}

// cursorStyle is the DECSCUSR value for cur, 0 for none.
func cursorStyle(cur *tea.Cursor) int {
	if cur == nil {
		return 0
	}
	style := int(cur.Shape)*2 + 1
	if !cur.Blink {
		style++
	}
	return style
}

// modesEqual reports whether a and b ask for the same modes and cursor.
func modesEqual(a, b *tea.View) bool {
	if a == nil || b == nil {
		return false
	}
	if (a.Cursor == nil) != (b.Cursor == nil) || (a.Cursor != nil && *a.Cursor != *b.Cursor) {
		return false
	}
	return a.AltScreen == b.AltScreen && a.MouseMode == b.MouseMode && a.ReportFocus == b.ReportFocus &&
		a.DisableBracketedPasteMode == b.DisableBracketedPasteMode &&
		a.KeyboardEnhancements == b.KeyboardEnhancements && a.WindowTitle == b.WindowTitle &&
		a.ForegroundColor == b.ForegroundColor && a.BackgroundColor == b.BackgroundColor
}

// Print writes s to the terminal after Close: what a program leaves on the
// screen when it ends.
func (s *Screen) Print(str string) {
	if s.closed {
		s.out.writeNow([]byte(str))
	}
}
