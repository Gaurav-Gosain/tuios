package terminal

import (
	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"tuios.dev/vt"
)

func init() { vt.SetDebugLogger(debuglog.Internal{}) }
