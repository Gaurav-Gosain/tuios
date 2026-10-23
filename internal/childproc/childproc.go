// Package childproc starts the helper programs the daemon runs, such as git,
// ssh, gh and hook commands, so that none of them opens a window.
//
// On Windows a daemon with no console gives each console program it starts a
// console of its own, and when Windows Terminal is the default terminal that
// console is a window on the person's screen. Each agent checkpoint ran git a
// few times, so each one flashed several windows and took seconds while they
// opened (issue #656). Command and CommandContext start the program with
// CREATE_NO_WINDOW instead. On every other OS they are exec.Command and
// exec.CommandContext.
//
// tuios now starts its daemon on a hidden console, which its children
// inherit. This package covers a daemon started some other way, such as by
// hand, by a scheduled task or over ssh. internal/lint rejects a bare
// exec.Command in the daemon's packages, so a new call site cannot miss it.
package childproc

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// gitArgs puts the settings every git the daemon runs gets in front of args.
//
// A repository's own config can name programs git runs: core.fsmonitor runs
// one on every status and diff. The daemon runs git in folders a pane
// controls, and a program git starts runs outside every pane, so a pane could
// get code run as a child of the daemon. core.fsmonitor only speeds git up,
// so it is off.
func gitArgs(name string, args []string) []string {
	// A path to git is git too, here or in a later caller.
	if base := strings.TrimSuffix(filepath.Base(name), ".exe"); base != "git" {
		return args
	}
	return append([]string{"-c", "core.fsmonitor=false"}, args...)
}

// WithGitSettings adds the settings of gitArgs to env, for a program the
// daemon runs that runs git itself, such as gh. They go in as
// GIT_CONFIG_COUNT entries after any env already has.
func WithGitSettings(env []string) []string {
	n := 0
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "GIT_CONFIG_COUNT="); ok {
			if c, err := strconv.Atoi(v); err == nil && c > 0 {
				n = c
			}
		}
	}
	i := strconv.Itoa(n)
	return append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(n+1),
		"GIT_CONFIG_KEY_"+i+"=core.fsmonitor", "GIT_CONFIG_VALUE_"+i+"=false")
}

// Command is exec.Command for a program that must not open a window.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, gitArgs(name, args)...) //tuios:allow-exec the one place that hides it
	Hide(cmd)
	return cmd
}

// CommandContext is exec.CommandContext for a program that must not open a
// window.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, gitArgs(name, args)...) //tuios:allow-exec the one place that hides it
	Hide(cmd)
	return cmd
}
