//go:build !slim

package session

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// sessionFeatures is the state Session keeps for the features tuios-slim leaves
// out. Session embeds it, so the fields read as its own.
type sessionFeatures struct {
	// agentClaims records, by window ID, who owns the window's agent state: the
	// ranked source that last set it (see AgentSource) and whether the
	// foreground-process detector promoted the window and so must clear it when
	// the agent exits.
	//
	// It used to be a bool holding only the second half. That was enough while the
	// detector was the only thing competing with an explicit report; it cannot
	// express which of several sources should win, so the value carries the source
	// now. Read and written under stateMu, so it needs no lock of its own.
	agentClaims map[string]agentClaim

	// agentHarnessPIDs records, by window ID, the pid of the harness process
	// whose hook last set the window's AgentSessionID, as the hook reported it.
	// sessionGuard reads it to tell a new conversation in the same harness
	// process (/clear or /resume after an interrupted turn) from a nested run
	// in another process, and applyAgentSession reads it for the same reason.
	// It is kept apart from agentClaims because other sources replace the
	// claim, and the pid must outlive that. The detector forgets it when it sees
	// the agent leave the pane. Daemon memory only, and read and written under
	// stateMu.
	agentHarnessPIDs map[string]int

	// agentSubagents records, by window ID, the subagents the window's agent
	// is running, as its hooks reported them: subagent id to its type and
	// when it was last reported. The window's AgentSubagents and the reserved
	// metadata key subagents count them, and all three move in one mutation.
	// See agent_subagents.go. Daemon memory only, and read and written under
	// stateMu.
	agentSubagents map[string]map[string]subagent

	// transcripts binds windows to the record files their harnesses write. It is
	// held here rather than in SessionState because none of it is state: a
	// transcript path names a project directory and a session, so it is kept in
	// daemon memory and never serialised, never versioned, and never pushed to a
	// client. Only the AgentState derived from it is.
	transcripts agentTranscriptState

	// agentHolds records, by window ID, a quieter agent state waiting out the
	// anti-flicker window before it is published (see holdQuieterState). It has a
	// lock of its own rather than riding stateMu because it is read and written
	// around ApplyAgentReport, which takes stateMu itself.
	agentHolds map[string]agentHold
	// agentHoldTimer is the one-shot that publishes a hold whose source then
	// went silent. Nil when nothing is waiting. Guarded by agentHoldMu.
	agentHoldTimer *time.Timer
	agentHoldMu    sync.Mutex

	// idle holds idle readings from the title and screen tiers until they are
	// confirmed (see agent_idle.go). It has its own lock.
	idle idleGate

	// agentTurns records, by window ID, when the window's current working
	// phase began, so a return to rest can be counted as a finished turn (see
	// agent_turns.go). completionSeen is the CompletionSeq each window had when
	// an attached client last pushed state with it focused. Both are guarded by
	// stateMu and neither is serialised.
	agentTurns     map[string]agentTurn
	completionSeen map[string]uint64

	// agentMetaPrune drops expired agent metadata. Idle when no token has a
	// TTL. See agent_meta.go.
	agentMetaPrune pruneTimer

	// subagentPrune drops subagents gone quiet. Idle while no pane has
	// subagents. See agent_subagents.go.
	subagentPrune pruneTimer
	// fed is the link manager a window on another machine is opened over. It is
	// nil unless the daemon installed one, and every reader checks. See
	// remote_pane.go.
	fed paneFederation
	// onRemotePane is told of every window this session opens on another
	// machine, so the daemon can hold the pane's report channel. Guarded by
	// ptysMu, like fed. See hosted_calls.go.
	onRemotePane func(windowID string, p *remotePane)
}

