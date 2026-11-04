package session

import (
	"encoding/binary"
	"encoding/json"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

// pane-history: the history rows above those a client holds.
//
// A stream-pane snapshot carries paneStreamScrollback rows of history, so a
// viewer attaches fast. A viewer that scrolls to the top of what it holds asks
// pane-history for the rows before it, by row id. A history row's id is its
// index plus the number of rows the history has let go of from its oldest end
// (vt.Emulator.HistoryDropped), so it stays the same while output adds rows
// at the bottom and the ring drops them at the top. The H frame, sent before
// each S frame, gives the id of the first history row the snapshot carries.
//
// A reflow rewraps the rows and a clear removes them, so ids taken before a
// resize or a clear name other rows after it. The libghostty-vt emulator does
// not count the rows its ring drops, so its panes have no ids: no H frame,
// and pane-history refuses them.

const (
	// paneFrameHistory is the H frame: u64 id of the snapshot's first
	// history row.
	paneFrameHistory = 'H'
	// paneHistoryDefault and paneHistoryMax bound one pane-history reply.
	paneHistoryDefault = 500
	paneHistoryMax     = 2000
)

// ErrVerbNoRowIDs refuses pane-history on a pane whose emulator gives its
// history rows no ids.
const ErrVerbNoRowIDs = "no_row_ids"

// historyCounter is an emulator that counts the history rows it let go of.
type historyCounter interface{ HistoryDropped() int64 }

// GetTerminalStateIDs is GetTerminalState for a stream: it also returns the
// id of the first history row the state carries, and false when the pane's
// emulator gives no ids.
func (p *PTY) GetTerminalStateIDs(maxScrollback int) (*TerminalState, int64, bool) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil {
		return nil, 0, false
	}
	state := TerminalStateOf(p.terminal, p.terminal.Width(), p.terminal.Height(), maxScrollback, 0)
	state.Seq = p.vtSeq
	hc, ok := p.terminal.(historyCounter)
	if !ok {
		return state, 0, false
	}
	return state, hc.HistoryDropped() + int64(state.ScrollbackLen-len(state.Scrollback)), true
}

// writeHistoryID sends the H frame of the snapshot the stream holds.
func (ps *paneStream) writeHistoryID() error {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(ps.snapFirst))
	return ps.writeFrame(paneFrameHistory, b[:])
}

// verbPaneHistory answers pane-history.
func (d *Daemon) verbPaneHistory(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Before  *int64 `json:"before"`
		Count   int    `json:"count"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Before == nil {
		return nil, invalidParam("before", "pane-history needs before: the id of the oldest history row the caller holds, from the H frame or the last reply's first.")
	}
	if p.Count < 0 || p.Count > paneHistoryMax {
		return nil, invalidParam("count", "count is how many rows to return, 1 to 2000.")
	}
	if p.Count == 0 {
		p.Count = paneHistoryDefault
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	_, pty, verr := d.resolveWindowPTY(sess, p.Window)
	if verr != nil {
		return nil, verr
	}
	rows, first, oldest, ok := pty.historyRows(*p.Before, p.Count)
	if !ok {
		return nil, newVerbError(ErrVerbNoRowIDs, "this pane's emulator gives its history rows no ids. Read the history with capture-pane instead")
	}
	return map[string]any{
		"rows":   rows,
		"first":  first,
		"count":  int64(*p.Before) - first,
		"oldest": oldest,
	}, nil
}

// historyRows returns the history rows with ids from before-count up to
// before, as VT sequences that print them row after row: each row's text and
// styles, then "\r\n", or nothing after a row the next one continues (a soft
// wrap), which is printed to the last column. first is the id of the first
// row returned and oldest the id of the oldest row the pane holds. ok is
// false when the emulator gives no ids.
func (p *PTY) historyRows(before int64, count int) (rows string, first, oldest int64, ok bool) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	t := p.terminal
	if t == nil {
		return "", 0, 0, false
	}
	hc, ok := t.(historyCounter)
	if !ok {
		return "", 0, 0, false
	}
	base, n := hc.HistoryDropped(), t.ScrollbackLen()
	end := int(min(max(before-base, 0), int64(n)))
	start := max(end-count, 0)
	var b strings.Builder
	t.ScrollbackRows(start, end, func(i int, line uv.Line) bool {
		// A line is decoded padded to its width, and Render prints its
		// blanks: a wrapped row then runs to its last column, and the row
		// after continues it. Another row ends with "\r\n", without the
		// blanks after its text, which Render prints last and unstyled.
		s := line.Render()
		if wrapped, _ := t.ScrollbackSoftWrapped(i); wrapped && i+1 < n {
			b.WriteString(s)
			return true
		}
		b.WriteString(strings.TrimRight(s, " "))
		b.WriteString("\r\n")
		return true
	})
	return b.String(), base + int64(start), base, true
}
