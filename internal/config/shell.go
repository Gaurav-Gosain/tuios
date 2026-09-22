package config

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ResolveShell returns the shell a new pane runs when nothing more specific
// names one, in the order ShellFor uses. A nil cfg skips
// appearance.preferred_shell. A preferred shell that does not exist is
// reported on stderr, which is where the standalone and CLI paths that call
// this have always reported it.
func ResolveShell(cfg *UserConfig) string {
	preferred := ""
	if cfg != nil {
		preferred = cfg.Appearance.PreferredShell
	}
	shell, missing := ShellFor(preferred)
	if missing {
		fmt.Fprintf(os.Stderr, "Warning: Configured shell '%s' not found. Falling back to defaults.\n", preferred)
	}
	return shell
}

// ShellFor picks a shell: preferred when it names one that exists, then
// $SHELL, then the first platform default found. missing reports a preferred
// shell that was named and not found, so the caller can say so where its user
// will see it.
//
// On Windows a missing .exe suffix is added to preferred and the name is
// looked up on PATH; elsewhere it must be a path that exists.
//
// The standalone window path, the CLI client and the daemon all come here, so
// a pane runs the same shell whichever of them spawns it.
func ShellFor(preferred string) (shell string, missing bool) {
	if preferred != "" {
		if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(preferred), ".exe") {
			preferred += ".exe"
		}
		var err error
		if runtime.GOOS == "windows" {
			_, err = exec.LookPath(preferred)
		} else {
			_, err = os.Stat(preferred)
		}
		if err == nil {
			return preferred, false
		}
		missing = true
	}

	if shell := os.Getenv("SHELL"); shell != "" {
		return shell, missing
	}

	if runtime.GOOS == "windows" {
		for _, shell := range []string{"powershell.exe", "pwsh.exe", "cmd.exe"} {
			if _, err := exec.LookPath(shell); err == nil {
				return shell, missing
			}
		}
		return "cmd.exe", missing
	}

	for _, shell := range []string{"/bin/bash", "/bin/zsh", "/bin/fish", "/bin/sh"} {
		if _, err := os.Stat(shell); err == nil {
			return shell, missing
		}
	}
	return "/bin/sh", missing
}
