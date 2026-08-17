package session

// AgentSource names where a window's agent state came from, and is the key its
// precedence is decided on. More than one source can want to set the same pane:
// the harness reporting for itself, an escape sequence it emitted, a rule
// matched against its screen, and the silence timer can all have an opinion at
// once. A bool could only record whether the foreground-process detector had
// claimed the window, which is why it was replaced by this.
//
// The rule is one line: a source may write over a claim ranked at or below its
// own, and never over one ranked above it. A source updating its own claim is
// the same-rank case and is always allowed, so a harness can move a pane from
// working to needs_input without having to relinquish anything first.
//
// There is exactly one exception, and it is about staleness rather than about
// the ranking being wrong: a screen rule that can see a blocking prompt on the
// pane right now may write over a higher-ranked claim that has gone quiet while
// the pane painted over it. See blockerOverridesClaim for the conditions and
// releaseAgentBlockerOverride for how it is given back.
type AgentSource string

const (
	// AgentSourceReport is the harness (or its hook shim) calling set-agent-state
	// for itself. It is the default for a caller that names no source, so every
	// caller written before sources existed keeps its authority.
	AgentSourceReport AgentSource = "report"
	// AgentSourceOSC is an in-band escape sequence the pane emitted.
	AgentSourceOSC AgentSource = "osc"
	// AgentSourceScreen is a rule matched against the pane's rendered text.
	AgentSourceScreen AgentSource = "screen"
	// AgentSourceDetect is the foreground-process detector recognising an agent.
	// It is daemon-internal: the detector is the only thing that sets it, so it
	// is reported by get-agent-state but not accepted from a caller.
	AgentSourceDetect AgentSource = "detect"
	// AgentSourceStall is the output-stall heuristic, the last resort.
	AgentSourceStall AgentSource = "stall"
)

// AgentSourceNames lists the source values set-agent-state accepts, in rank
// order, for the verb's schema and for input validation. It is part of the
// public protocol surface; keep the values stable. AgentSourceDetect is absent
// deliberately: it is what the daemon's own detector claims, not something a
// caller reports.
var AgentSourceNames = []string{"report", "osc", "screen", "stall"}

// agentSourceByName maps every accepted wire value to its AgentSource.
var agentSourceByName = map[string]AgentSource{
	"report": AgentSourceReport,
	"osc":    AgentSourceOSC,
	"screen": AgentSourceScreen,
	"stall":  AgentSourceStall,
}

// ParseAgentSource resolves a wire value to an AgentSource, reporting whether it
// was accepted. An empty input is accepted and means AgentSourceReport: that is
// what makes the field optional, and it is the only default that leaves a caller
// predating this field with the authority it already had.
func ParseAgentSource(s string) (AgentSource, bool) {
	if s == "" {
		return AgentSourceReport, true
	}
	v, ok := agentSourceByName[s]
	return v, ok
}

// Name returns the wire spelling, mapping the unset source to "report" so a
// reader always gets an explicit value.
func (a AgentSource) Name() string {
	if a == "" {
		return string(AgentSourceReport)
	}
	return string(a)
}

// rank orders the sources. Only the ordering matters, not the numbers; they are
// spaced so a tier can be inserted between two without renumbering. An unset
// source ranks as a report, matching what Name reports it as; a window with no
// claim at all is a separate case, and is open to any source.
func (a AgentSource) rank() int {
	switch a {
	case AgentSourceOSC:
		return 30
	case AgentSourceScreen:
		return 20
	case AgentSourceDetect:
		return 10
	case AgentSourceStall:
		return 0
	default: // AgentSourceReport and unset
		return 40
	}
}

// agentClaim is who currently owns one window's agent state.
type agentClaim struct {
	// source is the ranked owner: a lower-ranked source may not write over it.
	source AgentSource
	// harness is the harness id the source named, empty when unknown.
	harness string
	// auto records that the foreground-process detector promoted this window and
	// so is the one that clears it when the agent leaves the foreground. It is a
	// lifecycle claim, not a precedence one, which is why it survives a
	// higher-ranked source taking the state over: an explicit report during the
	// agent's run wins, and the pane still clears when the agent exits.
	auto bool
	// blocker marks a claim taken through the visible-blocker exception, and
	// prior is the claim it displaced. They are kept together because the
	// exception is a loan: the moment a later look finds the prompt gone, prior
	// goes back exactly as it was.
	blocker bool
	prior   agentPriorClaim
}

// agentPriorClaim is what a visible-blocker override displaced, held so the
// override can be undone rather than only outranked. Without it the pane would
// have no way back off needs_input: the screen tier asserts that state and no
// other, so nothing else on the pane would ever move it.
type agentPriorClaim struct {
	source  AgentSource
	state   AgentState
	harness string
}

// setAgentClaim records a claim, allocating the map on first use. It is called
// under stateMu, like every other read and write of agentClaims.
func (s *Session) setAgentClaim(windowID string, c agentClaim) {
	if s.agentClaims == nil {
		s.agentClaims = make(map[string]agentClaim)
	}
	s.agentClaims[windowID] = c
}

// agentClaimFor returns the claim on a window, or the zero claim when nothing
// has claimed it. It takes the state read lock, so it is safe for a verb handler
// to call.
func (s *Session) agentClaimFor(windowID string) agentClaim {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.agentClaims[windowID]
}
