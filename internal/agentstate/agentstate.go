// Package agentstate names the states an agent in a pane can be in and the
// sources that can say so. The daemon, the client and the packages between
// them read these one definition, so a state is added, ranked and named in
// one place. The wire carries the string value of each.
//
// It imports nothing from tuios, so every package can use it.
package agentstate

// State is what an agent in a pane is doing. The value is the wire string.
type State string

const (
	// None is the default: the pane is not running an agent, or is not
	// reporting. It serializes as the empty string so it is omitted from state.
	None State = ""
	// Working means the agent is actively working on a task.
	Working State = "working"
	// NeedsInput means the agent is blocked waiting for the user.
	NeedsInput State = "needs_input"
	// Idle means the agent is not working and not blocked; it is the state the
	// output-stall heuristic assigns to a pane that went quiet.
	Idle State = "idle"
	// Done means the agent finished its task.
	Done State = "done"
	// Errored means the agent stopped because of an error.
	Errored State = "errored"
	// Unknown means an agent is present and nothing says what it is doing. It
	// is what the silence timer writes when the screen tier looked and found
	// nothing, in place of idle: idle says nothing needs you, and a pane that
	// went quiet on a prompt no rule knows would be lying.
	Unknown State = "unknown"
	// LimitReached means the agent stopped on its harness's usage or rate
	// limit. It is not an error and not a prompt: nothing a person types
	// moves it before the limit resets, so it does not count as needing
	// you. The harness's words for the reset, when it gave any, are in the
	// pane's message.
	LimitReached State = "limit_reached"
)

// Info is what every reader may need about a state. One row per state; a
// value this build does not know has the zero row with its own name.
type Info struct {
	// Name is the wire spelling, "none" for None.
	Name string
	// Rank orders states for a roll-up: the most urgent wins. RankSeen is the
	// rank once a person has looked at the pane; it differs only for done, so
	// a finished agent stops being urgent the moment it is seen.
	Rank, RankSeen int
	// NeedsYou says a person has to act on the pane now.
	NeedsYou bool
	// Activity is the coarse reading: working, waiting, resting, unknown or
	// none.
	Activity string
}

// The ranks are spaced so a state can be added between two others without
// renumbering. Only the order is meaningful: errored > needs_input >
// limit_reached unseen > done unseen > working > limit_reached seen >
// done seen > idle > unknown > none.
//
// limit_reached ranks below needs_input, which a person can answer now, and
// above done, since the agent did not finish. Once seen it drops below
// working, as done does, and stays above done seen: the agent still has work
// it could not do.
//
// unknown ranks above none: there is something in that pane, which is more
// than none says. It ranked zero once, so a session whose only agent was in
// that state rolled up to nothing, and that is the state the stall timer
// writes for any agent with no screen rules.
func info(s State) (Info, bool) {
	switch s {
	case None:
		return Info{Name: "none", Activity: "none"}, true
	case Working:
		return Info{Name: "working", Rank: 6, RankSeen: 6, Activity: "working"}, true
	case NeedsInput:
		return Info{Name: "needs_input", Rank: 10, RankSeen: 10, NeedsYou: true, Activity: "waiting"}, true
	case Idle:
		return Info{Name: "idle", Rank: 2, RankSeen: 2, Activity: "resting"}, true
	case Done:
		return Info{Name: "done", Rank: 8, RankSeen: 4, Activity: "resting"}, true
	case Errored:
		return Info{Name: "errored", Rank: 12, RankSeen: 12, NeedsYou: true, Activity: "resting"}, true
	case Unknown:
		return Info{Name: "unknown", Rank: 1, RankSeen: 1, Activity: "unknown"}, true
	case LimitReached:
		return Info{Name: "limit_reached", Rank: 9, RankSeen: 5, Activity: "resting"}, true
	}
	return Info{Name: string(s), Activity: "none"}, false
}

// names is the accepted wire values in a stable order. It is part of the
// public protocol surface; keep the values stable.
var names = []string{"none", "working", "needs_input", "idle", "done", "errored", "unknown", "limit_reached"}

// Info returns the row for s.
func (s State) Info() Info {
	i, _ := info(s)
	return i
}

// Name returns the wire spelling of the state, mapping the empty None back to
// "none" so a reader always gets an explicit value.
func (s State) Name() string { return s.Info().Name }

// NeedsYou reports whether a person has to act on the pane now: a blocked
// agent and one that stopped on an error.
func (s State) NeedsYou() bool { return s.Info().NeedsYou }

// Activity is the coarse reading of a state: what the agent is doing, with the
// reason for a block left to the message.
func (s State) Activity() string { return s.Info().Activity }

// RankFor is the state's roll-up rank, the seen rank when seen is set.
func (s State) RankFor(seen bool) int {
	if seen {
		return s.Info().RankSeen
	}
	return s.Info().Rank
}

// Parse resolves a wire value to a State, reporting whether the value was one
// of the accepted names. An empty input is not accepted: a caller names a
// state, and "none" is the spelling that clears it.
func Parse(name string) (State, bool) {
	if name == "" {
		return None, false
	}
	if name == "none" {
		return None, true
	}
	if _, ok := info(State(name)); ok {
		return State(name), true
	}
	return None, false
}

// Names lists the accepted wire values in a stable order.
func Names() []string { return append([]string(nil), names...) }

// Source is who said what state a pane is in. The sources are ranked: a
// source below the one holding a pane's state cannot take it over.
type Source string

const (
	// Report is the harness (or its hook shim) calling set-agent-state for
	// itself. It is the default for a caller that names no source.
	Report Source = "report"
	// Transcript is the record file the harness writes as it runs, read by
	// the daemon. It sits below Report for latency, not trust. Daemon-internal.
	Transcript Source = "transcript"
	// Program is the pane's own OSC 7501 report. Daemon-internal.
	Program Source = "program"
	// OSC is an in-band escape sequence the pane emitted.
	OSC Source = "osc"
	// Screen is a rule matched against the pane's rendered text.
	Screen Source = "screen"
	// Detect is the foreground-process detector recognising an agent.
	// Daemon-internal.
	Detect Source = "detect"
	// Stall is the output-stall heuristic, the last resort.
	Stall Source = "stall"
)

// sourceNames are the sources a caller may name, in rank order. Detect,
// Transcript and Program are absent: only the daemon sets them.
var sourceNames = []string{"report", "osc", "screen", "stall"}

// SourceNames lists the source values a caller may name, in rank order.
func SourceNames() []string { return append([]string(nil), sourceNames...) }

// ParseSource resolves a wire value to a Source a caller may name. An empty
// input is accepted and means Report, which keeps the authority of a caller
// predating sources.
func ParseSource(name string) (Source, bool) {
	switch name {
	case "":
		return Report, true
	case "report", "osc", "screen", "stall":
		return Source(name), true
	}
	return "", false
}

// Name returns the wire spelling, mapping the unset source to "report".
func (s Source) Name() string {
	if s == "" {
		return string(Report)
	}
	return string(s)
}

// Rank orders the sources. Only the order matters; the numbers are spaced so a
// tier can be inserted. An unset source ranks as a report.
func (s Source) Rank() int {
	switch s {
	case Program:
		return 38
	case Transcript:
		return 35
	case OSC:
		return 30
	case Screen:
		return 20
	case Detect:
		return 10
	case Stall:
		return 0
	default: // Report and unset
		return 40
	}
}
