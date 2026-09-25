// Package harness holds the registry of coding-agent CLIs tuios knows how to
// recognise, as data rather than code.
//
// It exists so adding a harness that shipped this morning is a file a user
// drops in a directory, not a tuios release. The registry answers one question
// well: which harness, if any, is this process. It deliberately does not answer
// "what is that harness doing"; the sources that can answer that honestly are the
// harness reporting for itself and the escape sequences it emits, and both of
// those reach the daemon without going through here.
package harness

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// SchemaVersion is the manifest format this build understands. A manifest
// naming a different version fails to load by name rather than being skipped:
// a detection registry that silently ignores files rots without anyone noticing.
const SchemaVersion = 1

// Manifest describes one harness.
type Manifest struct {
	SchemaVersion int    `toml:"schema_version"`
	ID            string `toml:"id"`
	DisplayName   string `toml:"display_name"`
	// Priority breaks a tie when two manifests match the same process. Higher
	// wins; equal priorities fall back to the id so the answer is stable.
	Priority int    `toml:"priority"`
	Detect   Detect `toml:"detect"`
	Screen   Screen `toml:"screen"`
	Title    Title  `toml:"title"`
	// Notify is the rules matched against a desktop notification the program
	// sent with OSC 9, OSC 777 or OSC 99. It has the shape of Title.
	Notify     Title      `toml:"notify"`
	Transcript Transcript `toml:"transcript"`
	// Input is how a prompt is typed into the harness. See Input.
	Input Input `toml:"input"`
	// Resume is how a conversation of the harness is reopened. See Resume.
	Resume Resume `toml:"resume"`

	// source is where the manifest was loaded from: "bundled" or the path of
	// a user file. replacedBundled is true for a user file that took the
	// place of a bundled manifest with the same id. Both are set by Load.
	source          string
	replacedBundled bool
}

// Source says where the manifest was loaded from, "bundled" or a file path,
// and whether that file replaced a bundled manifest with the same id.
func (m *Manifest) Source() (source string, replacedBundled bool) {
	return m.source, m.replacedBundled
}

// Title is the rules matched against the pane's window title, the string the
// program sets with OSC 0 or OSC 2.
//
// It is a channel the agents already use and tuios parsed and threw away.
// Claude Code puts a braille spinner in the title while it works and a mark
// when it is idle; Codex writes "Action Required" there when it is blocked. A
// title is one short string the program chose to publish about itself, which
// makes it cheaper to read than the screen and less likely to rot than a rule
// written against a rendered frame.
//
// Two things bound what it is allowed to do, and both are about what a title
// can honestly prove.
//
// It never creates an identity claim. A spinner proves that something is
// animating, not that the something is an agent, and any program can set any
// title; a pane with no claim has nothing for a title rule to move. So these
// rules only ever move the state of a pane some other tier already recognised.
//
// And a substring here has to match whole tokens. The screen tier matches
// anywhere because a rendered frame is prose, but a title is mostly paths and
// names: a rule for "opencode" matching a pane sitting in ~/src/opencode-blinker
// would be a false positive with the agent's own name on it. See containsToken.
type Title struct {
	Enabled bool `toml:"enabled"`
	// FoldCase lowercases the title and every substring predicate before
	// matching, exactly as Screen.FoldCase does for the screen.
	FoldCase bool         `toml:"fold_case"`
	Rule     []ScreenRule `toml:"rule"`

	// order is as Screen.order.
	order []int
}

// Detect is how a process is recognised as this harness. Any one predicate
// matching is enough; they are alternatives, not requirements, because the same
// harness looks different installed from npm, from pip, or as a native binary.
type Detect struct {
	// Comm matches the base name of the process name the kernel reports.
	Comm []string `toml:"comm"`
	// Argv0 matches the base name of argv[0], with a script extension stripped,
	// and the base name of the token an interpreter was asked to run. Both are
	// "the name of the program this process is": a Gemini CLI launched from a bun
	// shim runs as "node /home/u/.bun/bin/gemini" with comm rewritten to
	// "MainThread", and the only place it says gemini is that second token.
	Argv0 []string `toml:"argv0"`
	// ArgvPath matches a package name in the path of the token an interpreter was
	// asked to run. It is how an npm package name identifies a harness whose
	// entry point is a generic cli.js. It is the one predicate that reads argv,
	// so it is the one predicate that is gated: see ProcInfo.RunToken for why
	// only one token is read, and packageSegments for why the name has to sit
	// under a package directory rather than anywhere in the path.
	ArgvPath []string `toml:"argv_path"`
	// ExeGlob matches the resolved executable path against a component-wise glob.
	// It is how an installer that names its binary after the release is
	// recognised by the directory it installs into. "*" and "?" stay inside one
	// component, "**" spans any number, and a pattern that does not start with
	// "/" matches any suffix of the path.
	ExeGlob []string `toml:"exe_glob"`
	// Require is corroboration a bare-name match must have. See Require.
	Require Require `toml:"require"`

	// argvSegments is ArgvPath pre-split into components, filled by normalize.
	argvSegments [][]string
}

