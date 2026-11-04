package main

import (
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

var (
	isMac      = runtime.GOOS == "darwin"
	homeDir, _ = os.UserHomeDir()
	// keySent is when the last key went to the model, for the latency
	// probe of the control socket. 0 when none is pending.
	keySent atomic.Int64
)

func nowNano() int64 { return time.Now().UnixNano() }
