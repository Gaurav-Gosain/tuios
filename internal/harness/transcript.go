package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Transcript says where a harness keeps its own record of a session, so the
// daemon can read what the agent said it did rather than infer it from the
// screen. A manifest with no [transcript] block gets no transcript reading,
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
	// Glob matches the session files inside Dir. It may span folders
	// ("*/chats/session-*.jsonl"), and then Dir names no placeholder but
	// {home}, since a path a hook reports is matched against it from Dir.
	// A glob that does not span folders matches only files directly in Dir.
	Glob string `toml:"glob"`
	// Exclude lists file name patterns Glob matches that are not sessions:
	// a harness's ledgers, backups and temporary files.
	Exclude []string `toml:"exclude"`
	// SessionFile names the file of a session from the session id the
	// harness's hook reports, under Dir: "{session_id}.json". It is for a
	// harness whose hook names its session but not its file. Empty means the
	// session id finds no file.
	SessionFile string `toml:"session_file"`
	// BySession says the file is the harness's whole store, Dir/Glob with no
	// wildcard, and the session id its hook or plugin reports names the
	// session in it. opencode's service.json is one: the reader asks the
	// service the file names for the session.
	BySession bool `toml:"by_session"`
	// Verify names the record fields that must match the pane before a file
	// found by search is believed. It is what stops a wrong join, which is worse
	// than no join: attributing one agent's state to another pane is a confident
	// lie, where no state is merely no state.
	Verify []string `toml:"verify"`
}

// ReaderJSONL is Claude Code's format: one JSON object per line, newest record
// last. The constant is the name a manifest spells, so adding a harness with
// the same layout is a manifest and no code. It is the one reader the daemon
// reads a turn state from (internal/transcript). The others are read only to
// show the conversation to the person (internal/transcriptview).
const ReaderJSONL = "claude-code-jsonl"

// knownReaders is the set a manifest may name. Each is a format of
// internal/transcriptview, which this package does not import, so the names
// are kept in step by the session package's tests.
var knownReaders = map[string]struct{}{
	ReaderJSONL:        {},
	"codex-rollout":    {},
	"gemini-chat":      {},
	"qwen-chat":        {},
	"copilot-events":   {},
	"amp-thread":       {},
	"opencode-service": {},
	"cursor-jsonl":     {},
}

// KnownReaders returns the reader names a manifest may give.
func KnownReaders() []string {
	out := make([]string, 0, len(knownReaders))
	for name := range knownReaders {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

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
	return slices.Contains(t.Verify, field)
}

// checkTranscript validates the block, treating a half-written one as an error.
// A transcript block that silently does nothing is the failure this registry
// exists to avoid, and it matters more here than elsewhere: the user cannot see
// the source not working, they can only see a pane whose state never updates.
func checkTranscript(file, id string, t *Transcript) error {
	t.Reader = strings.TrimSpace(t.Reader)
	t.Dir = strings.TrimSpace(t.Dir)
	t.Glob = strings.TrimSpace(t.Glob)
	t.SessionFile = strings.TrimSpace(t.SessionFile)
	if t.Reader == "" && t.Dir == "" && t.Glob == "" && len(t.Verify) == 0 && t.SessionFile == "" {
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
	for _, ex := range t.Exclude {
		if _, err := filepath.Match(ex, "probe"); err != nil || strings.ContainsRune(ex, '/') {
			return fmt.Errorf("%s: manifest %q transcript exclude %q must be a file name pattern", file, id, ex)
		}
	}
	if strings.ContainsRune(t.Glob, '/') && strings.Contains(baseless(t.Dir), "{") {
		return fmt.Errorf("%s: manifest %q transcript glob %q spans folders, so dir may name no placeholder but {home}", file, id, t.Glob)
	}
	if t.SessionFile != "" {
		if strings.Count(t.SessionFile, "{session_id}") != 1 || strings.ContainsAny(strings.ReplaceAll(t.SessionFile, "{session_id}", ""), `/\{}`) {
			return fmt.Errorf("%s: manifest %q transcript session_file %q must be a file name with {session_id} once", file, id, t.SessionFile)
		}
		if strings.Contains(baseless(t.Dir), "{") {
			return fmt.Errorf("%s: manifest %q transcript session_file needs a dir with no placeholder but {home}", file, id)
		}
	}
	return nil
}

// SessionPath is the file of the session a hook names by its id, under
// Root, or "" when the manifest has no session_file or the id could name
// anything but one file in that folder. The id is the harness's own, so it
// is held to the characters session ids are made of.
func (t Transcript) SessionPath(sessionID string) string {
	root := t.Root()
	if t.SessionFile == "" || root == "" || sessionID == "" || len(sessionID) > 128 {
		return ""
	}
	for _, r := range sessionID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return ""
		}
	}
	if strings.Trim(sessionID, ".") == "" {
		return ""
	}
	return filepath.Join(root, strings.ReplaceAll(t.SessionFile, "{session_id}", sessionID))
}

