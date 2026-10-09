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
	// Root is the pane's worktree root, else its working directory. Empty
	// turns the outside-the-worktree rule off.
	Root string
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
		for _, c := range in.pathCmds {
			if r.command(c, call) {
				return true
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
// it. A relative path is read as inside, since it is relative to where the
// command runs. The null device and the terminal are never outside.
func outside(path string, call Call) bool {
	if call.Root == "" || path == "" {
		return false
	}
	path = strings.Trim(path, `"'`)
	switch {
	case path == "~" || strings.HasPrefix(path, "~/"):
		if call.Home == "" {
			return true
		}
		path = filepath.Join(call.Home, strings.TrimPrefix(path, "~"))
	case strings.HasPrefix(path, "$HOME"):
		if call.Home == "" {
			return true
		}
		path = filepath.Join(call.Home, strings.TrimPrefix(path, "$HOME"))
	case !strings.HasPrefix(path, "/"):
		return false
	}
	root := filepath.Clean(call.Root)
	if shown, cut := shownPart(path, call); cut {
		return shownOutside(shown, root)
	}
	path = filepath.Clean(path)
	if slices.Contains(harmlessDevices, path) || strings.HasPrefix(path, "/dev/fd/") {
		return false
	}
	if path == root {
		return false
	}
	return !strings.HasPrefix(path, root+string(filepath.Separator)) && root != "/"
}

// shownPart is the part of a path from a shown line that the line shows as
// it is: up to the first "***", or up to the clip (clipMark, which only the
// last word of a clipped line carries). cut reports whether the line hides
// the rest. A "..." written in the path is not a clip and is read as it is.
func shownPart(path string, call Call) (shown string, cut bool) {
	if call.Shown {
		if i := strings.Index(path, ShownRedacted); i >= 0 {
			return path[:i], true
		}
	}
	if i := strings.Index(path, clipMark); i >= 0 {
		return path[:i], true
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
func shownOutside(shown, root string) bool {
	if !strings.HasPrefix(shown, "/") || root == "/" {
		return false
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
