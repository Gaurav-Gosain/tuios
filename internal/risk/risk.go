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

const clipMark = "\uffff"

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
	Tools   []string
	command func(c command, call Call) bool
	path    func(target string, call Call) bool
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

// Same reports whether r and o are the same rule: the same name, tools and
// pattern. A shipped rule has no pattern, so a rule of the person's that takes
// a shipped rule's name is not the shipped rule.
func (r Rule) Same(o Rule) bool {
	if r.Name != o.Name || !slices.Equal(r.Tools, o.Tools) || (r.pattern == nil) != (o.pattern == nil) {
		return false
	}
	return r.pattern == nil || r.pattern.String() == o.pattern.String()
}

// ShellTools are the tool names whose Text is a command line. A call with no
// tool name is read as one too.
var ShellTools = []string{"Bash", "bash", "shell", "exec_command", "local_shell", "run_shell_command", "execute", "terminal"}

// FileTools are the tool names whose Text is the path they write.
var FileTools = []string{"Write", "Edit", "MultiEdit", "NotebookEdit", "write", "edit", "multiedit", "patch", "write_file", "replace"}

func isShell(tool string) bool {
	return tool == "" || containsFold(ShellTools, tool)
}

func isFile(tool string) bool {
	return containsFold(FileTools, tool)
}

func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, s) })
}

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

const maxDepth = 4

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

type input struct {
	text     string
	cmds     []command
	pathText string
	pathCmds []command
}

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
				dirs[c.scope] = ""
			case c.inPipe:
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

func outside(path string, call Call) bool {
	if path == "" {
		return false
	}
	path = strings.Trim(path, `"'`)
	path, known := expandHome(path, call.Home)
	if !known {
		return true
	}
	root := fixedRoot(call.Root)
	if !strings.HasPrefix(path, "/") {
		run := ""
		if call.RunDir != "" && filepath.IsAbs(call.RunDir) {
			run = call.resolve(filepath.Clean(call.RunDir))
		}
		if root == "" || run == "" || !under(run, root) {
			return true
		}
		path = run + string(filepath.Separator) + path
	}
	if shown, cut := shownPart(path, call); cut {
		return shownOutside(shown, root, call.resolve)
	}
	path = filepath.Clean(path)
	if slices.Contains(harmlessDevices, path) || strings.HasPrefix(path, "/dev/fd/") {
		return false
	}
	if root == "" {
		return true
	}
	return !under(call.resolve(path), root)
}

func under(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

func (call Call) resolve(path string) string {
	if call.Resolve == nil {
		return path
	}
	return call.Resolve(path)
}

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
	return strings.TrimSuffix(home, "/") + rest, true
}

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

func shownOutside(shown, root string, resolve func(string) string) bool {
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
	dir = resolve(dir)
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
