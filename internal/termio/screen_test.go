package termio

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

// lockedBuffer is the terminal end of a Screen: the writer goroutine writes
// into it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) take() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := append([]byte(nil), l.b.Bytes()...)
	l.b.Reset()
	return out
}

// TestScreenDrawsStringFramesAsTheirCells is a wire compatibility check: a
// frame that reaches the Screen as a string (the screen saver, the fullscreen
// fast path, the crash screen) must leave the terminal showing the cells the
// string draws. The first string frame once crashed the client: the scratch
// buffer it is drawn into did not exist yet.
func TestScreenDrawsStringFramesAsTheirCells(t *testing.T) {
	const w, h = 60, 12
	for _, out := range []Output{{Raw: true}, {Raw: false}} {
		t.Run(fmt.Sprintf("raw=%v", out.Raw), func(t *testing.T) {
			term := &lockedBuffer{}
			s := New(term, []string{"TERM=xterm-256color"}, colorprofile.TrueColor, out)
			screen := vt.NewEmulator(w, h)
			for i := range 5 {
				frame := fmt.Sprintf("\x1b[1;38;2;200;80;10mframe %d\x1b[m\nsecond \x1b[44mline\x1b[m %d\n\n漢字 wide %d", i, i*7, i)
				s.FrameString(frame, w, h)
				s.Flush(tea.View{AltScreen: true}, true)
				s.Commit()
				s.out.flush()
				written := term.take()
				if !out.Raw {
					// The channel a non-raw screen writes to turns each
					// newline into CR LF on its way to the terminal.
					written = bytes.ReplaceAll(written, []byte("\n"), []byte("\r\n"))
				}
				_, _ = screen.Write(written)

				want := uv.NewScreenBuffer(w, h)
				uv.NewStyledString(frame).Draw(want, want.Bounds())
				for y := range h {
					for x := range w {
						got, exp := screen.CellAt(x, y), want.CellAt(x, y)
						if exp == nil || exp.Content == "" {
							continue
						}
						if got == nil || got.Content != exp.Content || !got.Style.Equal(&exp.Style) {
							t.Fatalf("frame %d cell %d,%d: terminal has %+v, the string draws %+v", i, x, y, got, exp)
						}
					}
				}
			}
			s.Close()
		})
	}
}

// TestRowDiffWritesWhatTheFullDiffWrites is a wire compatibility check for
// the diff of changed rows only. The ways it could go wrong, each driven
// below: a changed row compared as the same and left stale; the scroll check
// run with some rows untouched, so it reads hashes left from an older frame
// and scrolls the wrong lines; a one-row frame on which the full diff would
// have scrolled; the frame shown not updated for a row that changed; a resize
// diffed by rows. Every frame is diffed both ways: the bytes must be equal,
// and both terminals must show the same cells. A frame with a cell whose
// width may drift is the exception for bytes: the full diff repaints such a
// row on every frame, changed or not, and the row diff only when it changed,
// so only the cells are compared there.
func TestRowDiffWritesWhatTheFullDiffWrites(t *testing.T) {
	env := []string{"TERM=xterm-256color"}
	words := []string{"alpha", "beta", "gamma", "delta", "$ ls -la", "", "", "total 48"}
	for seed := range int64(6) {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			w, h := 40, 12
			rows, full := New(io.Discard, env, colorprofile.TrueColor, Output{Raw: true}), New(io.Discard, env, colorprofile.TrueColor, Output{Raw: true})
			rows.start()
			full.start()
			rowTerm, fullTerm := vt.NewEmulator(w, h), vt.NewEmulator(w, h)
			lines := make([]string, h)
			for i := range lines {
				lines[i] = words[rng.Intn(len(words))]
			}
			wide := seed%3 == 2
			for frame := range 300 {
				switch op := rng.Intn(10); {
				case op < 4: // a typed key: one cell of one row
					y := rng.Intn(h)
					lines[y] += string(rune('a' + rng.Intn(26)))
					if len(lines[y]) > w {
						lines[y] = lines[y][:2]
					}
				case op < 6: // a few rows
					for range 1 + rng.Intn(3) {
						lines[rng.Intn(h)] = words[rng.Intn(len(words))]
					}
				case op < 8: // output scrolls the screen up
					k := 1 + rng.Intn(3)
					lines = append(lines[k:], make([]string, k)...)
					for i := h - k; i < h; i++ {
						lines[i] = words[rng.Intn(len(words))]
					}
				case op < 9: // nothing changed
				default: // a resize
					if frame%2 == 0 {
						w, h = 40, 12
					} else {
						w, h = 44, 10
					}
					rowTerm.Resize(w, h)
					fullTerm.Resize(w, h)
					for len(lines) < h {
						lines = append(lines, "")
					}
					lines = lines[:h]
				}
				sb := uv.NewScreenBuffer(w, h)
				for y, l := range lines {
					style := ""
					if y%3 == 0 {
						style = "\x1b[38;5;" + fmt.Sprint(16+y) + "m"
					}
					if wide && y == 4 {
						l = "漢字 " + l
					}
					uv.NewStyledString(style+l).Draw(sb, uv.Rect(0, y, w, 1))
				}
				buf := sb.Buffer
				rows.diff(buf)
				fullDiff(full, buf)
				gotRows, gotFull := rows.body.Bytes(), full.body.Bytes()
				if !wide && !bytes.Equal(gotRows, gotFull) {
					t.Fatalf("frame %d: the row diff wrote %q, the full diff %q", frame, gotRows, gotFull)
				}
				_, _ = rowTerm.Write(gotRows)
				_, _ = fullTerm.Write(gotFull)
				rows.body.Reset()
				full.body.Reset()
				for y := range h {
					for x := range w {
						a, b := rowTerm.CellAt(x, y), fullTerm.CellAt(x, y)
						if (a == nil) != (b == nil) || (a != nil && (a.Content != b.Content || !a.Style.Equal(&b.Style))) {
							t.Fatalf("frame %d cell %d,%d: the row diff shows %+v, the full diff %+v", frame, x, y, a, b)
						}
					}
				}
			}
		})
	}
}

// fullDiff is the diff before rows were compared: every row touched on every
// frame, with the scroll check.
func fullDiff(s *Screen, buf *uv.Buffer) {
	w, h := buf.Width(), buf.Height()
	if w != s.w || h != s.h {
		if s.w != 0 {
			s.scr.Resize(w, h)
			s.scr.Erase()
		}
		s.w, s.h = w, h
	}
	s.rb.Buffer = buf
	s.touched = make([]uv.LineData, h)
	s.rb.Touched = make([]*uv.LineData, h)
	for i := range s.rb.Touched {
		s.touched[i] = uv.LineData{FirstCell: 0, LastCell: w}
		s.rb.Touched[i] = &s.touched[i]
	}
	s.scr.Render(&s.rb)
	_ = s.scr.Flush()
	copyCells(&s.shown, buf)
}