// ptyFeatures is the state PTY keeps for the features tuios-slim leaves
// out. PTY embeds it, so the fields read as its own.
type ptyFeatures struct {
	// lastAgentProbe is the unix-nano time of the last output-driven agent-exit
	// probe. It throttles the /proc read the probe does so a busy pane does not
	// re-check its foreground on every output chunk.
	lastAgentProbe atomic.Int64

	// lastScreenScan is the unix-nano time of the last output-driven screen scan,
	// throttling it the same way.
	lastScreenScan atomic.Int64

	// lastDetectScan is the unix-nano time the detection poll last read this
	// pane's foreground process, and detectSkips how many ticks have passed
	// over it since. Only the poll's goroutine touches them. See detectScanDue.
	lastDetectScan atomic.Int64
	detectSkips    atomic.Int32

	// screenSettle is the one-shot that scans the screen after a pane goes quiet.
	// It is a timer rather than a ticker so a silent pane costs nothing, which is
	// the rule the whole daemon is built to.
	//
	// The timer is built once and re-armed with Reset. Arming happens on every
	// chunk a pane emits, and a fresh time.AfterFunc there allocated a runtime
	// timer per chunk. screenLook is what it runs, held separately so the caller
	// does not have to build a closure per chunk either.
	screenSettleMu sync.Mutex
	screenSettle   *time.Timer
	screenLook     func()

	// agentProgress parks the most recent OSC 9;4 progress state the emulator
	// saw, as the state plus one so zero means none pending. The VT callback runs
	// on the vtWriter goroutine with the terminal lock held, where mutating
	// session state would re-enter that lock, so it only stores here and the PTY
	// read goroutine applies it on the output event that carried the sequence.
	agentProgress atomic.Int64
	// lastProgress is the most recent OSC 9;4 report, kept after it is
	// applied so a manifest's osc_progress rules can read it on any look. It
	// packs the state plus one into the high half and the percentage into the
	// low half, so zero means the pane has sent none.
	lastProgress atomic.Int64
	// agentNotify parks the most recent desktop notification (OSC 9, 777 or
	// 99) for the read goroutine, for the reason agentProgress does. See
	// agent_notify.go.
	agentNotify atomic.Pointer[paneNotification]
	// runClaim is held by one run call from its prompt check until the call
	// ends, so two runs cannot both pass the check and type into one line.
	// See verb_run.go.
	runClaim atomic.Bool
}

// probeAgentExitDue reports whether enough time has passed since the last
// output-driven agent-exit probe to run another, and claims the slot if so. It is
// only ever called from the single PTY read goroutine, so a plain load/store is
// race-free.
func (p *PTY) probeAgentExitDue(now int64) bool {
	if now-p.lastAgentProbe.Load() < int64(agentExitProbeInterval) {
		return false
	}
	p.lastAgentProbe.Store(now)
	return true
}

// storeAgentProgress parks an OSC 9;4 progress state for the read goroutine to
// apply. Called from the VT callback under the terminal lock, so it must stay a
// single atomic store and nothing more.
func (p *PTY) storeAgentProgress(state vt.ProgressState, percent int) {
	p.agentProgress.Store(int64(state) + 1)
	p.lastProgress.Store((int64(state)+1)<<32 | int64(uint32(int32(percent))))
}

// progressParked does nothing in the full build: the PTY read goroutine
// applies the parked report on the output event that carried it (see
// onPaneOutput). tuios-slim applies it from here.
func (s *Session) progressParked(*PTY, string) {}

// takeAgentProgress returns the parked OSC 9;4 progress state and clears it,
// reporting whether one was pending. A burst that parked several states between
// two output events collapses to the newest, which is the only one still true.
func (p *PTY) takeAgentProgress() (vt.ProgressState, bool) {
	v := p.agentProgress.Swap(0)
	if v == 0 {
		return 0, false
	}
	return vt.ProgressState(v - 1), true
}

// herdrEnvHook is the function a session asks for a new pane's herdr
// environment.
func (m *Manager) herdrEnvHook() func(sessionID, windowID string, workspace int, command []string) []string {
	return m.HerdrEnv
}

// forgetAgentClaimLocked drops who owns a closed window's agent state. The
// caller holds stateMu.
func (s *Session) forgetAgentClaimLocked(windowID string) {
	delete(s.agentClaims, windowID)
	delete(s.agentHarnessPIDs, windowID)
}
