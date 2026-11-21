package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/agentstate"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

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
type AgentSource = agentstate.Source

// The sources, under the names this package has always used. Their ranks
// are agentstate.Source.Rank.
const (
	AgentSourceReport     = agentstate.Report
	AgentSourceTranscript = agentstate.Transcript
	AgentSourceProgram    = agentstate.Program
	AgentSourceOSC        = agentstate.OSC
	AgentSourceScreen     = agentstate.Screen
	AgentSourceDetect     = agentstate.Detect
	AgentSourceStall      = agentstate.Stall
)

// AgentSourceNames lists the source values set-agent-state accepts, in rank
// order. Detect, Transcript and Program are absent: only the daemon sets them,
// and get-agent-state still reports them.
var AgentSourceNames = agentstate.SourceNames()

// ParseAgentSource resolves a wire value to an AgentSource. See
// agentstate.ParseSource.
func ParseAgentSource(s string) (AgentSource, bool) { return agentstate.ParseSource(s) }

type agentClaim struct {
	source       AgentSource
	harness      string
	identity     identityTier
	misses       int
	auto         bool
	sawProcess   bool
	blocker      bool
	prior        agentPriorClaim
	event        bool
	herdrAt      int64
	herdrAnchors []herdrAnchor
	screenWins   bool
	promptHold   int64
	holdFg       int
	heardAt      int64
	subagent     string
}

type herdrAnchor struct {
	pid   int
	start uint64
}

func (a herdrAnchor) alive() bool {
	st, ok := procinfo.StartTime(a.pid)
	return ok && st == a.start
}

const herdrShellGrace = 2 * time.Second

func (c agentClaim) herdrClaimLapsed(now int64) bool {
	if c.herdrAt == 0 || now-c.herdrAt < int64(herdrShellGrace) {
		return false
	}
	for _, a := range c.herdrAnchors {
		if a.alive() {
			return false
		}
	}
	return true
}

type agentPriorClaim struct {
	source       AgentSource
	state        AgentState
	harness      string
	herdrAt      int64
	herdrAnchors []herdrAnchor
	screenWins   bool
	message      string
	kind         string
	event        bool
}

func (s *Session) setAgentClaim(windowID string, c agentClaim) {
	if s.agentClaims == nil {
		s.agentClaims = make(map[string]agentClaim)
	}
	s.agentClaims[windowID] = c
}

func (s *Session) agentClaimFor(windowID string) agentClaim {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.agentClaims[windowID]
}

func (s *Session) yieldAgentClaim(windowID string, source AgentSource) bool {
	yielded := false
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if claim, held := s.agentClaims[windowID]; held && claim.source == source {
		if claim.auto || claim.sawProcess {
			s.agentClaims[windowID] = agentClaim{
				source: AgentSourceDetect, harness: claim.harness, auto: claim.auto, sawProcess: claim.sawProcess,
			}
		} else {
			delete(s.agentClaims, windowID)
		}
		yielded = true
	}
	return yielded
}
