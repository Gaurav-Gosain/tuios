package session

import (
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The agent switch: [agents] enabled = false.
//
// It is for a person who wants the multiplexer and nothing else. With the
// agent features off the daemon:
//
//   - reads nothing to find an agent: the detection tick and the stall tick
//     do nothing, an OSC 9;4 or desktop notification a pane prints is
//     dropped, and no screen, title or transcript is read;
//   - refuses every agent verb (agentOnlyVerbs) with agents_disabled, the
//     herdr socket and a pane on another machine included;
//   - fires no agent hook and delivers no queued message;
//   - offers to resume no agent conversation after a restart.
//
// Turning it off clears what the daemon held: every window's agent state,
// harness and notes, every transcript join, and every Inbox item. Closing an
// item that held an approval hands the prompt back to the harness. Turning it
// on starts the readers again and makes one detection pass at once.
//
// The respond grant. A pane without respond may not type into another pane
// that waits on a prompt, since the keys would answer it (typingRefusal).
// "Waits on a prompt" is agent state, and with the features off nothing keeps
// it: a check that read it would find every pane at rest, and any pane could
// answer another pane's permission prompt. So with the features off the rule
// fails closed, with two exceptions where no agent can hold a prompt the
// keys would answer (offTypingAllowed):
//
//   - the target pane's own shell holds its terminal, at its prompt;
//   - the calling pane opened the target pane, which is what the tmux shim
//     and an agent team's split-and-send do.
//
// Every other pane is refused to a pane without respond. The person's own
// keys, and a command run from a shell outside every pane, are not held to
// it. A pane that must type into other panes is given respond through
// [agents.permissions].

// ErrVerbAgentsDisabled is the error code of a verb that is an agent feature
// while the agent features are off. The message is config.AgentsOffMessage.
const ErrVerbAgentsDisabled = "agents_disabled"

// agentOnlyVerbs are the verbs that are agent features. Each one is refused
// with agents_disabled while the features are off. wait-for is not here: it
// waits for windows and commands too, and only its agent conditions are
// refused (agentWaitConditions).
var agentOnlyVerbs = []string{
	// Agents and fleets.
	"start-agent", "fan", "compare-fan", "verify-fan", "keep-fan",
	"list-agents", "list-host-agents", "resume-agent", "ask-agent",
	"explain-agent-detect", "explain-agent-screen",
	// Agent state, as a pane reports it and as a reader reads it.
	"set-agent-state", "set-agent-session", "set-agent-meta",
	"get-agent-state", "report-agent-activity", "agent-activity",
	"agent-transcript",
	// Mail.
	"send-agent-message", "release-agent-message", "read-agent-messages",
	// The Inbox, attention and approvals.
	"list-attention", "dismiss-attention", "mark-attention",
	"peek-prompt", "respond", "request-approval", "reply-approval",
	"get-approval", "ask-human", "answer-ask",
	// The queue, and the review sent to an agent.
	"queue-prompt", "list-queued", "cancel-queued", "send-review",
}

// agentWaitConditions are the wait-for conditions that read agent state or
// agent mail.
var agentWaitConditions = []string{"agent-state", "agent-message"}

// errAgentsUnchanged ends a state mutation that would change nothing.
var errAgentsUnchanged = errors.New("no agent state to clear")

// agentsDisabledError is the refusal of an agent feature while the features
// are off.
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
	raw, err := json.Marshal(params)
	if err != nil {
		return false
	}
	var p struct {
		Condition string `json:"condition"`
	}
	return json.Unmarshal(raw, &p) == nil && slices.Contains(agentWaitConditions, p.Condition)
}

// agentsEnabled reports whether the agent features are on.
func (d *Daemon) agentsEnabled() bool { return !d.agentsOff.Load() }

// agentsOffRefusal refuses a call that is an agent feature while the features
// are off. It is nil for every other call, and for every call while they are
// on.
func (d *Daemon) agentsOffRefusal(verb string, params json.RawMessage) *verbError {
	if !d.agentsOff.Load() {
		return nil
	}
	if AgentOnlyCall(verb, params) {
		return agentsDisabledError()
	}
	return nil
}

// isAgentEvent reports whether a session event is about an agent.
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
	if on {
		LogBasic("Agent features are on")
		d.goTracked(d.rescanAgents)
		return
	}
	LogBasic("Agent features are off")
	d.clearAgentsForOff()
}

// clearAgentsForOff drops what the daemon holds for agents: every pane's
// agent state, every queued message, and every Inbox item. Closing an item
// that held an approval hands the prompt back to its harness.
func (d *Daemon) clearAgentsForOff() {
	for _, s := range d.manager.AllSessions() {
		s.clearAllAgentState()
	}
	d.dropAllQueued()
	d.attention.closeAll(AttentionClosedAgentsDisabled)
}

// dropAllQueued empties every pane's delivery queue. A message queued while
// the features were on was for an agent that may be gone when they come
// back, and typing it then would type it into whatever holds the pane.
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

// noteSessionScanned undoes a detection pass that raced the switch going
// off: a pass that read the flag before it changed can write agent state
// after the clear. Called after each session's pass; it reports whether the
// features are off, so the caller stops.
func (d *Daemon) noteSessionScanned(s *Session) bool {
	if !d.agentsOff.Load() {
		return false
	}
	d.agentsSwitchMu.Lock()
	s.clearAllAgentState()
	d.agentsSwitchMu.Unlock()
	return true
}

// notePaneCreator records that the pane calling on cs opened window. The
// typing rule reads it while the agent features are off (offTypingAllowed).
func (d *Daemon) notePaneCreator(cs *connState, window string) {
	if cs == nil || window == "" {
		return
	}
	if pa := d.paneAuthority(cs); pa != nil && pa.window != "" {
		d.paneCreators.Store(window, pa.window)
	}
}

// offTypingAllowed reports whether a pane without respond may type into
// target while the agent features are off: target's shell is at its prompt,
// or the caller opened target. See the file comment.
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

// rescanAgents makes one detection pass over every pane, with a look at each
// pane's screen, right after the features come back on. Without it a pane
// that sits on a prompt and prints nothing would read as at rest until it
// printed again, and a pane without respond could answer it.
func (d *Daemon) rescanAgents() {
	if d.agentDetectInterval <= 0 {
		return
	}
	reg := d.agentMatcher.registry
	for _, sess := range d.manager.AllSessions() {
		if d.agentsOff.Load() {
			return
		}
		sess.scanAgentDetection(d.foregroundResolver(sess), d.agentMatcher.identifyDetail, nil)
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

// clearAllAgentState drops every window's agent state, harness, notes and
// claims, and every transcript join, for the switch to off.
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
			if w.AgentState == AgentStateNone && w.AgentHarness == "" && w.AgentMessage == "" &&
				w.AgentKind == "" && len(w.AgentMeta) == 0 && w.AgentSubagents == 0 {
				continue
			}
			w.AgentState = AgentStateNone
			clearAgentNote(w)
			w.AgentHarness = ""
			w.AgentMeta = nil
			w.AgentSubagents = 0
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
