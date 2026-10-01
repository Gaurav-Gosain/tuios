//go:build windows

package tmuxcompat

import (
	"errors"
	"fmt"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/shimlink"
)

// holderSupported reports whether this platform runs pane holders. On
// Windows the shim runs a pane's command directly, and respawn-pane fails.
const holderSupported = false

var errNoHolder = errors.New("the tmux shim's pane holder is not supported on Windows")

// EnsureDir creates the shim's runtime directory.
func EnsureDir(dir string) error { return shimlink.EnsureDir(dir) }

// InstallLink is not supported on Windows.
func InstallLink(dir, exe string) error { return errNoHolder }

// ExecCommand is not supported on Windows.
func ExecCommand(argv, env []string) error { return errNoHolder }

// RunPane is not supported on Windows.
func RunPane(o PaneOptions) int {
	fmt.Fprintln(os.Stderr, errNoHolder)
	return 1
}
