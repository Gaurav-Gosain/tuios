package risk

import (
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
)

// Names of the shipped rules, as the Inbox shows them.
const (
	RuleRecursiveDelete = "recursive delete"
	RuleForcePush       = "force push"
	RuleHardReset       = "hard reset"
	RuleClean           = "clean"
	RuleDiscardChanges  = "discard changes"
	RulePipeToShell     = "pipe to shell"
	RuleSudo            = "sudo"
	RuleDisk            = "disk"
	RuleWidePermissions = "wide permissions"
	RuleDatabase        = "database"
	RuleInfrastructure  = "infrastructure"
	RuleOutsideWorktree = "outside the worktree"
	// RuleCutShort is not a rule of its own: the daemon marks an approval
	// with it when the only line it has for the call does not show all of
	// it (clipped, redacted, or only part of a dialog), so the rules could
	// not read the rest. See CutShortHit.
	RuleCutShort = "cut short"
)

// CutShortHit is the mark for a call whose line was cut short.
var CutShortHit = Hit{Rule: RuleCutShort, Why: "the line does not show the whole call, so a risky part may be hidden"}

// Why is what a shipped rule, or the cut short mark, guards against.
func Why(name string) (string, bool) {
	if name == RuleCutShort {
		return CutShortHit.Why, true
	}
	for _, r := range Builtin() {
		if r.Name == name {
			return r.Why, true
		}
	}
	return "", false
}

// Builtin returns the rules tuios ships, in the order the Inbox lists them.
func Builtin() []Rule {
	return []Rule{
		{Name: RuleRecursiveDelete, Why: "deletes a tree of files without asking", command: recursiveDelete},
		{Name: RuleForcePush, Why: "can overwrite commits on the remote", command: forcePush},
		{Name: RuleHardReset, Why: "throws away uncommitted changes", command: hardReset},
		{Name: RuleClean, Why: "deletes untracked files", command: gitClean},
		{Name: RuleDiscardChanges, Why: "throws away changes in the working tree", command: discardChanges},
		{Name: RulePipeToShell, Why: "runs a downloaded script", command: pipeToShell},
		{Name: RuleSudo, Why: "runs as root", command: func(c command, _ Call) bool { return c.sudo }},
		{Name: RuleDisk, Why: "writes to a disk device or makes a file system", command: disk},
		{Name: RuleWidePermissions, Why: "opens up permissions across a tree", command: widePermissions},
		{Name: RuleDatabase, Why: "drops or empties database tables", command: database},
		{Name: RuleInfrastructure, Why: "changes deployed infrastructure or publishes a package", command: infrastructure},
		{Name: RuleOutsideWorktree, Why: "writes outside the pane's worktree", command: writesOutside, path: outside},
	}
}

func flags(args []string) (short string, long []string) {
	for _, a := range args {
		switch {
		case a == "--":
			return short, long
		case strings.HasPrefix(a, "--"):
			name, _, _ := strings.Cut(a, "=")
			long = append(long, name)
		case strings.HasPrefix(a, "-") && len(a) > 1:
			short += a[1:]
		}
	}
	return short, long
}

func operands(args []string) []string {
	var out []string
	dashes := false
	for _, a := range args {
		switch {
		case dashes:
			out = append(out, a)
		case a == "--":
			dashes = true
		case strings.HasPrefix(a, "-") && len(a) > 1:
		default:
			out = append(out, a)
		}
	}
	return out
}

func recursiveDelete(c command, _ Call) bool {
	if c.name() != "rm" {
		return false
	}
	short, long := flags(c.args())
	recursive := strings.ContainsAny(short, "rR") || slices.Contains(long, "--recursive")
	force := strings.Contains(short, "f") || slices.Contains(long, "--force")
	return recursive && force
}

