package harness

import (
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
	// harness's hook reports, under Dir: "{session_id}.json", or a fixed path
	// below a folder per session, "{session_id}/logs/transcript.jsonl". It is
	// for a harness whose hook names its session but not its file. The id is
	// a folder name of its own or part of the file name, the rest is literal,
	// and the path must have the shape Glob matches (checkSessionFile). Empty
	// means the session id finds no file.
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
// are kept in step by hand.
var knownReaders = map[string]struct{}{
	ReaderJSONL:        {},
	"codex-rollout":    {},
	"gemini-chat":      {},
	"qwen-chat":        {},
	"copilot-events":   {},
	"amp-thread":       {},
	"opencode-service": {},
	"cursor-jsonl":     {},
	"pi-jsonl":         {},
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
		return errorf("%s: manifest %q transcript reader %q is not one this build has",
			file, id, t.Reader)
	}
	if t.Dir == "" || t.Glob == "" {
		return errorf("%s: manifest %q transcript needs both dir and glob", file, id)
	}
	if _, err := placeholders(t.Dir); err != nil {
		return errorf("%s: manifest %q transcript dir: %w", file, id, err)
	}
	for _, f := range t.Verify {
		if _, ok := verifiableFields[f]; !ok {
			return errorf("%s: manifest %q transcript verify names unknown field %q",
				file, id, f)
		}
	}
	if _, err := filepath.Match(t.Glob, "probe"); err != nil {
		return errorf("%s: manifest %q transcript glob %q: %w", file, id, t.Glob, err)
	}
	for _, ex := range t.Exclude {
		if _, err := filepath.Match(ex, "probe"); err != nil || strings.ContainsRune(ex, '/') {
			return errorf("%s: manifest %q transcript exclude %q must be a file name pattern", file, id, ex)
		}
	}
	if strings.ContainsRune(t.Glob, '/') && strings.Contains(baseless(t.Dir), "{") {
		return errorf("%s: manifest %q transcript glob %q spans folders, so dir may name no placeholder but {home}", file, id, t.Glob)
	}
	if t.SessionFile != "" {
		if err := checkSessionFile(t.SessionFile, t.Glob, t.Exclude); err != nil {
			return errorf("%s: manifest %q transcript session_file %q %w", file, id, t.SessionFile, err)
		}
		if strings.Contains(baseless(t.Dir), "{") {
			return errorf("%s: manifest %q transcript session_file needs a dir with no placeholder but {home}", file, id)
		}
	}
	return nil
}

// checkSessionFile holds a session_file to a fixed path under Dir that only
// the session id varies, so a hook can name no file but one of the
// harness's: {session_id} once and no other placeholder, no "\" and no "..",
// and "/" only between path elements that are not empty or ".". The id may
// share the file name with literal text, as Amp's "{session_id}.json" does,
// and is a folder name of its own anywhere else.
//
// It also refuses a path whose shape glob cannot match, which would join
// nothing and say nothing: a file in a folder when glob does not span
// folders, another number of folders than glob has, a literal name glob
// does not match at its place, or a literal file name exclude leaves out.
// The part holding the id is left to the join, which matches the whole path
// against glob, since no stand-in id could be checked here without refusing
// a glob that fixes how ids look. A glob with "/" in a character class is
// refused: filepath.Match lets such a class match the separator, so the
// glob's path elements are not the parts between its "/"s.
func checkSessionFile(sessionFile, glob string, exclude []string) error {
	if strings.Count(sessionFile, "{session_id}") != 1 || strings.ContainsAny(strings.ReplaceAll(sessionFile, "{session_id}", ""), "{}") {
		return errorf("must name {session_id} once and no other placeholder")
	}
	if strings.ContainsRune(sessionFile, '\\') || strings.Contains(sessionFile, "..") {
		return errorf(`may hold no "\" and no ".."`)
	}
	parts := strings.Split(sessionFile, "/")
	for i, part := range parts {
		if part == "" || part == "." {
			return errorf(`has an empty or "." path element`)
		}
		if strings.Contains(part, "{session_id}") && part != "{session_id}" && i != len(parts)-1 {
			return errorf("must have {session_id} as a whole folder name, or in the file name")
		}
	}
	if classHoldsSlash(glob) {
		return errorf(`needs a glob with no "/" in a character class, and glob %q has one`, glob)
	}
	globParts := []string{glob}
	if strings.ContainsRune(glob, '/') {
		globParts = strings.Split(glob, "/")
	}
	if len(parts) != len(globParts) {
		return errorf("has %d path elements and glob %q has %d", len(parts), glob, len(globParts))
	}
	for i, part := range parts {
		if strings.Contains(part, "{session_id}") {
			continue
		}
		if ok, _ := filepath.Match(globParts[i], part); !ok {
			return errorf("names %q where glob %q has %q", part, glob, globParts[i])
		}
	}
	if name := parts[len(parts)-1]; !strings.Contains(name, "{session_id}") {
		for _, ex := range exclude {
			if ok, _ := filepath.Match(ex, name); ok {
				return errorf("names %q, which exclude %q leaves out", name, ex)
			}
		}
	}
	return nil
}

