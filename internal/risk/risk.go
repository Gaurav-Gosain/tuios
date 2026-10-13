// Package risk marks a tool call an agent asks to have approved as risky: a
// command that deletes recursively, force pushes, pipes a download to a shell,
// writes outside the worktree, and the like.
//
// It is a speed bump, not a sandbox. A rule reads the command as a person
// would type it, so a command built to hide what it does (a variable holding
// "rm", an alias, a script file, base64 piped to a shell that no rule sees as
// a download) passes. The harness's own permission system stays the boundary;
// what this package adds is that the Inbox asks for a second, deliberate
// press before it allows a call a rule matched.
//
// Match is pure: it reads the call and the rules and nothing else, so the
// daemon can run it under its own locks.
package risk

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Call is what the rules read.
type Call struct {
	// Tool is the tool name the harness gave the call (Bash, Write, shell).
	// Empty when it is not known, which the shell rules read as a command.
	Tool string
	// Text is what the tool acts on: the command line for a shell tool, the
	// path for a file tool.
	Text string
	// Root is the folder the pane's work is held to. It must be a folder
	// the pane cannot move (see approvalRoot in internal/session), never its
	// working folder. Empty, or a file system root, means the pane has no
	// such folder: every absolute path and every path under ~ is then read
	// as outside, so the rule fails closed.
	Root string
	// RunDir is the folder the call runs in: the cwd the harness sent with
	// the approval, else the pane's live folder. A relative path is resolved
	// against it. Empty means it is not known, and then every relative path
	// is read as outside.
	RunDir string
	// Resolve resolves a path's links (realPath), so that a path written
	// through a link compares with the resolved root. Root must be resolved
	// already. Nil compares paths as written.
	Resolve func(string) string
	// Home is the home directory a leading ~ stands for.
	Home string
	// Shown says Text is the one-line summary a pane reported for the call,
	// not the call itself. A key-like run in that line is replaced with
	// "***", so a path rule reads a path in it only up to the stars (see
	// outside).
	Shown bool
	// Clipped says the line was clipped to length, so Text ends in the "..."
	// the clip added. A path rule reads the last word of Text only up to that
	// "...". A "..." anywhere else, or on a line that was not clipped, is
	// part of what the call names, and is read as written.
	Clipped bool
}

// Marks a shown line carries where it does not show the call.
const (
	// ShownClip ends a line that was clipped to length.
	ShownClip = "..."
	// ShownRedacted stands in a line for a run that looked like a secret.
	ShownRedacted = "***"
)

// clipMark stands in Text for the "..." a clip ended it with, while the path
// rules read it, so that the cut is known by where it is and a "..." written
// in the call is not read as one. U+FFFF is a noncharacter: no reported line
// carries it, and a Text that does is not marked (see markClip).
const clipMark = "\uffff"

// markClip replaces the "..." a clipped text ends with by clipMark. ok is
// false when text does not end in "..." or already holds clipMark.
func markClip(text string) (marked string, ok bool) {
	if strings.Contains(text, clipMark) {
		return text, false
	}
	s, ok := strings.CutSuffix(strings.TrimRight(text, " \t"), ShownClip)
	if !ok {
		return text, false
	}
	return s + clipMark, true
}

// Hit is one rule a call matched.
type Hit struct {
	// Rule is the rule's name.
	Rule string `json:"rule"`
	// Why says in a few words what the rule guards against.
	Why string `json:"why"`
}

// Rule is one risk rule.
type Rule struct {
	// Name is what the Inbox calls the rule.
	Name string
	// Why says in a few words what the rule guards against.
	Why string
	// Tools limits the rule to these tool names, compared without case.
	// Empty applies it to every tool. Naming any one of ShellTools covers
	// them all and a call with no tool; naming any one of FileTools covers
	// them all.
	Tools []string
	// command reads one command of a shell call. Nil for a rule that does
	// not read commands.
	command func(c command, call Call) bool
	// path reads the target of a file tool. Nil for a rule that does not.
	path func(target string, call Call) bool
	// pattern is a person's RE2 rule, matched against each command segment
	// of a shell call and against the whole text of any other.
	pattern *regexp.Regexp
}

