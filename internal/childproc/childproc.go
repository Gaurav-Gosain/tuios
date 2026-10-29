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

// OwnGroup starts cmd in a process group of its own and makes its context end
// that whole group, so a command that started children of its own cannot
// outlive its deadline through them. A no-op where there are no process
// groups. cmd must come from CommandContext for the context to apply.
func OwnGroup(cmd *exec.Cmd) { ownGroup(cmd) }
