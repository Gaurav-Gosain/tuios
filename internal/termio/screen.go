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
	"os"
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
	// staged is the frame composed last, not yet diffed, and shown a copy
	// of the frame last diffed: what the terminal shows. view and framed
	// are what the next Commit writes the modes, the cursor and the debt by.
	staged *uv.Buffer
	shown  uv.Buffer
	view   *tea.View
	framed bool

	last   *tea.View
	closed bool

	console  *os.File
	restore  func()
	released bool

	// What a fresh terminal renderer is made from, for Restore.
	environ   []string
	profile   colorprofile.Profile
	output    Output
	graphemes bool
}

// Output says how the bytes reach the terminal, which decides how the diff
// may move the cursor. Bubble Tea derives both from its input.
type Output struct {
	// Raw: the program's input is a terminal it puts in raw mode, so the
	// output is not post-processed. When it is not a terminal (an SSH
	// channel with no real PTY, the browser build), a newline reaches the
	// terminal as CR LF, and backspace is not trusted to move the cursor.
	Raw bool
	// Console is that terminal: the Screen puts it in raw mode from New,
	// and back while it is released and when it is closed, as Bubble Tea
	// does for its input when it runs a renderer.
	Console *os.File
}

// New makes the Screen for a terminal reached through out. environ is the
// program's environment: TERM decides which sequences the diff may use, as it
// does for Bubble Tea's renderer.
func New(out io.Writer, environ []string, profile colorprofile.Profile, o Output) *Screen {
	s := &Screen{out: newWriter(out), console: o.Console,
		environ: environ, profile: profile, output: o}
	s.restore = makeRaw(o.Console)
	s.scr = s.freshRenderer()
	s.query = shouldQuerySync(environ)
	return s
}

// freshRenderer is a terminal renderer that knows nothing of the screen yet.
func (s *Screen) freshRenderer() *uv.TerminalRenderer {
	r := uv.NewTerminalRenderer(&s.body, s.environ)
	r.SetColorProfile(s.profile)
	// Hard tabs stay off: tmux drops the background under one. Backspace
	// moves the cursor on a raw Linux terminal, whose termios leaves BS0
	// set, as Bubble Tea finds it there.
	r.SetTabStops(-1)
	r.SetBackspace(s.output.Raw && runtime.GOOS == "linux")
	r.SetMapNewline(!s.output.Raw && runtime.GOOS != "windows")
	r.SetScrollOptim(true)
	if s.graphemes {
		r.SetWidthMethod(ansi.GraphemeWidth)
	}
	return r
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
func (s *Screen) SetColorProfile(p colorprofile.Profile) {
	s.profile = p
	s.scr.SetColorProfile(p)
}

// ModeReport takes the terminal's answers to the start query.
func (s *Screen) ModeReport(msg tea.ModeReportMsg) {
	switch msg.Mode {
	case ansi.ModeSynchronizedOutput:
		if msg.Value == ansi.ModeReset {
			s.syncd = true
		}
	case ansi.ModeUnicodeCore:
		if msg.Value == ansi.ModeReset || msg.Value == ansi.ModeSet || msg.Value == ansi.ModePermanentlySet {
			s.graphemes = true
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

// Frame takes a newly composed frame and reports whether it differs from the
// frame last written. Nothing is diffed or written yet: Commit does that, so
// frames composed before the write merge into one, as Bubble Tea's renderer
// wrote only the view it held when its flush came. buf must stay as composed
// until Commit, or be composed again.
func (s *Screen) Frame(buf *uv.Buffer) bool {
	if s.closed || s.released {
		return false
	}
	s.staged = buf
	return !sameCells(buf, &s.shown)
}

// FrameString is Frame for a frame drawn as a string, w by h cells.
func (s *Screen) FrameString(content string, w, h int) bool {
	w, h = max(w, 1), max(h, 1)
	if s.scratch.RenderBuffer == nil || s.scratch.Width() != w || s.scratch.Height() != h {
		s.scratch = uv.NewScreenBuffer(w, h)
	}
	s.scratch.Clear()
	uv.NewStyledString(content).Draw(s.scratch, s.scratch.Bounds())
	return s.Frame(s.scratch.Buffer)
}

// Flush takes the modes v asks for and the cursor, for the next Commit.
// framed says a frame was composed for it: that write goes out even when
// empty, because the writer behind it may owe graphics to a frame write.
func (s *Screen) Flush(v tea.View, framed bool) {
	if s.closed || s.released {
		return
	}
	keep := v
	keep.Content = ""
	s.view = &keep
	s.framed = s.framed || framed
}

// Commit writes what changed since the last Commit: the modes, the cells of
// the frame staged last, and the cursor. Called once a flush is due (Bubble
// Tea's flush message), so everything composed before it goes out in one
// write.
func (s *Screen) Commit() {
	if s.closed || s.released || s.view == nil {
		return
	}
	v := s.view
	if !s.framed && s.staged == nil && s.pre.Len() == 0 && modesEqual(s.last, v) {
		s.view = nil
		return
	}
	s.start()
	if buf := s.staged; buf != nil {
		s.diff(buf)
		s.staged = nil
	}
	lv := s.last
	first := lv == nil
	var modes, post bytes.Buffer
	writeModes(&modes, lv, v)

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
	if len(out) > 0 || s.framed {
		s.out.enqueue(out)
	}
	s.pre.Reset()
	s.body.Reset()
	s.framed = false
	s.last = v
	s.view = nil
}

// diff renders buf against what the terminal shows into body, and keeps a
// copy of it as the frame shown.
func (s *Screen) diff(buf *uv.Buffer) {
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
	s.scr.Render(&s.rb)
	_ = s.scr.Flush()
	copyCells(&s.shown, buf)
}

// sameCells reports whether a and b hold the same cells.
func sameCells(a, b *uv.Buffer) bool {
	if a.Width() != b.Width() || a.Height() != b.Height() {
		return false
	}
	for y := range a.Lines {
		la, lb := a.Lines[y], b.Lines[y]
		for x := range la {
			if !la[x].Equal(&lb[x]) {
				return false
			}
		}
	}
	return true
}

// copyCells makes dst a copy of src, reusing its lines where it can.
func copyCells(dst, src *uv.Buffer) {
	if dst.Width() != src.Width() || dst.Height() != src.Height() {
		*dst = *uv.NewBuffer(src.Width(), src.Height())
	}
	for y := range src.Lines {
		copy(dst.Lines[y], src.Lines[y])
	}
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
	if !s.released {
		s.out.writeNow(s.leave())
		s.restore()
	}
}

// Release hands the terminal to another program (an editor, or the shell
// on a suspend): the queued frames go out, the modes are put back, the
// alternate screen is left and the console leaves raw mode.
func (s *Screen) Release() {
	if s.closed || s.released || !s.started {
		return
	}
	s.released = true
	s.out.flush()
	s.out.writeNow(s.leave())
	s.restore()
}

// Restore takes the terminal back after Release. The next frame enters the
// alternate screen again, writes every mode and redraws every cell.
func (s *Screen) Restore() {
	if s.closed || !s.released {
		return
	}
	s.released = false
	s.restore = makeRaw(s.console)
	s.started = false
	s.last = nil
	s.w, s.h = 0, 0
	s.shown = uv.Buffer{}
	s.staged, s.view = nil, nil
	s.scr = s.freshRenderer()
}

// leave is the sequences that undo what the frames set, for the modes of the
// last frame. It moves the terminal renderer's cursor to the last row.
func (s *Screen) leave() []byte {
	lv := s.last
	if lv == nil {
		return nil
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
	return b.Bytes()
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