// Custom makes a rule from the person's config: a name, the tools it applies
// to, and an RE2 pattern matched against each command of a shell call and the
// whole text of any other call.
func Custom(name string, tools []string, pattern string) (Rule, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Rule{}, err
	}
	return Rule{Name: name, Why: "matches the rule " + name + " in your config", Tools: slices.Clone(tools), pattern: re}, nil
}

// ShellTools are the tool names whose Text is a command line. A call with no
// tool name is read as one too.
var ShellTools = []string{"Bash", "bash", "shell", "exec_command", "local_shell", "run_shell_command", "execute", "terminal"}

// FileTools are the tool names whose Text is the path they write.
var FileTools = []string{"Write", "Edit", "MultiEdit", "NotebookEdit", "write", "edit", "multiedit", "patch", "write_file", "replace"}

// isShell reports whether a call's text is a command line.
func isShell(tool string) bool {
	return tool == "" || containsFold(ShellTools, tool)
}

// isFile reports whether a call's text is a path it writes.
func isFile(tool string) bool {
	return containsFold(FileTools, tool)
}

func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, s) })
}

// appliesTo reports whether a rule reads calls of this tool. A rule that
// names one shell tool reads every call whose text is a command line, since
// harnesses name the same tool differently (Bash, shell, and execute on a
// protocol pane's line) and a line with no tool is read as a command. Naming
// one file tool likewise covers every file tool.
func (r Rule) appliesTo(tool string) bool {
	switch {
	case len(r.Tools) == 0 || containsFold(r.Tools, tool):
		return true
	case isShell(tool):
		return slices.ContainsFunc(r.Tools, func(t string) bool { return containsFold(ShellTools, t) })
	case isFile(tool):
		return slices.ContainsFunc(r.Tools, isFile)
	}
	return false
}

// maxDepth bounds how deep Match follows a command inside a command: a
// command substitution, a process substitution, sh -c or eval.
const maxDepth = 4

// maxText bounds the text Match reads. A longer command is read up to it.
const maxText = 64 << 10

// Match returns the rules the call matched, each once, in the order of rules.
func Match(rules []Rule, call Call) []Hit {
	text := call.Text
	if len(text) > maxText {
		text = text[:maxText]
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var cmds []command
	if isShell(call.Tool) {
		cmds = parseCommands(text, 0)
	}
	// The path and command rules read a clipped text with its clip marked,
	// so that only the word the clip cut is read as cut. The patterns read
	// the text as the line shows it.
	in := input{text: text, cmds: cmds, pathText: text, pathCmds: cmds}
	if call.Clipped {
		if marked, ok := markClip(text); ok {
			in.pathText = marked
			if isShell(call.Tool) {
				in.pathCmds = parseCommands(marked, 0)
			}
		}
	}
	var hits []Hit
	for _, r := range rules {
		if !r.appliesTo(call.Tool) || !r.matches(call, in) {
			continue
		}
		if !slices.ContainsFunc(hits, func(h Hit) bool { return h.Rule == r.Name }) {
			hits = append(hits, Hit{Rule: r.Name, Why: r.Why})
		}
	}
	return hits
}

// Names is the rule names of hits, in order.
func Names(hits []Hit) []string {
	if len(hits) == 0 {
		return nil
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Rule
	}
	return out
}

// input is a call's text as each kind of rule reads it.
type input struct {
	// text and cmds are what the patterns read.
	text string
	cmds []command
	// pathText and pathCmds are what the path and command rules read: the
	// same, with a clip marked (see markClip).
	pathText string
	pathCmds []command
}

// matches runs one rule on a call.
func (r Rule) matches(call Call, in input) bool {
	switch {
	case r.pattern != nil:
		if len(in.cmds) == 0 {
			return r.pattern.MatchString(in.text)
		}
		for _, c := range in.cmds {
			if r.pattern.MatchString(c.raw) {
				return true
			}
		}
		return false
	case r.command != nil && isShell(call.Tool):
		// A cd earlier in the line moves where the later commands run, so
		// each command is read from the folder the ones before it left in
		// its own shell. dirs holds that folder by scope (command.scope): a
		// cd in a subshell, an sh -c line or a substitution moves its own
		// scope and the ones inside it, never an outer one.
		dirs := map[string]string{"": call.RunDir}
		for _, c := range in.pathCmds {
			cur := call
			cur.RunDir = dirIn(dirs, c.scope)
			if c.chdir {
				cur.RunDir = ""
			}
			if r.command(c, cur) {
				return true
			}
			switch {
			case c.chdir || c.cdpath:
				// The folder cannot be followed from here on.
				dirs[c.scope] = ""
			case c.inPipe:
				// A pipeline stage is a subshell: its cd ends with it.
			default:
				dirs[c.scope] = runDirAfter(c, cur)
			}
		}
	case r.path != nil && isFile(call.Tool):
		return r.path(strings.TrimSpace(in.pathText), call)
	}
	return false
}

// ParseSummary splits the line tuios's own hooks report for an approval,
// "approve <Tool>: <what>", into the tool and what it acts on. A line of any
// other form is returned whole with no tool, which the shell rules read as a
// command.
func ParseSummary(line string) (tool, text string) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "approve ")
	if !ok {
		return "", line
	}
	name, arg, ok := strings.Cut(rest, ": ")
	if !ok || name == "" || strings.ContainsAny(name, " \t") {
		return "", line
	}
	return name, arg
}

