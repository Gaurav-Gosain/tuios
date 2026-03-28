package app

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

type textSizingPlacement struct {
	RawOSC    []byte
	GuestX    int
	AbsLine   int
	Scale     int
	TextLen   int
	IsPlaced  bool
	PlacedAtX int
	PlacedAtY int
}

type textSizingWinState struct {
	scrollback   int
	scrollOffset int
	x, y, w, h   int
	count        int
}

type TextSizingState struct {
	mu            sync.Mutex
	placements    map[string][]*textSizingPlacement
	pendingOutput []byte
	lastState     map[string]textSizingWinState
}

func NewTextSizingState() *TextSizingState {
	return &TextSizingState{
		placements: make(map[string][]*textSizingPlacement),
		lastState:  make(map[string]textSizingWinState),
	}
}

func (m *OS) setupTextSizingPassthrough(window *terminal.Window) {
	if window == nil || window.Terminal == nil {
		return
	}
	if m.TextSizingState == nil {
		m.TextSizingState = NewTextSizingState()
	}

	win := window

	oldScreenClear := win.Terminal.GetCallbacks().ScreenClear
	win.Terminal.SetScreenClearFunc(func() {
		if oldScreenClear != nil {
			oldScreenClear()
		}
		sixelPassthroughLog("TextSizing SCREEN_CLEAR: winID=%s", win.ID[:min(8, len(win.ID))])
		m.ClearTextSizingPlacements(win.ID)
	})

	var lastLen int
	var lastY int
	var lastTime time.Time
	window.Terminal.SetTextSizingFunc(func(rawOSC []byte, cursorX, cursorY, scale, textLen int) {
		now := time.Now()
		if len(rawOSC) == lastLen && cursorY == lastY && now.Sub(lastTime) < 100*time.Millisecond {
			return
		}
		lastLen = len(rawOSC)
		lastY = cursorY
		lastTime = now

		scrollbackLen := 0
		if win.Terminal != nil {
			scrollbackLen = win.Terminal.ScrollbackLen()
		}
		absLine := scrollbackLen + cursorY

		oscCopy := make([]byte, len(rawOSC))
		copy(oscCopy, rawOSC)

		sixelPassthroughLog("TextSizing ADD: winID=%s guestX=%d cursorY=%d scrollback=%d absLine=%d",
			win.ID[:min(8, len(win.ID))], cursorX, cursorY, scrollbackLen, absLine)

		m.TextSizingState.mu.Lock()
		m.TextSizingState.placements[win.ID] = append(
			m.TextSizingState.placements[win.ID],
			&textSizingPlacement{
				RawOSC:  oscCopy,
				GuestX:  cursorX,
				AbsLine: absLine,
				Scale:   scale,
				TextLen: textLen,
			},
		)
		m.TextSizingState.mu.Unlock()
	})
}

func eraseAt(buf *[]byte, x, y, cols, rows int) {
	spaces := make([]byte, cols)
	for i := range spaces {
		spaces[i] = ' '
	}
	for row := range rows {
		*buf = append(*buf, fmt.Sprintf("\x1b7\x1b[%d;%dH", y+row+1, x+1)...)
		*buf = append(*buf, spaces...)
		*buf = append(*buf, "\x1b8"...)
	}
}

// emitOSC66 writes CUP + OSC 66 at a screen position.
// contentEndX is the right edge of the window's content area (exclusive, 0-based).
func emitOSC66(buf *[]byte, x, y, scale, scaledWidth, contentEndX int, rawOSC []byte) {
	*buf = append(*buf, "\x1b7"...)
	*buf = append(*buf, fmt.Sprintf("\x1b[%d;%dH", y+1, x+1)...)
	*buf = append(*buf, rawOSC...)
	// Erase from end of scaled text to end of content area (NOT end of line,
	// which would destroy window borders and other UI elements).
	// Also erase the row above to clean up wrapped command text.
	eraseWidth := contentEndX - (x + scaledWidth)
	if eraseWidth > 0 {
		spaces := make([]byte, eraseWidth)
		for i := range spaces {
			spaces[i] = ' '
		}
		// Erase on scale rows
		for row := range scale {
			*buf = append(*buf, fmt.Sprintf("\x1b[%d;%dH", y+row+1, x+scaledWidth+1)...)
			*buf = append(*buf, spaces...)
		}
		// Erase on the row above (command text wrapping area)
		if y > 0 {
			*buf = append(*buf, fmt.Sprintf("\x1b[%d;%dH", y, x+scaledWidth+1)...)
			*buf = append(*buf, spaces...)
		}
	}
	*buf = append(*buf, "\x1b8"...)
}

