package vt

import "sync/atomic"

// debugLoggerBox holds the process-wide debug logger, because atomic.Pointer
// cannot hold an interface value directly.
type debugLoggerBox struct{ l Logger }

var debugLog atomic.Pointer[debugLoggerBox]

// SetDebugLogger sets a process-wide logger for diagnostic lines that no single
// emulator owns, such as every XTWINOPS request a program sends. It is nil by
// default, and then nothing is formatted. Each line is one message without a
// trailing newline. Call it once at startup, before the first emulator exists.
func SetDebugLogger(l Logger) {
	if l == nil {
		debugLog.Store(nil)
		return
	}
	debugLog.Store(&debugLoggerBox{l: l})
}

// debugLogger returns the logger SetDebugLogger set, or nil.
func debugLogger() Logger {
	if b := debugLog.Load(); b != nil {
		return b.l
	}
	return nil
}