func gitArgs(c command) (string, []string) {
	if c.name() != "git" {
		return "", nil
	}
	args := c.args()
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--namespace":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}

var jjValueFlags = []string{
	"-R", "--repository", "--at-op", "--at-operation", "--config", "--config-file", "--color",
	"-r", "--revision", "--revisions", "-f", "--from", "-t", "--to", "--into", "-c", "--changes-in",
	"--tool", "-b", "--bookmark", "--change", "--named", "--remote", "--name", "--sparse-patterns",
	"--depth", "-m", "--message",
}

func jjOperands(c command) []string {
	if c.name() != "jj" {
		return nil
	}
	var out []string
	args := c.args()
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return append(out, args[i+1:]...)
		case slices.Contains(jjValueFlags, a):
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1:
		default:
			out = append(out, a)
		}
	}
	return out
}

func jjIs(ops []string, words ...string) bool {
	return len(ops) >= len(words) && slices.Equal(ops[:len(words)], words)
}

func forcePush(c command, _ Call) bool {
	if jjIs(jjOperands(c), "git", "push") {
		return !slices.Contains(c.args(), "--dry-run")
	}
	sub, args := gitArgs(c)
	if sub != "push" {
		return false
	}
	short, long := flags(args)
	if strings.Contains(short, "f") {
		return true
	}
	if slices.Contains(long, "--force") || slices.Contains(long, "--force-with-lease") {
		return true
	}
	for _, o := range operands(args) {
		if strings.HasPrefix(o, "+") {
			return true
		}
	}
	return false
}

func hardReset(c command, _ Call) bool {
	if jjIs(jjOperands(c), "op", "restore") {
		return true
	}
	sub, args := gitArgs(c)
	return sub == "reset" && slices.Contains(args, "--hard")
}

func gitClean(c command, _ Call) bool {
	sub, args := gitArgs(c)
	if sub != "clean" {
		return false
	}
	short, long := flags(args)
	return strings.Contains(short, "f") || slices.Contains(long, "--force")
}

func discardChanges(c command, _ Call) bool {
	if ops := jjOperands(c); jjIs(ops, "abandon") {
		return true
	} else if jjIs(ops, "restore") {
		paths := ops[1:]
		return len(paths) == 0 || slices.ContainsFunc(paths, func(p string) bool { return p == "." || p == "root:" || p == `root:""` })
	}
	sub, args := gitArgs(c)
	if sub != "checkout" && sub != "restore" {
		return false
	}
	for _, o := range operands(args) {
		if o == "." || o == ":/" || o == "*" {
			return true
		}
	}
	return false
}

var pipeTargets = []string{"sh", "bash", "zsh", "dash", "ksh", "fish", "python", "python3", "node"}

func pipeToShell(c command, _ Call) bool {
	if slices.ContainsFunc(c.fedBy, isDownload) {
		return true
	}
	if !slices.Contains(pipeTargets, c.name()) && !c.sudo {
		return false
	}
	return slices.ContainsFunc(c.pipedFrom, isDownload)
}