// MatchesGlob reports whether path, under root (Root, resolved), is a file
// Glob names and Exclude does not. A glob that spans folders is matched
// against the path from root. Any other is matched against the file name,
// and the file must sit directly in Dir: a file in a folder below it is not a
// session file, whatever its name.
func (t Transcript) MatchesGlob(root, path string) bool {
	base := filepath.Base(path)
	for _, ex := range t.Exclude {
		if ok, _ := filepath.Match(ex, base); ok {
			return false
		}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	if strings.ContainsRune(t.Glob, '/') {
		ok, _ := filepath.Match(t.Glob, rel)
		return ok
	}
	if strings.Count(rel, "/") != t.dirDepth() {
		return false
	}
	ok, _ := filepath.Match(t.Glob, base)
	return ok
}

// dirDepth is how many folders Dir is below Root: 0 for a Dir with no
// placeholder but {home}, 1 for Claude Code's {home}/.claude/projects/
// {cwd:dashes}, whose placeholder is one folder.
func (t Transcript) dirDepth() int {
	dir := filepath.ToSlash(t.Dir)
	i := strings.IndexByte(baseless(dir), '{')
	if i < 0 {
		return 0
	}
	dir = baseless(dir)
	rest := dir[strings.LastIndexByte(dir[:i], '/')+1:]
	return strings.Count(strings.Trim(rest, "/"), "/") + 1
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
	out := expandBase(t.Dir, home)
	out = strings.ReplaceAll(out, "{cwd:dashes}", DashPath(cwd))
	return filepath.Clean(out)
}

// Root is the folder every transcript of the harness is under, whatever the
// pane's working directory: Dir up to the last whole path element before the
// first placeholder other than {home}, with {home} resolved. For Claude Code
// it is ~/.claude/projects. It is "" when Dir has no such folder or the home
// is unknown, and then no path a hook reports can be accepted.
func (t Transcript) Root() string {
	if !t.Enabled() {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	dir := expandBase(t.Dir, home)
	if i := strings.IndexByte(dir, '{'); i >= 0 {
		dir = dir[:i]
		j := strings.LastIndexAny(dir, `/\`)
		if j < 0 {
			return ""
		}
		dir = dir[:j+1]
	}
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) || dir == filepath.Dir(dir) {
		// Relative, or the root of the file system: no folder to hold a
		// path to.
		return ""
	}
	return dir
}

// DashPath is the {cwd:dashes} mangling.
func DashPath(cwd string) string { return strings.ReplaceAll(cwd, "/", "-") }

// knownPlaceholders is what ExpandDir understands.
var knownPlaceholders = map[string]struct{}{"{home}": {}, "{state}": {}, "{cwd:dashes}": {}}

// expandBase resolves the two placeholders that name a fixed folder: {home},
// and {state}, the XDG state folder ($XDG_STATE_HOME, or ~/.local/state).
func expandBase(dir, home string) string {
	if strings.Contains(dir, "{state}") {
		state := os.Getenv("XDG_STATE_HOME")
		if !filepath.IsAbs(state) {
			state = filepath.Join(home, ".local", "state")
		}
		dir = strings.ReplaceAll(dir, "{state}", state)
	}
	return strings.ReplaceAll(dir, "{home}", home)
}

// baseless is dir without its fixed-folder placeholders, for the checks of
// which placeholders a dir names.
func baseless(dir string) string {
	return strings.ReplaceAll(strings.ReplaceAll(dir, "{home}", ""), "{state}", "")
}

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
// reads no transcript for it.
func (r *Registry) TranscriptFor(id string) *Transcript {
	m := r.Lookup(id)
	if m == nil || !m.Transcript.Enabled() {
		return nil
	}
	return &m.Transcript
}

// StorePath is the one file of a by_session harness, "" for any other or
// when the home is unknown.
func (t Transcript) StorePath() string {
	root := t.Root()
	if !t.BySession || root == "" || strings.ContainsAny(t.Glob, "*?[/") {
		return ""
	}
	return filepath.Join(root, t.Glob)
}