// outside reports whether path, as written in a command, names a place
// outside root: an absolute path, or one under ~, that is not root or under
// it. A relative path is resolved against call.RunDir, the folder the call
// runs in, and is outside when that folder is unknown or itself outside root.
// The null device and the terminal are never outside.
func outside(path string, call Call) bool {
	if path == "" {
		return false
	}
	path = strings.Trim(path, `"'`)
	// A path that names a variable, a substitution or another user's home
	// is not known until the shell expands it, so it is outside. $HOME and
	// ${HOME} at the start are the one exception: Home says what they are.
	path, known := expandHome(path, call.Home)
	if !known {
		return true
	}
	root := fixedRoot(call.Root)
	if !strings.HasPrefix(path, "/") {
		// Relative to where the call runs. The pane moves that folder, so
		// a run folder outside the root, or one nobody knows, makes every
		// relative path outside.
		run := ""
		if call.RunDir != "" && filepath.IsAbs(call.RunDir) {
			run = call.resolve(filepath.Clean(call.RunDir))
		}
		if root == "" || run == "" || !under(run, root) {
			return true
		}
		// Joined as text, so that the "***" and the clip in a shown path
		// are still there for shownPart to find.
		path = run + string(filepath.Separator) + path
	}
	if shown, cut := shownPart(path, call); cut {
		return shownOutside(shown, root)
	}
	path = filepath.Clean(path)
	if slices.Contains(harmlessDevices, path) || strings.HasPrefix(path, "/dev/fd/") {
		return false
	}
	if root == "" {
		// No fixed folder: nothing absolute is inside.
		return true
	}
	return !under(call.resolve(path), root)
}

