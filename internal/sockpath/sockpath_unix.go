//go:build !windows

package sockpath

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dir is the folder the daemon's socket lives in: $XDG_RUNTIME_DIR/tuios, or
// /tmp/tuios-<uid> without a runtime directory. It only names the folder;
// the daemon creates and checks it.
func Dir() string {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "tuios")
	}
	return filepath.Join("/tmp", fmt.Sprintf("tuios-%d", os.Getuid()))
}
