//go:build slim

package session

import (
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// sessionFeatures holds only the timers the core session stops when it
// stops, which never run in tuios-slim. See session_features_full.go.
type sessionFeatures struct {
	idle           noIdleGate
	agentMetaPrune pruneTimer
	subagentPrune  pruneTimer
	onRemotePane   func(windowID string, p *remotePane)
}

// noIdleGate stands in for the idle readings tuios-slim never takes.
type noIdleGate struct{}

func (noIdleGate) stop() {}

// ptyFeatures holds the one agent report tuios-slim reads: the OSC 9;4
// progress state, for the prompt rule of the respond grant. See
// prompt_state_slim.go.
type ptyFeatures struct {
	// agentProgress parks the newest OSC 9;4 state as the state plus one, so
	// zero means none pending.
	agentProgress atomic.Int64
	// promptMu orders the goroutines that apply parked reports, so an older
	// report cannot land after a newer one.
	promptMu sync.Mutex
}

// storeAgentProgress parks an OSC 9;4 progress state. Called from the VT
// callback under the terminal lock, so it is a single atomic store.
func (p *PTY) storeAgentProgress(state vt.ProgressState, _ int) {
	p.agentProgress.Store(int64(state) + 1)
}

// storeAgentNotify drops the copy of a desktop notification the full build
// keeps for the agent rules. The notification itself is still published.
func (p *PTY) storeAgentNotify(string, string) {}

// The agent bookkeeping hooks of a session. tuios-slim keeps no agent state.

func (s *Session) noteAgentTurnsLocked(lifecycleSnapshot, int64) {}
func clearNowAtRestLocked(lifecycleSnapshot, *SessionState)      {}
func (s *Session) forgetSubagentsLocked(*SessionState)           {}
func (s *Session) markCompletionSeenLocked(string)               {}
func (s *Session) stopAgentHoldTimer()                           {}
func (p *PTY) stopScreenSettle()                                 {}
func (s *Session) refreshWorktree(string)                        {}
func (s *Session) worktreeListing() *WorktreeInfo                { return nil }
func (s *Session) forgetAgentClaimLocked(string)                 {}

// refuseHostCommand refuses nothing in tuios-slim: a slim client is never
// attached through another machine's daemon.
func (c *TUIClient) refuseHostCommand(*RemoteCommandPayload) string { return "" }

// detectWorktree: tuios-slim keeps no worktree record for a session.
func detectWorktree(string) *WorktreeInfo { return nil }

// herdrEnvHook: tuios-slim has no herdr socket, so a pane gets no herdr
// environment.
func (m *Manager) herdrEnvHook() func(string, string, int, []string) []string { return nil }

// startPendingStream: tuios-slim has no subscribe verb, so no connection
// ever holds a stream to start.
func (d *Daemon) startPendingStream(*connState) {}

// fireAgentStateHook: tuios-slim follows no agent state, so no agent-state
// hook fires.
func (d *Daemon) fireAgentStateHook(*Session, SessionEvent) {}
