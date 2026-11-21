package session

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
)

// ErrVerbAgentsDisabled is the error code of a verb that is an agent feature
// while the agent features are off. The message is config.AgentsOffMessage.
const ErrVerbAgentsDisabled = "agents_disabled"

var agentOnlyVerbs = []string{
	"start-agent", "agent-preset", "agent-readiness", "place-agent", "delegate", "fan", "compare-fan", "verify-fan", "keep-fan", "set-fan-verdict", "judge-fan",
	"list-agents", "list-host-agents", "resume-agent", "ask-agent",
	"explain-agent-detect", "explain-agent-screen",
	"set-agent-state", "set-agent-session", "set-agent-meta", "set-agent-quiet",
	"get-agent-state", "report-agent-activity", "agent-activity", "agent-hook-event", "agent-statusline-event",
	"agent-transcript",
	"send-agent-message", "release-agent-message", "read-agent-messages", "agent-mail-notice",
	"list-attention", "dismiss-attention", "mark-attention",
	"peek-prompt", "respond", "request-approval", "reply-approval",
	"get-approval", "ask-human", "answer-ask",
	"queue-prompt", "list-queued", "cancel-queued", "send-review",
	"reply-history", "add-reply-history",
}

var agentWaitConditions = []string{"agent-state", "agent-message"}

var errAgentsUnchanged = errors.New("no agent state to clear")

func agentsDisabledError() *verbError {
	return newVerbError(ErrVerbAgentsDisabled, config.AgentsOffVerbMessage)
}

// AgentOnlyCall reports whether a call is an agent feature, the way the
// daemon decides it: an agent verb, or wait-for on an agent condition. The
// CLI uses it to refuse a call to another machine while this machine has the
// features off.
func AgentOnlyCall(verb string, params any) bool {
	if slices.Contains(agentOnlyVerbs, verb) {
		return true
	}
	if verb != "wait-for" {
		return false
	}
	raw, err := jsonx.Marshal(params)
	if err != nil {
		return false
	}
	var p struct {
		Condition string `json:"condition"`
	}
	return jsonx.Unmarshal(raw, &p) == nil && slices.Contains(agentWaitConditions, p.Condition)
}

func (d *Daemon) agentsEnabled() bool { return !d.agentsOff.Load() }

func (d *Daemon) agentsOffRefusal(verb string, params json.RawMessage) *verbError {
	if !d.agentsOff.Load() {
		return nil
	}
	if AgentOnlyCall(verb, params) {
		return agentsDisabledError()
	}
	return nil
}

func isAgentEvent(eventType string) bool {
	switch eventType {
	case EventAgentState, EventAgentMessage, EventAgentActivity, EventTranscript:
		return true
	}
	return false
}

// SetAgentsEnabled turns the agent features on or off. It is safe to call
// from any goroutine, and a call that changes nothing does nothing.
func (d *Daemon) SetAgentsEnabled(on bool) {
	d.agentsSwitchMu.Lock()
	defer d.agentsSwitchMu.Unlock()
	if d.agentsOff.Swap(!on) == !on {
		return
	}
	defer d.awake.agentEvent()
	if on {
		LogBasic("Agent features are on")
		d.wakes.detect.Set()
		d.wakes.stall.Set()
		d.goTracked(d.rescanAgents)
		return
	}
	LogBasic("Agent features are off")
	d.clearAgentsForOff()
}

func (d *Daemon) clearAgentsForOff() {
	for _, s := range d.manager.AllSessions() {
		s.clearAllAgentState()
	}
	d.dropAllQueued()
	d.attention.closeAll(AttentionClosedAgentsDisabled)
}

func (d *Daemon) dropAllQueued() {
	q := &d.queue
	var touched []string
	q.mu.Lock()
	for id := range q.typed {
		q.unstampLocked(id)
	}
	for id, pq := range q.panes {
		pq.delivering = false
		if len(q.removeLocked(id, pq, func(*queueEntry) bool { return true })) > 0 {
			touched = append(touched, id)
		}
	}
	q.mu.Unlock()
	if len(touched) > 0 {
		go d.publishQueued(touched...)
	}
}

func (d *Daemon) noteSessionScanned(s *Session) bool {
	if !d.agentsOff.Load() {
		return false
	}
	d.agentsSwitchMu.Lock()
	s.clearAllAgentState()
	d.agentsSwitchMu.Unlock()
	return true
}

func (d *Daemon) notePaneCreator(cs *connState, window string) {
	if cs == nil || window == "" {
		return
	}
	if pa := d.paneAuthority(cs); pa != nil && pa.window != "" {
		d.paneCreators.Store(window, pa.window)
	}
	d.noteStartedBy(cs, window)
}

