package terminal

import (
	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// The emulator writes its process-wide diagnostics (XTWINOPS requests) to the
// tuios debug log when TUIOS_DEBUG_INTERNAL=1. Both binaries link this
// package, the daemon included.
func init() { vt.SetDebugLogger(debuglog.Internal{}) }