// Require is the evidence a name match needs beyond the name itself.
//
// It exists because a name is not always enough to be worth acting on. "pi" is a
// coding agent and also a plotting tool, a pi calculator and a plausible alias;
// matching every process called pi mislabels panes, and refusing to match any
// leaves a real harness undetectable. Corroboration is the way out: pi is a
// Node program, so a process called pi whose executable is a node runtime is pi,
// and one called pi that is a static binary somebody wrote is not.
//
// It constrains only the bare-name predicates, Comm and Argv0. ArgvPath and
// ExeGlob already name a specific install layout and carry their own evidence.
// An empty Require constrains nothing, which is what almost every manifest wants.
type Require struct {
	// ExeBase matches the base name of the resolved executable.
	ExeBase []string `toml:"exe_base"`
	// ExeGlob matches the resolved executable path, with the same component-wise
	// glob syntax as Detect.ExeGlob.
	ExeGlob []string `toml:"exe_glob"`
}

// any reports whether any corroboration is demanded at all.
func (r *Require) any() bool { return len(r.ExeBase)+len(r.ExeGlob) > 0 }

// satisfied reports whether a process carries the corroboration. An unreadable
// executable fails it: silence is not evidence, and a name that needed backing up
// and did not get it must not match.
func (r *Require) satisfied(p ProcInfo) bool {
	if !r.any() {
		return true
	}
	if p.Exe == "" {
		return false
	}
	if contains(r.ExeBase, p.ExeBase()) {
		return true
	}
	for _, pattern := range r.ExeGlob {
		if matchExeGlob(pattern, p.Exe) {
			return true
		}
	}
	return false
}

// Screen holds optional rules matched against a pane's rendered text.
//
// Bundled manifests ship needs_input rules, and working and idle rules for the
// harnesses whose screens have a stable shape to key on. An idle rule has to
// prove an input box is on the screen, with region = "prompt_box" or with a
// regex pinning the box's structure, because the absence of work is not
// evidence of rest (the policy test in registry_test.go makes the full
// argument). The daemon also holds an idle verdict through a short
// confirmation window before it publishes it, so a redraw cannot flap the
// pane. A rule here is coupled to one agent's TUI at one version,
// and agent TUIs change in patch releases, so a rule that silently stops matching
// degrades to no opinion without telling anyone. The signals tuios prefers (the
// harness reporting for itself, and the escape sequences it emits) are
// contractual and do not rot, which is why this is the last resort rather than
// the foundation.
type Screen struct {
	Enabled bool `toml:"enabled"`
	// FoldCase lowercases the screen text and every substring predicate before
	// matching, so a rule written in lowercase matches however the TUI cases its
	// prompt this release. Regex predicates are exempt: a pattern opts into
	// folding with (?i), and folding the text under one that did not ask would
	// silently change what its character classes mean. Manifests converted from
	// herdr need this on, because herdr always matches substrings case-folded.
	FoldCase bool `toml:"fold_case"`
	// Lines is how many non-empty lines from the bottom of the pane a rule
	// sees. A rule reading bottom_non_empty_lines(N) must fit inside it; the
	// loader refuses one that asks for more rather than widening what every
	// other rule reads.
	Lines int          `toml:"lines"`
	Rule  []ScreenRule `toml:"rule"`

	// order is the rule indices highest priority first, ties in declaration
	// order, so a scan can stop at the first rule that matches. Filled by
	// parseManifest.
	order []int
}

// scanOrder returns order when it was built for rules, and builds it when it
// was not, for a manifest assembled in code rather than parsed.
func scanOrder(order []int, rules []ScreenRule) []int {
	if len(order) == len(rules) {
		return order
	}
	return priorityOrder(rules)
}

