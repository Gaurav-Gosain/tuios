package termio

import (
	"bytes"
	"fmt"
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
