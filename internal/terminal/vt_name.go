package terminal

import "github.com/Gaurav-Gosain/tuios/internal/vt"

// A guest that asks for the terminal's name (XTVERSION) learns it runs in
// tuios. Both binaries link this package, the daemon included, which is where
// the panes answer. cmd/tuios adds the release version.
func init() { vt.SetTerminalName("tuios") }
