package main

import (
	"encoding/base64"
	"io"
	"strconv"
	"strings"
)

// screen draws the frames itself. Bubble Tea's renderer runs a ticker 60
// times a second for the life of the program, also when nothing changes.
// The plugin draws after each message instead, and writes only the lines
// that changed, so a pane where nothing happens does not wake up.
type screen struct {
	out   io.Writer
	prev  []string
	w, h  int
	title string
}

func (s *screen) start() {
	// The alternate screen, no cursor, no wrap at the right edge, focus
	// events, and the mouse: presses, drags and motion, in SGR reports.
	// Motion comes only while the pointer moves over the pane.
	_, _ = io.WriteString(s.out, "\x1b[?1049h\x1b[?25l\x1b[?7l\x1b[?1004h\x1b[?2031h\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h\x1b[2J")
}

func (s *screen) stop() {
	_, _ = io.WriteString(s.out, "\x1b[?1006l\x1b[?1003l\x1b[?1002l\x1b[?1000l\x1b[?2031l\x1b[?1004l\x1b[?7h\x1b[?25h\x1b[?1049l")
}

// askColors asks the terminal for its colours again.
func (s *screen) askColors() { _, _ = s.out.Write(colorQueries()) }

// copy puts text on the clipboard of the terminal with OSC 52. tuios passes
// it on to the terminal it runs in.
func (s *screen) copy(text string) {
	_, _ = io.WriteString(s.out, "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte(text))+"\x07")
}

func (s *screen) draw(content string, w, h int, title string) {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	b.WriteString("\x1b[?2026h")
	if w != s.w || h != s.h {
		b.WriteString("\x1b[0m\x1b[2J")
		s.prev, s.w, s.h = nil, w, h
	}
	for i := 0; i < h && i < len(lines); i++ {
		if i < len(s.prev) && s.prev[i] == lines[i] {
			continue
		}
		b.WriteString("\x1b[")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(";1H\x1b[0m")
		b.WriteString(lines[i])
		b.WriteString("\x1b[0m")
	}
	if title != s.title {
		s.title = title
		b.WriteString("\x1b]2;")
		b.WriteString(title)
		b.WriteString("\x07")
	}
	b.WriteString("\x1b[?2026l")
	s.prev = lines
	if b.Len() > len("\x1b[?2026h\x1b[?2026l") {
		_, _ = io.WriteString(s.out, b.String())
	}
}
