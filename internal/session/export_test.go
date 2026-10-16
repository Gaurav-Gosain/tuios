package session

import (
	"bufio"
	"context"
	"io"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"tuios.dev/vt"
	"tuios.dev/vt/snapshot"
)

// Seams the tests reach for. Production code calls the functions these wrap.

// scanScreenForAgent runs only the screen tier of a look; see screenVerdict.
func (s *Session) scanScreenForAgent(ptyID string, reg *harness.Registry) bool {
	return s.lookAtPane(ptyID, reg, paneLook{screen: true})
}

// applyAgentDetection is scanAgentDetection reading every pane.
func (s *Session) applyAgentDetection(
	resolve func(ptyID string) (foregroundInfo, bool),
	identify func(foregroundInfo) (detection, bool),
) int {
	return s.scanAgentDetection(resolve, identify, nil)
}

// StartPeriodicSave is startPeriodicSaveWith writing through
// SaveSessionForResurrection. It makes the channels of the latches it is given.
func StartPeriodicSave(getState func() *SessionState, dirty, stale *wakeLatch, wakes *atomic.Int64) func() {
	dirty.init()
	stale.init()
	return startPeriodicSaveWith(getState, dirty, stale, wakes, SaveSessionForResurrection)
}

// setResurrectionDirOverride redirects resurrection state, and returns the value
// it replaced.
func setResurrectionDirOverride(dir string) string {
	prev, _ := resurrectionDirOverride.Swap(dir).(string)
	return prev
}

// openPaneOn is openPaneReply with only the pane id.
func openPaneOn(stream io.ReadWriteCloser, spec hostedPaneSpec) (string, *bufio.Reader, error) {
	opened, br, err := openPaneReply(stream, spec)
	return opened.Pane, br, err
}

// refreshRemoteCwdOnOutput asks a pane on another machine where it is, if it
// has been long enough since the last answer. Reading the cached value is what
// starts the ask; see remotePane.Cwd.
func (p *PTY) refreshRemoteCwdOnOutput() {
	if rp, ok := p.pty.(*remotePane); ok {
		rp.Cwd()
	}
}

// closeDaemonLogFile releases the file. The daemon holds it for the life of the
// process.
func closeDaemonLogFile() {
	daemonLogMu.Lock()
	defer daemonLogMu.Unlock()
	if daemonLogFH != nil {
		_ = daemonLogFH.Close()
		daemonLogFH = nil
	}
	daemonLogPath = ""
	daemonLogSize = 0
}

// uploadBytes reports how many bytes unfinished uploads hold.
func (d *Daemon) uploadBytes() int {
	d.uploadsMu.Lock()
	defer d.uploadsMu.Unlock()
	n := 0
	for _, u := range d.uploads {
		n += len(u.data)
	}
	return n
}

// wakeDue wakes every timed snooze whose time has come and sets the timer for
// the next.
func (a *attentionStore) wakeDue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.wakeDueLocked()
}

// snoozedCount is how many items are asleep.
func (a *attentionStore) snoozedCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.snoozed) + len(a.hostSnoozed)
}

// pendingFor reports whether a window has an idle reading waiting.
func (g *idleGate) pendingFor(windowID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.pending[windowID]
	return ok
}

// submitPromptTimed is submitPrompt with the waits as parameters.
func submitPromptTimed(ctx context.Context, pane promptPane, text string, in harness.InputProfile, quiet, maxWait time.Duration) error {
	_, err := submitPromptStamped(ctx, pane, text, in, quiet, maxWait)
	return err
}

// historyStateOf is snapshot.CaptureHistory and State together, for a caller
// that holds the emulator itself.
func historyStateOf(t vt.Terminal, lines int) *TerminalState {
	r := snapshot.CaptureHistory(t, lines)
	return r.State(r.Len())
}
