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
	"fmt"
	"path"
	"path/filepath"
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
	Priority   int        `toml:"priority"`
	Detect     Detect     `toml:"detect"`
	Screen     Screen     `toml:"screen"`
	Transcript Transcript `toml:"transcript"`
}

// Detect is how a process is recognised as this harness. Any one predicate
// matching is enough; they are alternatives, not requirements, because the same
// harness looks different installed from npm, from pip, or as a native binary.
type Detect struct {
	// Comm matches the base name of /proc/<pid>/comm.
	Comm []string `toml:"comm"`
	// Argv0 matches the base name of argv[0], with a script extension stripped.
	Argv0 []string `toml:"argv0"`
	// ArgvPath matches a substring of any argv token. It is how an npm package
	// name identifies a harness whose entry point is a generic cli.js.
	ArgvPath []string `toml:"argv_path"`
	// ExeGlob matches the resolved /proc/<pid>/exe against a shell glob. It is
	// how an installer that names its binary after the release is recognised by
	// the directory it installs into.
	ExeGlob []string `toml:"exe_glob"`
}

// Screen holds optional rules matched against a pane's rendered text.
//
// It ships disabled in every bundled manifest and has to be turned on per
// harness by the user. A rule here is coupled to one agent's TUI at one version,
// and agent TUIs change in patch releases, so a rule that silently stops matching
// degrades to no opinion without telling anyone. The signals tuios prefers (the
// harness reporting for itself, and the escape sequences it emits) are
// contractual and do not rot, which is why this is the last resort rather than
// the foundation.
type Screen struct {
	Enabled bool `toml:"enabled"`
	// Lines is how many lines from the bottom of the pane a rule sees.
	Lines int          `toml:"lines"`
	Rule  []ScreenRule `toml:"rule"`
}

// ScreenRule is one screen-text rule. The predicates combine as: every string in
// All must be present, at least one in Any must be present, and none in Not may
// be. An empty list is satisfied, so a rule with only Any is an "any of these".
type ScreenRule struct {
	State    string   `toml:"state"`
	Priority int      `toml:"priority"`
	All      []string `toml:"all"`
	Any      []string `toml:"any"`
	Not      []string `toml:"not"`
}

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
	for i, r := range m.Screen.Rule {
		if _, ok := screenStates[r.State]; !ok {
			return nil, fmt.Errorf("%s: manifest %q screen rule %d: unknown state %q",
				name, m.ID, i, r.State)
		}
	}
	if m.Screen.Lines <= 0 {
		m.Screen.Lines = defaultScreenLines
	}
	if err := checkTranscript(name, m.ID, &m.Transcript); err != nil {
		return nil, err
	}
	if m.DisplayName == "" {
		m.DisplayName = m.ID
	}
	return &m, nil
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
}

// any reports whether the manifest carries any predicate at all.
func (d *Detect) any() bool {
	return len(d.Comm)+len(d.Argv0)+len(d.ArgvPath)+len(d.ExeGlob) > 0
}

// checkGenericNames enforces the collision policy: a manifest identified only by
// a short bare name must also carry a path or glob predicate to go with it.
func (d *Detect) checkGenericNames(file, id string) error {
	if len(d.ArgvPath) > 0 || len(d.ExeGlob) > 0 {
		return nil
	}
	for _, n := range append(append([]string{}, d.Comm...), d.Argv0...) {
		if len(n) < genericNameLimit {
			return fmt.Errorf(
				"%s: manifest %q matches on the generic name %q alone; add argv_path or exe_glob",
				file, id, n)
		}
	}
	return nil
}

// matches reports whether a process is this harness. comm, argv0 and exe are
// expected already reduced to lowercase base names where that applies; argv is
// the raw command line and exe the raw resolved path, since those two are matched
// as paths rather than names.
func (d *Detect) matches(comm, argv0 string, argv []string, exe string) bool {
	if contains(d.Comm, comm) || contains(d.Argv0, argv0) {
		return true
	}
	for _, want := range d.ArgvPath {
		for _, arg := range argv {
			if strings.Contains(strings.ToLower(arg), want) {
				return true
			}
		}
	}
	if exe != "" {
		lower := strings.ToLower(exe)
		for _, pattern := range d.ExeGlob {
			// path.Match, not filepath.Match: the separator is always "/" here
			// because these are procfs paths, and matching must not change with
			// the host's separator.
			if ok, err := path.Match(pattern, lower); err == nil && ok {
				return true
			}
			// A bare directory name is the common case ("*/claude/*"), so also
			// try the pattern against each path element's parent join.
			if ok, err := filepath.Match(pattern, lower); err == nil && ok {
				return true
			}
		}
	}
	return false
}

func contains(list []string, v string) bool {
	if v == "" {
		return false
	}
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
