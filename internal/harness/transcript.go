package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Transcript says where a harness keeps its own record of a session, so the
// daemon can read what the agent said it did rather than infer it from the
// screen. A manifest with no [transcript] block gets exactly today's behaviour,
// which is what almost every harness will have: most keep no such file, and the
// ones that do keep it somewhere of their own choosing.
//
// The manifest says WHERE. A named Go reader says HOW. There is deliberately no
// expression language for deriving state from records: the same argument that
// keeps installer commands out of manifests applies harder here, because this
// code is pointed at a file holding the user's entire conversation, and a
// condition mini-DSL evaluated over that would be both a bug farm and a way to
// select content by writing data. Readers are reviewed, named, and in-tree.
type Transcript struct {
	// Reader names the in-tree implementation that understands the format. An
	// unknown name fails the manifest by name rather than being ignored.
	Reader string `toml:"reader"`
	// Dir is where the files live, with placeholders resolved by ExpandDir.
	Dir string `toml:"dir"`
	// Glob matches the session files inside Dir.
	Glob string `toml:"glob"`
	// Verify names the record fields that must match the pane before a file
	// found by search is believed. It is what stops a wrong join, which is worse
	// than no join: attributing one agent's state to another pane is a confident
	// lie, where no state is merely no state.
	Verify []string `toml:"verify"`
}

// ReaderJSONL is the only reader that exists today: one JSON object per line,
// newest record last, as Claude Code writes. The constant is the name a manifest
// spells, so adding a harness with the same layout is a manifest and no code.
const ReaderJSONL = "claude-code-jsonl"

// knownReaders is the set a manifest may name.
var knownReaders = map[string]struct{}{ReaderJSONL: {}}

// verifiableFields are the record fields Verify may name. They are the agent's
// own identity: which directory it was started in, which build it is, and which
// session it calls itself. None of them is conversation, which is why matching
// on them is allowed at all.
var verifiableFields = map[string]struct{}{
	"cwd": {}, "version": {}, "session_id": {},
}

// Enabled reports whether the manifest carries a usable transcript block.
func (t Transcript) Enabled() bool {
	return t.Reader != "" && t.Dir != "" && t.Glob != ""
}

// Verifies reports whether field is one this manifest asks to be checked.
func (t Transcript) Verifies(field string) bool {
	for _, f := range t.Verify {
		if f == field {
			return true
		}
	}
	return false
}

// checkTranscript validates the block, treating a half-written one as an error.
// A transcript block that silently does nothing is the failure this registry
// exists to avoid, and it matters more here than elsewhere: the user cannot see
// the source not working, they can only see a pane whose state never updates.
func checkTranscript(file, id string, t *Transcript) error {
	t.Reader = strings.TrimSpace(t.Reader)
	t.Dir = strings.TrimSpace(t.Dir)
	t.Glob = strings.TrimSpace(t.Glob)
	if t.Reader == "" && t.Dir == "" && t.Glob == "" && len(t.Verify) == 0 {
		return nil
	}
	if _, ok := knownReaders[t.Reader]; !ok {
		return fmt.Errorf("%s: manifest %q transcript reader %q is not one this build has",
			file, id, t.Reader)
	}
	if t.Dir == "" || t.Glob == "" {
		return fmt.Errorf("%s: manifest %q transcript needs both dir and glob", file, id)
	}
	if _, err := placeholders(t.Dir); err != nil {
		return fmt.Errorf("%s: manifest %q transcript dir: %w", file, id, err)
	}
	for _, f := range t.Verify {
		if _, ok := verifiableFields[f]; !ok {
			return fmt.Errorf("%s: manifest %q transcript verify names unknown field %q",
				file, id, f)
		}
	}
	if _, err := filepath.Match(t.Glob, "probe"); err != nil {
		return fmt.Errorf("%s: manifest %q transcript glob %q: %w", file, id, t.Glob, err)
	}
	return nil
}

// ExpandDir resolves the placeholders in Dir against a pane's working directory,
// returning "" when it cannot (an unknown home, or a pane whose directory is not
// known yet). An empty result means no search, which means no claim.
//
// Two placeholders exist and no more:
//
//	{home}        the user's home directory
//	{cwd:dashes}  the pane's working directory with every "/" turned into "-"
//
// {cwd:dashes} is a named mangling implemented here rather than a rule a
// manifest can write, because it is a fact about another program's on-disk
// layout and belongs in reviewed code. It was checked against the 39 project
// directories on the author's machine: every one is the absolute path with "/"
// replaced by "-", including a nested case that proves the replacement is
// literal and not a normalisation ("/tmp/claude-1000/-home-gaurav-.../work"
// becomes "-tmp-claude-1000--home-gaurav-...-work", keeping the doubled dash).
func (t Transcript) ExpandDir(cwd string) string {
	if !t.Enabled() || cwd == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	out := strings.ReplaceAll(t.Dir, "{home}", home)
	out = strings.ReplaceAll(out, "{cwd:dashes}", DashPath(cwd))
	return filepath.Clean(out)
}

// DashPath is the {cwd:dashes} mangling.
func DashPath(cwd string) string { return strings.ReplaceAll(cwd, "/", "-") }

// knownPlaceholders is what ExpandDir understands.
var knownPlaceholders = map[string]struct{}{"{home}": {}, "{cwd:dashes}": {}}

// placeholders returns the placeholders a template uses, erroring on one this
// build does not know. Left unchecked, a typo would leave the literal braces in
// a path, the directory would never exist, and the source would go quiet with
// nothing to say why.
func placeholders(tmpl string) ([]string, error) {
	var out []string
	rest := tmpl
	for {
		open := strings.Index(rest, "{")
		if open < 0 {
			return out, nil
		}
		close := strings.Index(rest[open:], "}")
		if close < 0 {
			return nil, fmt.Errorf("unclosed placeholder in %q", tmpl)
		}
		ph := rest[open : open+close+1]
		if _, ok := knownPlaceholders[ph]; !ok {
			return nil, fmt.Errorf("unknown placeholder %s", ph)
		}
		out = append(out, ph)
		rest = rest[open+close+1:]
	}
}

// TranscriptFor returns the transcript block for a harness id, or nil when it
// has none. nil is the answer for almost every harness and means the daemon
// behaves exactly as it did before this existed.
func (r *Registry) TranscriptFor(id string) *Transcript {
	m := r.Lookup(id)
	if m == nil || !m.Transcript.Enabled() {
		return nil
	}
	return &m.Transcript
}
