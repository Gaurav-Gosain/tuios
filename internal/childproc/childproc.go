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
)

// Command is exec.Command for a program that must not open a window.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...) //tuios:allow-exec the one place that hides it
	Hide(cmd)
	return cmd
}

// CommandContext is exec.CommandContext for a program that must not open a
// window.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...) //tuios:allow-exec the one place that hides it
	Hide(cmd)
	return cmd
}
