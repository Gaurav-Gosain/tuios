package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/scrollclear"
	"tuios.dev/vt"
)

// ErrVerbFullScreen reports a clear refused because a full-screen program
// holds the pane's alternate screen. Nothing was cleared.
const ErrVerbFullScreen = "full_screen"

var errFullScreen = errors.New("a full-screen program uses the pane. Quit the program, then clear the scrollback")

// IsFullScreenError reports whether err is a clear refused because a
// full-screen program holds the pane, from this process or from a daemon.
func IsFullScreenError(err error) bool {
	if errors.Is(err, errFullScreen) {
		return true
	}
	callErr, ok := errors.AsType[*VerbCallError](err)
	return ok && callErr.Code == ErrVerbFullScreen
}

const clearHistoryWait = 5 * time.Second

// ClearHistorySequence is the output that clears t's scrollback. With screen
// set it also removes the rows above the prompt: the cursor row, or the row
// the prompt starts on when the shell marks its prompt with OSC 133, moves to
// the top of the screen with everything below it. Nothing is typed to the
// program, so the shell does not have to redraw. Ghostty keeps the cursor row
// the same way when it cannot ask the shell to redraw.
//
// The rows go up by line feeds at the bottom row, which every emulator scrolls
// into its history, and ED 3 then drops that history with the rest. The cursor
// is put back by CUP on its row less the rows removed.
//
// It returns errFullScreen while the alternate screen is up: ED 3 there
// addresses the alternate screen, and moving rows would break the program's
// own idea of the screen. Ghostty does nothing on the alternate screen either.
// The caller holds whatever lock guards t.
func ClearHistorySequence(t vt.Terminal, screen bool) ([]byte, error) {
	if t.IsAltScreen() {
		return nil, errFullScreen
	}
	var b strings.Builder
	if screen {
		if n := clearKeepRow(t); n > 0 {
			pos := t.CursorPosition()
			fmt.Fprintf(&b, "\x1b[%d;1H%s\x1b[%d;%dH", t.Height(), strings.Repeat("\n", n), pos.Y-n+1, pos.X+1)
		}
	}
	b.WriteString(scrollclear.Drop)
	return []byte(b.String()), nil
}

func clearKeepRow(t vt.Terminal) int {
	y := t.CursorPosition().Y
	if y <= 0 {
		return 0
	}
	keep := y
	if l := t.SemanticMarkers(); l != nil {
		marks := l.Markers()
		if n := len(marks); n > 0 {
			last := marks[n-1]
			if last.Type == vt.MarkerPromptStart || last.Type == vt.MarkerCommandStart {
				if a := l.Last(vt.MarkerPromptStart); a != nil {
					if row := a.AbsLine - t.ScrollbackLen(); row >= 0 && row < y {
						keep = row
					}
				}
			}
		}
	}
	return keep
}

// ClearHistory clears the pane's scrollback, and with screen set the rows
// above its prompt, for the daemon's emulator and every subscriber at once.
//
// The output queued before it is applied first, under streamMu so nothing
// new can be queued: the sequence is built from the cursor the emulator has
// after the last byte the program wrote, and goes into the stream right
// behind that byte.
func (p *PTY) ClearHistory(ctx context.Context, screen bool) error {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	if !p.vtClosed {
		ack := make(chan struct{})
		select {
		case p.vtWriteChan <- vtChunk{ack: ack}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-ack:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.terminalMu.Lock()
	if p.terminal == nil {
		p.terminalMu.Unlock()
		return errors.New("the pane has no screen")
	}
	data, err := ClearHistorySequence(p.terminal, screen)
	if err != nil {
		p.terminalMu.Unlock()
		return err
	}
	if p.vtClosed {
		_, _ = p.terminal.Write(data)
	}
	p.terminalMu.Unlock()

	p.outputMu.Lock()
	seq := p.appendToBuffer(data)
	p.outputMu.Unlock()
	p.broadcast(ptyChunk{data: data}, seq)
	if p.vtClosed {
		p.terminalMu.Lock()
		p.vtSeq = seq
		p.terminalMu.Unlock()
		return nil
	}
	select {
	case p.vtWriteChan <- vtChunk{data: data, seq: seq}:
	case <-p.ctx.Done():
	}
	return nil
}

func (d *Daemon) verbClearHistory(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Screen  bool   `json:"screen"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	ctx, cancel := context.WithTimeout(context.Background(), clearHistoryWait)
	defer cancel()
	if err := pty.ClearHistory(ctx, p.Screen); err != nil {
		if errors.Is(err, errFullScreen) {
			return nil, newVerbError(ErrVerbFullScreen, err.Error())
		}
		return nil, newVerbError(ErrVerbInternal, "clear-history: "+err.Error())
	}
	return map[string]any{"type": "history_cleared", "window": pty.ID, "screen": p.Screen}, nil
}
