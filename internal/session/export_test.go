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

func (s *Session) scanScreenForAgent(ptyID string, reg *harness.Registry) bool {
	return s.lookAtPane(ptyID, reg, paneLook{screen: true})
}

func (s *Session) applyAgentDetection(
	resolve func(ptyID string) (foregroundInfo, bool),
	identify func(foregroundInfo) (detection, bool),
) int {
	return s.scanAgentDetection(resolve, identify, nil)
}

func StartPeriodicSave(getState func() *SessionState, dirty, stale *wakeLatch, wakes *atomic.Int64) func() {
	dirty.init()
	stale.init()
	return startPeriodicSaveWith(resurrectionInterval, getState, dirty, stale, wakes, SaveSessionForResurrection)
}

func setResurrectionDirOverride(dir string) string {
	prev, _ := resurrectionDirOverride.Swap(dir).(string)
	return prev
}

func openPaneOn(stream io.ReadWriteCloser, spec hostedPaneSpec) (string, *bufio.Reader, error) {
	opened, br, err := openPaneReply(stream, spec)
	return opened.Pane, br, err
}

func (p *PTY) refreshRemoteCwdOnOutput() {
	if rp, ok := p.pty.(*remotePane); ok {
		rp.Cwd()
	}
}

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

func (d *Daemon) uploadBytes() int {
	d.uploadsMu.Lock()
	defer d.uploadsMu.Unlock()
	n := 0
	for _, u := range d.uploads {
		n += len(u.data)
	}
	return n
}

func (a *attentionStore) wakeDue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.wakeDueLocked()
}

func (a *attentionStore) snoozedCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.snoozed) + len(a.hostSnoozed)
}

func (g *idleGate) pendingFor(windowID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.pending[windowID]
	return ok
}

func submitPromptTimed(ctx context.Context, pane promptPane, text string, in harness.InputProfile, quiet, maxWait time.Duration) error {
	_, err := submitPromptStamped(ctx, pane, text, in, quiet, maxWait)
	return err
}

func historyStateOf(t vt.Terminal, lines int) *TerminalState {
	r := snapshot.CaptureHistory(t, lines)
	return r.State(r.Len())
}