func isDownload(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	switch baseName(argv[0]) {
	case "curl", "wget":
		return true
	}
	return false
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

var diskDevice = lazyre.New(`^/dev/(sd[a-z]|nvme\d|disk\d|rdisk\d|hd[a-z]|vd[a-z]|xvd[a-z]|mmcblk\d)`)

func disk(c command, _ Call) bool {
	name := c.name()
	if name == "mkfs" || strings.HasPrefix(name, "mkfs.") || name == "newfs" || name == "wipefs" {
		return true
	}
	if name == "dd" {
		for _, a := range c.args() {
			if strings.HasPrefix(a, "of=") {
				return true
			}
		}
	}
	return slices.ContainsFunc(c.writes, diskDevice().MatchString)
}

func widePermissions(c command, call Call) bool {
	short, long := flags(c.args())
	recursive := strings.Contains(short, "R") || slices.Contains(long, "--recursive")
	if !recursive {
		return false
	}
	ops := operands(c.args())
	switch c.name() {
	case "chmod":
		for _, o := range ops {
			if o == "777" || o == "0777" || o == "a+rwx" || o == "ugo+rwx" {
				return true
			}
		}
	case "chown", "chgrp":
		for _, o := range ops {
			if o == "/" || o == "~" || o == "~/" || o == "$HOME" || (call.Home != "" && strings.TrimRight(o, "/") == strings.TrimRight(call.Home, "/")) {
				return true
			}
		}
	}
	return false
}

var dropTable = lazyre.New(`(?i)\bDROP\s+(TABLE|DATABASE|SCHEMA)\b|\bTRUNCATE\s+(TABLE\b|[A-Za-z_"` + "`" + `])`)

func database(c command, _ Call) bool {
	return dropTable().MatchString(c.raw)
}

func infrastructure(c command, _ Call) bool {
	ops := operands(c.args())
	first := func(words ...string) bool {
		return len(ops) > 0 && slices.Contains(words, ops[0])
	}
	switch c.name() {
	case "terraform", "tofu":
		return first("apply", "destroy")
	case "kubectl":
		return slices.Contains(ops, "delete")
	case "docker", "podman":
		return len(ops) >= 2 && ops[0] == "system" && ops[1] == "prune"
	case "npm", "pnpm", "yarn", "cargo":
		return first("publish")
	}
	return false
}

func writesOutside(c command, call Call) bool {
	for _, w := range c.writes {
		if outside(w, call) {
			return true
		}
	}
	args := c.args()
	ops := operands(args)
	switch c.name() {
	case "curl":
		if anyOutside(flagValues(args, "o", "--output"), call) {
			return true
		}
	case "wget":
		if anyOutside(flagValues(args, "O", "--output-document"), call) || anyOutside(flagValues(args, "P", "--directory-prefix"), call) {
			return true
		}
	case "tar":
		short, long := flags(args)
		if (strings.Contains(short, "x") || slices.Contains(long, "--extract") || slices.Contains(long, "--get")) &&
			anyOutside(flagValues(args, "C", "--directory"), call) {
			return true
		}
	case "git":
		if len(ops) >= 3 && ops[0] == "clone" {
			dir := ops[len(ops)-1]
			if !strings.Contains(dir, "://") && !strings.Contains(dir, "@") && outside(dir, call) {
				return true
			}
		}
	case "jj":
		ops := jjOperands(c)
		if jjIs(ops, "git", "clone") && len(ops) >= 4 && outside(ops[len(ops)-1], call) {
			return true
		}
		if jjIs(ops, "workspace", "add") && len(ops) >= 3 && outside(ops[2], call) {
			return true
		}
	case "cp", "mv", "ln", "install", "rsync":
		if anyOutside(flagValues(args, "", "--target-directory"), call) {
			return true
		}
		if len(ops) >= 2 && outside(ops[len(ops)-1], call) {
			return true
		}
		if short, long := flags(args); strings.Contains(short, "t") || slices.Contains(long, "--target-directory") {
			for _, o := range ops {
				if outside(o, call) {
					return true
				}
			}
		}
	case "rm", "rmdir", "tee", "touch", "mkdir", "truncate", "shred", "unlink":
		for _, o := range ops {
			if outside(o, call) {
				return true
			}
		}
	case "sed":
		short, long := flags(args)
		if strings.Contains(short, "i") || slices.Contains(long, "--in-place") {
			for _, o := range ops {
				if outside(o, call) {
					return true
				}
			}
		}
	}
	return false
}

func anyOutside(paths []string, call Call) bool {
	return slices.ContainsFunc(paths, func(p string) bool { return outside(p, call) })
}

func flagValues(args []string, short, long string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return out
		case long != "" && a == long:
			if i+1 < len(args) {
				out = append(out, args[i+1])
				i++
			}
		case long != "" && strings.HasPrefix(a, long+"="):
			out = append(out, strings.TrimPrefix(a, long+"="))
		case short != "" && strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1:
			j := strings.Index(a[1:], short)
			if j < 0 {
				continue
			}
			if rest := a[1+j+1:]; rest != "" {
				out = append(out, rest)
			} else if i+1 < len(args) {
				out = append(out, args[i+1])
				i++
			}
		}
	}
	return out
}