// classHoldsSlash reports a character class in glob with "/" in it, such as
// "[^/]". A "\" escapes the next character, as filepath.Match reads it
// outside Windows, and an escaped "/" in a class counts too.
func classHoldsSlash(glob string) bool {
	in := false
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		if c == '\\' && i+1 < len(glob) {
			i++
			if in && glob[i] == '/' {
				return true
			}
			continue
		}
		switch {
		case !in:
			in = c == '['
		case c == '/':
			return true
		case c == ']':
			in = false
		}
	}
	return false
}

// SessionPath is the file of the session a hook names by its id, under
// Root, or "" when the manifest has no session_file or the id could name
// anything but the one file session_file gives it. The id is the harness's
// own, so it is held to the characters session ids are made of, with no
// "/" or "\" and no "..", so a hook cannot point the daemon outside Dir. An
// id that ends in "." names no file either: Windows drops a trailing dot
// from a path element, so "X." would name X's file, another conversation's.
func (t Transcript) SessionPath(sessionID string) string {
	root := t.Root()
	if t.SessionFile == "" || root == "" || sessionID == "" || len(sessionID) > 128 || strings.Contains(sessionID, "..") || strings.HasSuffix(sessionID, ".") {
		return ""
	}
	for _, r := range sessionID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return ""
		}
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
	out := expandBase(t.Dir, home, os.Getenv)
	out = strings.ReplaceAll(out, "{cwd:dashes}", DashPath(cwd))
	return filepath.Clean(out)
}

// Root is the folder every transcript of the harness is under, whatever the
// pane's working directory: Dir up to the last whole path element before the
// first placeholder other than a fixed folder's, with those resolved. For
// Claude Code it is ~/.claude/projects. It is "" when Dir has no such folder
// or the home is unknown, and then no path a hook reports can be accepted. The
// variables that move a harness's own folder are read from the daemon's
// environment.
func (t Transcript) Root() string { return t.RootIn(os.Getenv) }

// RootIn is Root with the variables that move a harness's own folder
// (agentHomes) read through getenv, so a caller can check a path against the
// folder a given process's harness writes to.
func (t Transcript) RootIn(getenv func(string) string) string {
	if !t.Enabled() {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	dir := expandBase(t.Dir, home, getenv)
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
var knownPlaceholders = map[string]struct{}{"{home}": {}, "{state}": {}, "{pi_agent}": {}, "{claude_config}": {}, "{codex_home}": {}, "{cwd:dashes}": {}}

// agentHomes are the placeholders for a harness's own folder, each moved by
// the variable the harness itself reads, else the folder under the home.
var agentHomes = []struct {
	placeholder, env string
	under            []string
}{
	{"{pi_agent}", "PI_CODING_AGENT_DIR", []string{".pi", "agent"}},
	{"{claude_config}", "CLAUDE_CONFIG_DIR", []string{".claude"}},
	{"{codex_home}", "CODEX_HOME", []string{".codex"}},
}

// expandBase resolves the placeholders that name a fixed folder: {home};
// {state}, the XDG state folder ($XDG_STATE_HOME, or ~/.local/state); and
// the agentHomes, read through getenv (see agentHomeDir).
func expandBase(dir, home string, getenv func(string) string) string {
	if strings.Contains(dir, "{state}") {
		state := os.Getenv("XDG_STATE_HOME")
		if !filepath.IsAbs(state) {
			state = filepath.Join(home, ".local", "state")
		}
		dir = strings.ReplaceAll(dir, "{state}", state)
	}
	for _, a := range agentHomes {
		if strings.Contains(dir, a.placeholder) {
			dir = strings.ReplaceAll(dir, a.placeholder, agentHomeDir(home, getenv(a.env), a.under))
		}
	}
	return strings.ReplaceAll(dir, "{home}", home)
}

// agentHomeDir is a harness's own folder, the way the harness finds it: the
// variable's value v with a leading ~ expanded, else home joined with under.
// A relative value is returned as it is, and Root then gives "" for it, since
// no path can be checked against a folder that depends on where a process
// runs. A value with a { in it is refused the same way: Root cuts the path at
// the first {, which in the value would move the folder up.
func agentHomeDir(home, v string, under []string) string {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return filepath.Join(append([]string{home}, under...)...)
	case strings.ContainsRune(v, '{'):
		return "."
	case v == "~" || strings.HasPrefix(v, "~/"):
		return filepath.Join(home, strings.TrimPrefix(v, "~"))
	}
	return v
}

// baseless is dir without its fixed-folder placeholders, for the checks of
// which placeholders a dir names.
func baseless(dir string) string {
	dir = strings.ReplaceAll(strings.ReplaceAll(dir, "{home}", ""), "{state}", "")
	for _, a := range agentHomes {
		dir = strings.ReplaceAll(dir, a.placeholder, "")
	}
	return dir
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
			return nil, errorf("unclosed placeholder in %q", tmpl)
		}
		ph := rest[open : open+close+1]
		if _, ok := knownPlaceholders[ph]; !ok {
			return nil, errorf("unknown placeholder %s", ph)
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
