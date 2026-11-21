package app

import (
	"bytes"
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
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

	var lastLen, lastX, lastY, lastScroll int
	var lastTime time.Time
	window.Terminal.SetTextSizingFunc(func(rawOSC []byte, cursorX, cursorY, scale, textLen int) {
		now := time.Now()
		scrollbackLen := 0
		if win.Terminal != nil {
			scrollbackLen = win.Terminal.ScrollbackLen()
		}
		if len(rawOSC) == lastLen && cursorX == lastX && cursorY == lastY &&
			scrollbackLen == lastScroll && now.Sub(lastTime) < 100*time.Millisecond {
			return
		}
		lastLen = len(rawOSC)
		lastX = cursorX
		lastY = cursorY
		lastScroll = scrollbackLen
		lastTime = now

		absLine := scrollbackLen + cursorY

		oscCopy := cleanOSC66(rawOSC)

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

func emitOSC66(buf *[]byte, x, y, scale, scaledWidth, contentTopY, contentEndX int, rawOSC []byte) {
	*buf = append(*buf, "\x1b7"...)
	*buf = append(*buf, fmt.Sprintf("\x1b[%d;%dH", y+1, x+1)...)
	*buf = append(*buf, rawOSC...)
	eraseWidth := contentEndX - (x + scaledWidth)
	if eraseWidth > 0 {
		spaces := make([]byte, eraseWidth)
		for i := range spaces {
			spaces[i] = ' '
		}
		for row := range scale {
			*buf = append(*buf, fmt.Sprintf("\x1b[%d;%dH", y+row+1, x+scaledWidth+1)...)
			*buf = append(*buf, spaces...)
		}
		if y > 0 && y-1 >= contentTopY {
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
		if w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsAltScreen() {
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

		curState := textSizingWinState{
			scrollback:   scrollbackLen,
			scrollOffset: w.ScrollbackOffset,
			x:            w.X + m.sessionView.dx, y: w.Y + m.sessionView.dy, w: w.Width, h: w.Height,
			count: len(placements),
		}
		if m.TextSizingState.lastState[w.ID] == curState {
			continue
		}
		m.TextSizingState.lastState[w.ID] = curState

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
			view := m.sessionView
			wx, wy := w.X+view.dx, w.Y+view.dy
			hostX := wx + borderOff + p.GuestX
			hostY := wy + borderOff + (p.AbsLine - viewportTop)
			scaledWidth := p.TextLen * p.Scale
			eraseCols := min(scaledWidth, 120)

			if visible {
				if hostY < wy+borderOff || hostY+p.Scale > wy+w.Height-borderOff {
					visible = false
				} else if hostX+scaledWidth > wx+borderOff+contentWidth {
					visible = false
				} else if hostY < 0 || hostX < 0 || hostY+p.Scale > screenHeight || hostX+scaledWidth > screenWidth {
					visible = false
				} else if view.on && !image.Rect(hostX, hostY, hostX+scaledWidth, hostY+p.Scale).In(view.clip) {
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

			if p.IsPlaced && (p.PlacedAtX != hostX || p.PlacedAtY != hostY) {
				eraseAt(&m.TextSizingState.pendingOutput, p.PlacedAtX, p.PlacedAtY, eraseCols, p.Scale)
			}

			contentTopY := wy + borderOff
			contentEndX := wx + borderOff + contentWidth
			emitOSC66(&m.TextSizingState.pendingOutput, hostX, hostY, p.Scale, scaledWidth, contentTopY, contentEndX, p.RawOSC)
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
		m.WriteHost(syncBegin, eraseData, syncEnd)
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
		return
	}
	m.WriteHost(buf)
}

func cleanOSC66(raw []byte) []byte {
	body := raw
	if bytes.HasPrefix(body, []byte("\x1b]")) {
		body = body[2:]
	}
	switch {
	case bytes.HasSuffix(body, []byte("\x1b\\")):
		body = body[:len(body)-2]
	case bytes.HasSuffix(body, []byte("\a")):
		body = body[:len(body)-1]
	}
	out := make([]byte, 0, len(body)+3)
	out = append(out, "\x1b]"...)
	out = append(out, vt.StripControls(string(body))...)
	return append(out, '\a')
}