// firstMatch walks rules in scan order and returns the first one match
// accepts: the best rule, since the order is highest priority first with ties
// in declaration order.
func firstMatch(order []int, rules []ScreenRule, match func(*ScreenRule) bool) (state string, rule int, ok bool) {
	for _, i := range scanOrder(order, rules) {
		if match(&rules[i]) {
			return rules[i].State, i, true
		}
	}
	return "", -1, false
}

// priorityOrder returns the indices of rules, highest priority first and ties
// in declaration order, which is the order a scan considers them in.
func priorityOrder(rules []ScreenRule) []int {
	order := make([]int, len(rules))
	for i := range order {
		order[i] = i
	}
	// cmp.Compare rather than a subtraction: priority is whatever integer a
	// user wrote, and the difference of two far apart ones overflows, which
	// sorted a lower priority rule first and made Classify disagree with
	// Explain (FuzzManifest).
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(rules[b].Priority, rules[a].Priority) })
	return order
}

// ScreenRule is one rule: a state, and the gate that says when the rule's
// region shows it. The same shape serves screen, title and notify rules. See
// Gate for how the predicates combine.
type ScreenRule struct {
	State    string `toml:"state"`
	Priority int    `toml:"priority"`
	// Message is the reason the rule reports with its state, in plain words:
	// what the agent waits for. A blocked state is one state with a reason
	// attached, not a family of states, and this is where the reason lives.
	Message string `toml:"message"`
	// Kind says what sort of block a needs_input rule reads: "approval" for a
	// prompt that wants a yes or a no on something the agent proposed, and
	// "question" for one that wants an answer in words. It fronts the prompt
	// line in the pane's message ("approval: Do you want to ..."). Empty means
	// guess from the rule's own words; see RuleKind.
	Kind string `toml:"kind"`
	// Gate holds the predicates, flat and nested. Regex and NotRegex hold RE2
	// patterns, compiled once at load and matched with ^ and $ anchoring lines
	// rather than the whole region, because a region is lines and a rule
	// almost always means "some line looks like this". RE2 guarantees
	// matching linear in the text, so a pathological pattern can cost a load
	// error but never a stalled screen scan.
	Gate
	// Region is the part of what the pane shows that the rule reads; see
	// region.go for the names. A screen rule reads the whole tail by default,
	// a title rule the title. A rule reading a box region matches nothing when
	// no box is on the screen, which is what lets an idle rule prove a prompt
	// box is there. Notify rules take no region.
	Region string `toml:"region"`
	// Answers says which keys answer the prompt a needs_input rule reads, so
	// a person can answer it without attaching. See answers.go. Empty on
	// every other rule.
	Answers Answers `toml:"answers"`
}

// maxScreenPattern bounds one regex pattern's length. RE2 compiles a pattern
// into a program roughly proportional to its size, and the screen scan runs in
// the daemon on every settle; a pattern too long to read is refused at load,
// where the error names the file, rather than priced on the hot path.
const maxScreenPattern = 512

// defaultScreenLines is how much of the pane bottom a screen rule sees when a
// manifest does not say. It is small because agent TUIs draw their live state in
// a box at the bottom, and reading further up finds transcript history that says
// what the agent did rather than what it is doing.
const defaultScreenLines = 6

// DefaultScreenLines is defaultScreenLines for callers outside the package. The
// diagnostic dumps a pane's tail before it knows whether a harness has been
// named at all, so it needs an answer no manifest can give it.
const DefaultScreenLines = defaultScreenLines

// genericNameLimit is the length below which a bare name is too generic to
// identify a harness on its own. Names like "pi", "cn" and "amp" are plausible
// commands for unrelated software, and a false positive labels an innocent pane
// as an agent, which is worse than missing a real one.
const genericNameLimit = 5

