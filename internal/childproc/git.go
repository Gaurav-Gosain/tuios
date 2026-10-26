package childproc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// tuios runs git in folders a pane controls, and a repository can name
// programs that git runs: hooks, filter and diff drivers, signing and ssh
// programs, credential helpers. A program git starts for the daemon or the
// client runs as their child. Git builds every git tuios runs, so this file
// is the one place that decides which of those programs run.
//
// Each git gets three kinds of setting:
//
//   - Fixed settings that turn the repository's own programs off: no hooks
//     (core.hooksPath is the null device), no fsmonitor, no external diff,
//     no ext:: transport, and no look inside a submodule's work tree, where
//     a nested repository's own config would apply.
//   - For each config key that names a program and is set in the
//     repository's own config (local or worktree scope, includes counted),
//     the value the person's system, global or command-line config gives
//     it, or a value that runs nothing. A filter git-lfs installed in
//     ~/.gitconfig keeps working. A filter set only in .git/config does not
//     run, so its files are read as they are on disk.
//   - On diff, log and show, --no-ext-diff and --no-textconv.
//
// The settings go in as GIT_CONFIG_COUNT entries, which git reads as
// command-line config. A key there needs no quoting, so a driver name that
// holds "=" cannot split it, and the git a submodule check starts inherits
// them.
//
// The person's own act is different. Ship called by the person makes their
// commit the way their git makes one, hooks included. AsPerson marks a
// context for that, and Git then adds only core.fsmonitor=false.

// personKey marks a context whose git is the person's own act.
type personKey struct{}

// AsPerson returns ctx marked so that Git runs with the repository's own
// configuration, hooks included. Use it only for a git the person asked for
// themselves, never for one a pane can start.
func AsPerson(ctx context.Context) context.Context {
	return context.WithValue(ctx, personKey{}, true)
}

func isPerson(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(personKey{}).(bool)
	return v
}

// Git returns git with args, to run in dir, with the settings above. ctx may
// be nil for a git with no deadline. The caller may add to cmd.Env, but must
// not replace it. When the repository's config cannot be read, the command
// fails to start with that error, and git does not run.
func Git(ctx context.Context, dir string, args ...string) *exec.Cmd {
	person := isPerson(ctx)
	if !person {
		args = diffFlags(args)
	}
	var cmd *exec.Cmd
	if ctx == nil {
		cmd = exec.Command("git", args...) //tuios:allow-exec the one place that builds git
	} else {
		cmd = exec.CommandContext(ctx, "git", args...) //tuios:allow-exec the one place that builds git
	}
	Hide(cmd)
	cmd.Dir = dir
	env, err := GitEnv(ctx, dir, nil)
	cmd.Env = env
	if err != nil {
		cmd.Err = err
	}
	return cmd
}

// GitEnv is env (os.Environ when nil) with the settings above for a git that
// runs in dir, for a program tuios starts that runs git itself, such as gh.
// It drops the variables that would point git at another repository or
// index, and sets git to never prompt or open an editor.
func GitEnv(ctx context.Context, dir string, env []string) ([]string, error) {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+16)
	n := 0
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		switch name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE":
			continue
		case "GIT_CONFIG_COUNT":
			if c, err := strconv.Atoi(value); err == nil && c > 0 {
				n = c
			}
		}
		out = append(out, kv)
	}
	out = append(out, "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true")
	if isPerson(ctx) {
		return appendConfig(out, n, [][2]string{{"core.fsmonitor", "false"}}), nil
	}
	over, err := repoOverrides(ctx, dir, out)
	if err != nil {
		return out, err
	}
	return appendConfig(out, n, append(over, fixedSettings()...)), nil
}

// fixedSettings are the settings every git gets.
func fixedSettings() [][2]string {
	return [][2]string{
		// A repository's fsmonitor names a program git runs on status and diff.
		{"core.fsmonitor", "false"},
		// No hook runs: .git/hooks and a hooks path the repository sets are
		// both replaced. On Windows os.DevNull is NUL, which holds no file.
		{"core.hooksPath", os.DevNull},
		{"diff.external", ""},
		// The ext:: transport runs a command named in the URL.
		{"protocol.ext.allow", "never"},
		// Status and diff look inside a submodule's work tree with a git of
		// its own, which reads that repository's config, where these
		// settings cannot see its drivers. A submodule's moved commit is
		// still reported.
		{"diff.ignoreSubmodules", "dirty"},
	}
}

// appendConfig adds kvs to env as GIT_CONFIG_COUNT entries after the n env
// already has.
func appendConfig(env []string, n int, kvs [][2]string) []string {
	env = slices.DeleteFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "GIT_CONFIG_COUNT=") })
	for i, kv := range kvs {
		idx := strconv.Itoa(n + i)
		env = append(env, "GIT_CONFIG_KEY_"+idx+"="+kv[0], "GIT_CONFIG_VALUE_"+idx+"="+kv[1])
	}
	return append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(n+len(kvs)))
}