func (m *OS) RefreshTextSizing() {
	if m.TextSizingState == nil {
		return
	}
	m.TextSizingState.mu.Lock()
	defer m.TextSizingState.mu.Unlock()

	for _, w := range m.Windows {
		if w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsAltScreen {
			continue
		}
		placements := m.TextSizingState.placements[w.ID]
		if len(placements) == 0 {
			continue
		}

		scrollbackLen := 0
		if w.Terminal != nil {
			scrollbackLen = w.Terminal.ScrollbackLen()
		}
		borderOff := w.BorderOffset()
		contentHeight := w.Height - 2*borderOff
		contentWidth := w.Width - 2*borderOff

		viewportTop := scrollbackLen - w.ScrollbackOffset
		viewportBottom := viewportTop + contentHeight

		// Detect if anything changed since last frame
		curState := textSizingWinState{
			scrollback:   scrollbackLen,
			scrollOffset: w.ScrollbackOffset,
			x: w.X, y: w.Y, w: w.Width, h: w.Height,
			count: len(placements),
		}
		if m.TextSizingState.lastState[w.ID] == curState {
			continue // Nothing changed, skip
		}
		m.TextSizingState.lastState[w.ID] = curState

		// Prune placements far out of view
		var kept []*textSizingPlacement
		for _, p := range placements {
			if p.AbsLine >= viewportTop-50 {
				kept = append(kept, p)
			}
		}
		m.TextSizingState.placements[w.ID] = kept

		screenWidth := m.GetRenderWidth()
		screenHeight := m.GetRenderHeight()

		for _, p := range kept {
			visible := p.AbsLine >= viewportTop && p.AbsLine < viewportBottom
			hostX := w.X + borderOff + p.GuestX
			hostY := w.Y + borderOff + (p.AbsLine - viewportTop)
			scaledWidth := p.TextLen * p.Scale
			eraseCols := min(scaledWidth, 120)

			// Clip: must fit entirely within window content area AND screen
			if visible {
				if hostY < w.Y+borderOff || hostY+p.Scale > w.Y+w.Height-borderOff {
					visible = false
				} else if hostX+scaledWidth > w.X+borderOff+contentWidth {
					visible = false
				} else if hostY < 0 || hostX < 0 || hostY+p.Scale > screenHeight || hostX+scaledWidth > screenWidth {
					visible = false
				}
			}

			if !visible {
				if p.IsPlaced {
					eraseAt(&m.TextSizingState.pendingOutput, p.PlacedAtX, p.PlacedAtY, eraseCols, p.Scale)
					p.IsPlaced = false
				}
				continue
			}

			// Erase old position if it moved
			if p.IsPlaced && (p.PlacedAtX != hostX || p.PlacedAtY != hostY) {
				eraseAt(&m.TextSizingState.pendingOutput, p.PlacedAtX, p.PlacedAtY, eraseCols, p.Scale)
			}

			contentEndX := w.X + borderOff + contentWidth
			emitOSC66(&m.TextSizingState.pendingOutput, hostX, hostY, p.Scale, scaledWidth, contentEndX, p.RawOSC)
			p.PlacedAtX = hostX
			p.PlacedAtY = hostY
			p.IsPlaced = true
		}
	}
}

func (m *OS) ClearTextSizingPlacements(windowID string) {
	if m.TextSizingState == nil {
		return
	}
	m.TextSizingState.mu.Lock()

	placements := m.TextSizingState.placements[windowID]
	var eraseData []byte
	for _, p := range placements {
		if p.IsPlaced {
			eraseAt(&eraseData, p.PlacedAtX, p.PlacedAtY, min(p.TextLen*p.Scale, 120), p.Scale)
		}
	}
	delete(m.TextSizingState.placements, windowID)
	delete(m.TextSizingState.lastState, windowID)
	m.TextSizingState.mu.Unlock()

	if m.PostRenderWriter != nil {
		m.PostRenderWriter.ClearPending()
	}

	if len(eraseData) > 0 {
		var buf []byte
		buf = append(buf, "\x1b[?2026h"...)
		buf = append(buf, eraseData...)
		buf = append(buf, "\x1b[?2026l"...)
		_, _ = os.Stdout.Write(buf)
	}
}

func (m *OS) FlushTextSizing() {
	if m.TextSizingState == nil {
		return
	}
	m.TextSizingState.mu.Lock()
	data := m.TextSizingState.pendingOutput
	m.TextSizingState.pendingOutput = nil
	m.TextSizingState.mu.Unlock()

	if len(data) == 0 {
		return
	}

	var buf []byte
	buf = append(buf, "\x1b[?2026h"...)
	buf = append(buf, data...)
	buf = append(buf, "\x1b[?2026l"...)

	if m.PostRenderWriter != nil {
		m.PostRenderWriter.QueuePostRender(buf)
	} else {
		_, _ = os.Stdout.Write(buf)
	}
}