// parseManifest reads one manifest and checks it says enough to be usable. The
// error names the file, since the whole point of the registry is that a user
// edits these by hand.
func parseManifest(name string, data []byte) (*Manifest, error) {
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if m.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%s: schema_version %d, this build understands %d",
			name, m.SchemaVersion, SchemaVersion)
	}
	if m.ID = strings.TrimSpace(m.ID); m.ID == "" {
		return nil, fmt.Errorf("%s: no id", name)
	}
	m.Detect.normalize()
	if !m.Detect.any() {
		return nil, fmt.Errorf("%s: manifest %q matches nothing", name, m.ID)
	}
	if err := m.Detect.checkGenericNames(name, m.ID); err != nil {
		return nil, err
	}
	if m.Screen.Lines <= 0 {
		m.Screen.Lines = defaultScreenLines
	}
	if m.Screen.Lines > maxRegionLines {
		return nil, fmt.Errorf("%s: manifest %q screen lines %d, limit %d", name, m.ID, m.Screen.Lines, maxRegionLines)
	}
	// Title and notify rules are the same shape as screen rules and are checked
	// the same way, so a mistake in one is reported in the same words as a
	// mistake in the other. One budget covers the whole file.
	var budget gateBudget
	for i := range m.Screen.Rule {
		if err := m.Screen.Rule[i].check("screen", screenStates, m.Screen.FoldCase, m.Screen.Lines, &budget); err != nil {
			return nil, fmt.Errorf("%s: manifest %q screen rule %d: %w", name, m.ID, i, err)
		}
	}
	for i := range m.Title.Rule {
		if err := m.Title.Rule[i].check("title", screenStates, m.Title.FoldCase, 0, &budget); err != nil {
			return nil, fmt.Errorf("%s: manifest %q title rule %d: %w", name, m.ID, i, err)
		}
	}
	for i := range m.Notify.Rule {
		if err := m.Notify.Rule[i].check("notify", notifyStates, m.Notify.FoldCase, 0, &budget); err != nil {
			return nil, fmt.Errorf("%s: manifest %q notify rule %d: %w", name, m.ID, i, err)
		}
	}
	m.Screen.order = priorityOrder(m.Screen.Rule)
	m.Title.order = priorityOrder(m.Title.Rule)
	m.Notify.order = priorityOrder(m.Notify.Rule)
	if err := m.Input.normalize(); err != nil {
		return nil, fmt.Errorf("%s: manifest %q input: %w", name, m.ID, err)
	}
	if err := m.Resume.normalize(); err != nil {
		return nil, fmt.Errorf("%s: manifest %q resume: %w", name, m.ID, err)
	}
	if err := checkTranscript(name, m.ID, &m.Transcript); err != nil {
		return nil, err
	}
	if m.DisplayName == "" {
		m.DisplayName = m.ID
	}
	return &m, nil
}

// check validates one rule of the given block and compiles it. block is
// "screen", "title" or "notify", states is what that block may assert, lines
// is how far up a screen rule may read, and b counts the file's predicates.
func (r *ScreenRule) check(block string, states map[string]struct{}, foldCase bool, lines int, b *gateBudget) error {
	if _, ok := states[r.State]; !ok {
		return fmt.Errorf("unknown state %q", r.State)
	}
	if err := r.Gate.compile(foldCase, 0, b); err != nil {
		return err
	}
	if r.Kind = strings.ToLower(strings.TrimSpace(r.Kind)); r.Kind != "" && !promptKinds[r.Kind] {
		return fmt.Errorf("unknown kind %q (approval or question)", r.Kind)
	}
	if err := r.Answers.check(block, r.State); err != nil {
		return err
	}
	switch block {
	case "notify":
		if strings.TrimSpace(r.Region) != "" {
			return fmt.Errorf("region %q: a notify rule reads the notification and takes no region", r.Region)
		}
		return nil
	case "title":
		region, err := normalizeTitleRegion(r.Region)
		if err != nil {
			return err
		}
		r.Region = region
		return nil
	}
	region, bottom, err := normalizeScreenRegion(r.Region)
	if err != nil {
		return err
	}
	if bottom > lines {
		return fmt.Errorf("region %s reads past the %d lines the manifest reads; raise [screen] lines", region, lines)
	}
	r.Region = region
	// An idle rule has to prove the agent is at rest, and the proof is an input
	// box on the screen. Reading the box region is that proof; a pattern on
	// every path to a match can be, when it pins the box's own structure. A
	// rule resting on a loose substring would call a pane idle because a word
	// appeared in its output.
	if r.State == "idle" && r.Region != RegionPromptBox && !r.provesShape() {
		return fmt.Errorf("an idle rule must read region = %q or carry a regex that pins the input box", RegionPromptBox)
	}
	return nil
}

