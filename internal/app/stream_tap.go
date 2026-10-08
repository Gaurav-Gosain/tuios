package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// StreamTap sees each pane's stream as this client applies it: the snapshot a
// pane is primed from, then every byte and in-band resize that follows it, in
// the order the client applies them. It lets a process that runs this model
// without drawing it (tuios gui-bridge) hand the same stream to a renderer of
// its own, which keeps its own emulator per pane.
//
// Snapshot runs on the Update goroutine before the pane is subscribed, so it
// is ordered before the pane's first Output. A nil state means the pane is
// subscribed without a snapshot, and the renderer must start from a blank
// screen. Output and Resized run on the daemon client's read goroutine. None
// of them may block.
type StreamTap interface {
	Snapshot(ptyID string, state *session.TerminalState)
	Output(ptyID string, data []byte)
	Resized(ptyID string, width, height int)
}

// StreamResumer is a StreamTap whose renderer may still hold a pane's
// emulator from an earlier connection, fed up to some stream position. A
// bridge that respawns behind a renderer that kept its emulators is the case:
// the renderer needs only the bytes it missed, not a new snapshot that throws
// its history away.
//
// ResumeFrom is asked when a pane restored from a snapshot ending at snapSeq
// is about to be subscribed. It returns the position the renderer holds, when
// that is before snapSeq and the stream is to start there, or 0 to start at
// snapSeq as usual. The model's own emulator still starts at snapSeq: the
// bytes between the two go to the tap only.
type StreamResumer interface {
	ResumeFrom(ptyID string, snapSeq int64) int64
}

// SetStreamTap installs t. Set it before the session is attached, so the
// panes restored on attach are seen too.
func (m *OS) SetStreamTap(t StreamTap) { m.streamTap = t }

// tapSnapshot reports a restore to the tap, but only for a pane whose stream
// is not running: the tap's emulator follows a running stream already, and a
// snapshot taken mid-stream cannot be ordered against bytes already sent.
func (m *OS) tapSnapshot(ptyID string, state *session.TerminalState) {
	if m.streamTap == nil || ptyID == "" || m.SubscribedPTYs[ptyID] {
		return
	}
	m.streamTap.Snapshot(ptyID, state)
}

// snapshotHave is how many scrollback rows a snapshot request may skip because
// this client already holds them. A tap's renderer starts each snapshot from a
// blank emulator, so with a tap the request asks for the whole history.
func (m *OS) snapshotHave(w *terminal.Window) int {
	if m.streamTap != nil {
		return 0
	}
	return w.ScrollbackLenSync()
}