// under reports whether path is dir or inside it. Both are clean.
func under(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// resolve is call.Resolve, or the path as it is when there is none.
func (call Call) resolve(path string) string {
	if call.Resolve == nil {
		return path
	}
	return call.Resolve(path)
}

// expandHome expands a leading ~, $HOME or ${HOME} with home. known is false
// for a path the shell would expand some other way: a ~user, a variable or a
// substitution anywhere, or a home that is not known.
func expandHome(path, home string) (string, bool) {
	rest := ""
	switch {
	case path == "~" || strings.HasPrefix(path, "~/"):
		rest = strings.TrimPrefix(path, "~")
	case strings.HasPrefix(path, "~"):
		return "", false
	case path == "$HOME" || strings.HasPrefix(path, "$HOME/"):
		rest = strings.TrimPrefix(path, "$HOME")
	case path == "${HOME}" || strings.HasPrefix(path, "${HOME}/"):
		rest = strings.TrimPrefix(path, "${HOME}")
	default:
		if strings.ContainsAny(path, "$`") {
			return "", false
		}
		return path, true
	}
	if home == "" || strings.ContainsAny(rest, "$`") {
		return "", false
	}
	// Joined as text, for shownPart, like a relative path.
	return strings.TrimSuffix(home, "/") + rest, true
}

// fixedRoot is call.Root cleaned, or "" when there is none. A file system
// root is no root at all: it would hold every path, so it is read as none and
// the rule fails closed.
func fixedRoot(root string) string {
	if root == "" {
		return ""
	}
	root = filepath.Clean(root)
	if filepath.Dir(root) == root {
		return ""
	}
	return root
}

// shownPart is the part of a path from a shown line that the line shows as
// it is: up to the first "***", or up to the clip (clipMark, which only the
// last word of a clipped line carries). cut reports whether the line hides
// the rest. A "..." written in the path is not a clip and is read as it is.
func shownPart(path string, call Call) (shown string, cut bool) {
	if call.Shown {
		if before, _, ok := strings.Cut(path, ShownRedacted); ok {
			return before, true
		}
	}
	if before, _, ok := strings.Cut(path, clipMark); ok {
		return before, true
	}
	return path, false
}

// shownOutside reports whether a path the line shows only the start of is
// outside root whatever the hidden rest is. The line was cut, so the call is
// marked cut short anyway; this rule only marks what the line itself shows
// leaving the worktree.
//
// The start is read as finished directories and an unfinished last name.
// The path is outside for sure only when the finished directories are
// neither root, a directory under it, nor a directory above it on the way
// to it, or when they are above it and the unfinished name cannot become the
// next name on the way. An unfinished name that may still become ".." can
// climb anywhere, so it is never outside for sure.
//
// With no root ("") every absolute start is outside.
func shownOutside(shown, root string) bool {
	if !strings.HasPrefix(shown, "/") {
		return false
	}
	if root == "" {
		return true
	}
	i := strings.LastIndex(shown, "/")
	dir, name := filepath.Clean(shown[:i+1]), shown[i+1:]
	if name == "" || name == "." || name == ".." {
		return false
	}
	sep := string(filepath.Separator)
	switch {
	case dir == root || strings.HasPrefix(dir, root+sep):
		return false
	case dir == "/" || strings.HasPrefix(root, dir+sep):
		next, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(root, dir), sep), sep)
		return !strings.HasPrefix(next, name)
	}
	return true
}

// harmlessDevices are paths a command writes to without changing anything.
var harmlessDevices = []string{"/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty"}

// FromConfig is the rule set [agents.approvals.risk] describes: the shipped
// rules unless builtin is false, then the person's own. A rule with no name or
// a pattern RE2 cannot compile is left out; config validation warns about it.
func FromConfig(cfg config.RiskConfig) []Rule {
	var rules []Rule
	if cfg.BuiltinRules() {
		rules = Builtin()
	}
	for _, rc := range cfg.Rules {
		name := strings.TrimSpace(rc.Name)
		if name == "" || strings.TrimSpace(rc.Pattern) == "" {
			continue
		}
		r, err := Custom(name, rc.Tools, rc.Pattern)
		if err != nil {
			continue
		}
		rules = append(rules, r)
	}
	return rules
}

// dirIn is the folder a command in scope runs in: the folder its own scope
// or the nearest outer one was left in.
func dirIn(dirs map[string]string, scope string) string {
	for {
		if d, ok := dirs[scope]; ok {
			return d
		}
		if scope == "" {
			return ""
		}
		scope = scope[:strings.LastIndex(scope, "/")]
	}
}

// runDirAfter is the folder the commands after c run in: c's target when c is
// a cd or pushd, else the folder c ran in. "" when it cannot be known: a cd -,
// a popd, a target with a variable, a substitution or a ~user in it, or a
// relative one from an unknown folder. An unknown folder makes every relative
// path after it outside.
func runDirAfter(c command, call Call) string {
	switch c.name() {
	case "cd", "pushd":
	case "popd":
		return ""
	default:
		return call.RunDir
	}
	ops := operands(c.args())
	if len(ops) == 0 {
		return call.Home
	}
	dir, known := expandHome(strings.Trim(ops[0], `"'`), call.Home)
	switch {
	case !known || dir == "-" || strings.ContainsAny(dir, "*?["):
		return ""
	case filepath.IsAbs(dir):
		return filepath.Clean(dir)
	case call.RunDir == "":
		return ""
	}
	return filepath.Join(call.RunDir, dir)
}
