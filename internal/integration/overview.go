package integration

import "fmt"

// The report tuios doctor agents prints and the settings page's Agents tab
// draws. Both read it from here, so the two can never disagree about what is
// installed, what is out of date and what has no integration at all.

// Overview is every harness tuios can integrate with, as this machine has it.
type Overview struct {
	// TuiosOnPath says the program the hooks run can be found.
	TuiosOnPath bool
	// Harnesses is one status per harness with an integration, in Targets
	// order.
	Harnesses []Status
	// Unsupported lists the harnesses tuios recognises and has no
	// integration for, each with the reason.
	Unsupported []Unsupported
}

// BuildOverview reads every harness's integration. command is the program a
// current install runs, normally "tuios". It reads files and searches PATH,
// so a caller on a UI goroutine runs it somewhere else.
func BuildOverview(env Env, command string) Overview {
	r := Overview{Unsupported: UnsupportedHarnesses()}
	for _, t := range Targets() {
		st := t.Status(env, command)
		r.Harnesses = append(r.Harnesses, st)
		r.TuiosOnPath = st.TuiosOnPath
	}
	return r
}

// Lookup is the status of one harness, by id or alias.
func (r Overview) Lookup(harness string) (Status, bool) {
	id, ok := Canonical(harness)
	if !ok {
		return Status{}, false
	}
	for _, s := range r.Harnesses {
		if s.Harness == id {
			return s, true
		}
	}
	return Status{}, false
}

// State is a harness's integration in one word, for a person to read.
type State int

const (
	// StateInstalled is an integration that is installed and current.
	StateInstalled State = iota
	// StateOutOfDate is an integration that is installed by an older tuios,
	// or that runs another program. Install replaces it.
	StateOutOfDate
	// StateNotInstalled is a harness with an integration that is not
	// installed, on a machine where the harness has run.
	StateNotInstalled
	// StateNotRun is a harness whose configuration directory does not exist.
	// Install refuses until the harness has run once.
	StateNotRun
)

// String is the words the CLI uses for the state.
func (s State) String() string {
	switch s {
	case StateInstalled:
		return "installed"
	case StateOutOfDate:
		return "out of date"
	default:
		return "not installed"
	}
}

// State is the status in one word.
func (s Status) State() State {
	switch {
	case s.Installed && s.Current:
		return StateInstalled
	case s.Installed:
		return StateOutOfDate
	case !s.ConfigDirExists:
		return StateNotRun
	default:
		return StateNotInstalled
	}
}

// NeedsAction reports whether install would fix the integration: it is out
// of date, or it is not installed and the harness has run here.
func (s Status) NeedsAction() bool {
	st := s.State()
	return st == StateOutOfDate || st == StateNotInstalled
}

// Verdict is the status in a few words, as tuios integration status and
// tuios doctor agents print it.
func (s Status) Verdict() string {
	switch s.State() {
	case StateInstalled:
		return fmt.Sprintf("installed, current (v%d)", s.Version)
	case StateOutOfDate:
		return fmt.Sprintf("installed, out of date (v%d, this tuios installs v%d): run tuios integration install %s", s.Version, s.WantVersion, s.Harness)
	case StateNotRun:
		return "not installed; " + s.Name + " has not run here"
	default:
		return "not installed: run tuios integration install " + s.Harness
	}
}

// RemovalStep is one part of what UninstallAll removed: the hook entries, the
// status line, or the MCP server.
type RemovalStep struct {
	// Part is "" for the hook entries, "status line" or "MCP server".
	Part   string
	Result Result
	Err    error
}

// UninstallAll removes everything tuios integration install can write for
// the harness: the hook entries or the plugin, the status line, and the MCP
// server entry. Every part is tried, and each says what it did.
func (t *Target) UninstallAll(env Env) []RemovalStep {
	res, err := t.Uninstall(env)
	steps := []RemovalStep{{Result: res, Err: err}}
	if t.SupportsStatusLine() {
		res, err := t.UninstallStatusLine(env)
		steps = append(steps, RemovalStep{Part: "status line", Result: res, Err: err})
	}
	if t.SupportsMCP() {
		res, err := t.UninstallMCP(env)
		steps = append(steps, RemovalStep{Part: "MCP server", Result: res, Err: err})
	}
	return steps
}