// repoOverrides reads every config git would use in dir and returns, for each
// key that names a program and has a value from the repository's own config,
// the value that replaces it: the person's own, or one that runs nothing.
func repoOverrides(ctx context.Context, dir string, env []string) ([][2]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "git", "config", "--show-scope", "-z", "--list") //tuios:allow-exec the config read Git is built from
	Hide(cmd)
	ownGroup(cmd)
	cmd.WaitDelay = time.Second
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git config: %s", msg)
	}
	return overridesFrom(stdout.Bytes()), nil
}

// overridesFrom turns the output of git config --show-scope -z --list into
// the overrides. Each entry is the scope, NUL, the key, a newline and the
// value, NUL; a key with no value has no newline.
func overridesFrom(out []byte) [][2]string {
	type entry struct {
		key, value string
		trusted    bool
	}
	var entries []entry
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		key, value, _ := strings.Cut(fields[i+1], "\n")
		entries = append(entries, entry{key: key, value: value, trusted: trustedScope(fields[i])})
	}
	var over [][2]string
	done := map[string]bool{}
	for _, e := range entries {
		if e.trusted || done[e.key] {
			continue
		}
		neutral, multi, ok := programKey(e.key)
		if !ok {
			continue
		}
		done[e.key] = true
		var trusted []string
		for _, t := range entries {
			if t.trusted && t.key == e.key {
				trusted = append(trusted, t.value)
			}
		}
		switch {
		case multi:
			// An empty value clears the list. The person's own come back after it.
			over = append(over, [2]string{e.key, ""})
			for _, v := range trusted {
				over = append(over, [2]string{e.key, v})
			}
		case len(trusted) > 0:
			over = append(over, [2]string{e.key, trusted[len(trusted)-1]})
		default:
			over = append(over, [2]string{e.key, neutral})
		}
	}
	return over
}

// trustedScope reports whether config of this scope is the person's: the
// system and global files and the command line. The repository's own config,
// its worktree config and anything they include are not.
func trustedScope(scope string) bool {
	switch scope {
	case "system", "global", "command":
		return true
	}
	return false
}

// programKey reports whether key names a program git may run, with the value
// that runs nothing and whether the key holds a list. git prints the section
// and the last part of a key in lower case, and the subsection as written.
func programKey(key string) (neutral string, multi, ok bool) {
	section, rest, found := strings.Cut(key, ".")
	if !found {
		return "", false, false
	}
	sub, name := "", rest
	if i := strings.LastIndexByte(rest, '.'); i >= 0 {
		sub, name = rest[:i], rest[i+1:]
	}
	switch section {
	case "filter":
		switch name {
		case "clean", "smudge", "process":
			return "", false, sub != ""
		case "required":
			return "false", false, sub != ""
		}
	case "diff":
		if sub == "" {
			return "", false, name == "external"
		}
		return "", false, name == "textconv" || name == "command"
	case "merge":
		return "", false, sub != "" && name == "driver"
	case "gpg":
		switch {
		case name == "program":
			switch sub {
			case "x509":
				return "gpgsm", false, true
			case "ssh":
				return "ssh-keygen", false, true
			}
			return "gpg", false, true
		case sub == "ssh" && name == "defaultkeycommand":
			return "", false, true
		}
	case "core":
		if sub != "" {
			return "", false, false
		}
		switch name {
		case "sshcommand":
			return "ssh", false, true
		case "askpass", "alternaterefscommand":
			return "", false, true
		case "gitproxy":
			return "none", false, true
		}
	case "remote":
		switch name {
		case "uploadpack":
			return "git-upload-pack", false, sub != ""
		case "receivepack":
			return "git-receive-pack", false, sub != ""
		}
	case "credential":
		return "", true, name == "helper"
	case "lfs":
		// git-lfs runs from the person's own filter and reads the
		// repository's config for programs of its own.
		switch {
		case strings.HasPrefix(sub, "customtransfer.") && name == "path":
			return "", false, true
		case strings.HasPrefix(sub, "extension.") && (name == "clean" || name == "smudge"):
			return "", false, true
		}
	}
	return "", false, false
}

// diffFlags adds --no-ext-diff and --no-textconv after a diff, log or show
// subcommand, so no diff driver the repository names runs.
func diffFlags(args []string) []string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-c" || a == "-C":
			i++
			continue
		case strings.HasPrefix(a, "-"):
			continue
		}
		switch a {
		case "diff", "log", "show":
			out := make([]string, 0, len(args)+2)
			out = append(out, args[:i+1]...)
			out = append(out, "--no-ext-diff", "--no-textconv")
			return append(out, args[i+1:]...)
		}
		return args
	}
	return args
}