func (d *Daemon) callerPaneRef(cs *connState) *PaneRef {
	pa := d.paneAuthority(cs)
	if pa == nil || pa.hosted || pa.window == "" || pa.window == unplacedWindow {
		return nil
	}
	sess := d.sessionHoldingWindow(pa.window)
	if sess == nil {
		return nil
	}
	return &PaneRef{Session: sess.Name(), Window: pa.window}
}

func (d *Daemon) callerWorkspaceIn(cs *connState, sess *Session) int {
	ref := d.callerPaneRef(cs)
	if ref == nil || sess == nil || ref.Session != sess.Name() {
		return 0
	}
	st := sess.GetState()
	i, err := findWindowStateIndex(st.Windows, ref.Window)
	if err != nil {
		return 0
	}
	if ws := st.Windows[i].Workspace; ws > 0 && !IsScratchWorkspace(ws) {
		return ws
	}
	return 0
}

func (d *Daemon) callerCwd(cs *connState) string {
	ref := d.callerPaneRef(cs)
	if ref == nil {
		return ""
	}
	if cs.peerPID > 0 {
		if dir, ok := ptyspawn.ProcessCwd(cs.peerPID); ok && !d.peerChanged(cs) {
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				return dir
			}
		}
	}
	if sess := d.sessionHoldingWindow(ref.Window); sess != nil {
		return sess.windowCwd(ref.Window)
	}
	return ""
}

func (d *Daemon) noteStartedBy(cs *connState, window string) {
	if cs == nil || window == "" {
		return
	}
	ref := d.callerPaneRef(cs)
	if ref == nil || ref.Window == window {
		return
	}
	if sess := d.sessionHoldingWindow(window); sess != nil {
		sess.setStartedBy(window, ref)
	}
}

func (d *Daemon) offTypingAllowed(pa *paneAuth, target WindowState) bool {
	if creator, ok := d.paneCreators.Load(target.ID); ok && creator == pa.window {
		return true
	}
	if target.PTYID == "" {
		return false
	}
	for _, s := range d.manager.AllSessions() {
		if pty := s.GetPTY(target.PTYID); pty != nil {
			return shellAtPrompt(pty)
		}
	}
	return false
}

func (d *Daemon) rescanAgents() {
	if d.agentDetectInterval <= 0 {
		return
	}
	reg := d.agentMatcher.registry
	for _, sess := range d.manager.AllSessions() {
		if d.agentsOff.Load() {
			return
		}
		sess.scanAgentDetection(d.foregroundResolver(sess), d.agentMatcher.identifyKept, nil)
		if reg != nil {
			for _, w := range sess.GetState().Windows {
				if w.PTYID != "" {
					sess.scanPaneForAgent(w.PTYID, reg)
				}
			}
		}
		if d.noteSessionScanned(sess) {
			return
		}
	}
}

func (s *Session) clearAllAgentState() {
	var windows []string
	_ = s.mutateState(func(st *SessionState) error {
		now := time.Now().UnixNano()
		changed := false
		for i := range st.Windows {
			w := &st.Windows[i]
			windows = append(windows, w.ID)
			delete(s.agentClaims, w.ID)
			delete(s.agentHarnessPIDs, w.ID)
			delete(s.agentSharedGuards, w.ID)
			if w.AgentState == AgentStateNone && w.AgentHarness == "" && w.AgentMessage == "" &&
				w.AgentKind == "" && len(w.AgentMeta) == 0 && w.AgentSubagents == 0 && len(w.AgentSubagentList) == 0 {
				continue
			}
			w.AgentState = AgentStateNone
			clearAgentNote(w)
			w.AgentHarness = ""
			w.AgentMeta = nil
			w.AgentSubagents = 0
			w.AgentSubagentList = nil
			w.AgentStateAt = now
			changed = true
		}
		if !changed {
			return errAgentsUnchanged
		}
		return nil
	})
	for _, id := range windows {
		s.DropAgentTranscript(id)
	}
}

// HostCallGuard, when set, is asked about every call a client makes to
// another machine, and a non-nil answer is returned in place of the call.
// The CLI sets it to RefuseAgentCallHere, so this machine's agent switch
// governs what this machine's client does on any machine.
var HostCallGuard func(verb string, params any) error

// AgentsOffHereError is the answer to an agent call to another machine
// while this machine has the agent features off. Path is this machine's
// config file.
type AgentsOffHereError struct {
	Path string
}

// Error names this machine's config, since the other machine's switch is
// not the one that refused.
func (e *AgentsOffHereError) Error() string {
	where := "this machine's config"
	if e.Path != "" {
		where += ", " + e.Path
	}
	return "Agent features are off in " + where + ". Set agents.enabled = true there to use this command."
}

// ErrorCode is the code the refusal carries, the one the daemon uses.
func (e *AgentsOffHereError) ErrorCode() string { return ErrVerbAgentsDisabled }