// compile compiles a rule on its own, the way parseManifest does, for a rule
// built in code rather than read from a file.
func (r *ScreenRule) compile(foldCase bool) error {
	return r.Gate.compile(foldCase, 0, &gateBudget{})
}

// compilePatterns compiles each pattern with (?m), so ^ and $ mean lines: the
// haystack is a pane tail joined with newlines, and anchoring the whole blob
// is never what a rule about one rendered line wants.
func compilePatterns(patterns []string) ([]*regexp.Regexp, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	out := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		if len(p) > maxScreenPattern {
			return nil, fmt.Errorf("pattern %d is %d bytes, limit %d", i, len(p), maxScreenPattern)
		}
		re, err := regexp.Compile("(?m)" + p)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}
		out[i] = re
	}
	return out, nil
}

// screenStates are the states a screen rule may assert. It is deliberately
// narrower than the full agent-state set: a rule reading someone else's UI can
// credibly spot that it is asking a question or showing a spinner, and cannot
// credibly tell "finished" from "finished and errored".
var screenStates = map[string]struct{}{
	"working":     {},
	"needs_input": {},
	"idle":        {},
}

// notifyStates are the states a notify rule may assert. done is allowed here
// and nowhere else: a desktop notification is the harness itself speaking, and
// "the turn finished" is a claim only the harness can honestly make.
var notifyStates = map[string]struct{}{
	"working":     {},
	"needs_input": {},
	"idle":        {},
	"done":        {},
}

// normalize lowercases and trims every predicate so matching can be a plain
// lookup against an already-lowercased base name.
func (d *Detect) normalize() {
	for _, list := range []*[]string{&d.Comm, &d.Argv0, &d.ArgvPath, &d.ExeGlob} {
		out := (*list)[:0]
		for _, v := range *list {
			if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
				out = append(out, v)
			}
		}
		*list = out
	}
	// Kept index-aligned with ArgvPath so a match can name the pattern it was
	// written as, which is the string the manifest author will search for.
	d.argvSegments = make([][]string, len(d.ArgvPath))
	for i, want := range d.ArgvPath {
		d.argvSegments[i] = segments(want)
	}
}

// any reports whether the manifest carries any predicate at all.
func (d *Detect) any() bool {
	return len(d.Comm)+len(d.Argv0)+len(d.ArgvPath)+len(d.ExeGlob) > 0
}

// checkGenericNames enforces the collision policy: a short bare name may only
// match with corroboration.
//
// Only [detect.require] constrains a name, so only that satisfies this. Another
// predicate standing alongside the name does not: predicates are alternatives,
// so an argv_path next to comm = ["pi"] does not constrain the comm match and
// every process called pi would still match.
func (d *Detect) checkGenericNames(file, id string) error {
	if d.Require.any() {
		return nil
	}
	for _, n := range append(append([]string{}, d.Comm...), d.Argv0...) {
		if len(n) < genericNameLimit {
			return fmt.Errorf(
				"%s: manifest %q matches on the generic name %q alone; add a [detect.require] block",
				file, id, n)
		}
	}
	return nil
}

// matches reports whether a process is this harness, and names the predicate
// that decided it so a diagnostic can quote the rule rather than the verdict.
//
// The predicates are ordered by how much of the process's own identity they read.
// comm, argv0 and exe describe what the process is; argv describes what it was
// handed, and only an interpreter's argv stands in for its identity, which is why
// run is empty for everything else.
func (d *Detect) matches(p ProcInfo, run string) (string, bool) {
	if d.Require.satisfied(p) {
		if comm := p.CommBase(); contains(d.Comm, comm) {
			return "comm=" + comm, true
		}
		if argv0 := p.Argv0Base(); contains(d.Argv0, argv0) {
			return "argv0=" + argv0, true
		}
		if runName := BaseName(run); run != "" && contains(d.Argv0, runName) {
			return "argv0=" + runName + " (run token)", true
		}
	}
	if p.Exe != "" {
		for _, pattern := range d.ExeGlob {
			if matchExeGlob(pattern, p.Exe) {
				return "exe_glob=" + pattern, true
			}
		}
	}
	if run != "" {
		have := segments(run)
		for i, want := range d.argvSegments {
			if packageSegments(have, want) {
				return "argv_path=" + d.ArgvPath[i], true
			}
		}
	}
	return "", false
}

func contains(list []string, v string) bool {
	if v == "" {
		return false
	}
	return slices.Contains(list, v)
}
