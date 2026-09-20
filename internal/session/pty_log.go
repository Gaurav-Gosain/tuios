package session

import (
	"os"
	"path/filepath"
	"sync"
)

// The raw output log: every byte a pane's process wrote, as it was written.
//
// It exists because a rendering fault that only shows up in a live session
// cannot be reasoned about from a screenshot or from a capture taken
// afterwards. A capture shows the grid as it is now, which says nothing about
// the moment it went wrong, and the grid is downstream of the bytes anyway.
// With the bytes, the emulator can be fed exactly what it was fed and the
// frame where it diverges can be found.
//
// It is off unless TUIOS_PTY_LOG names a directory, and it writes one file per
// pane. Nothing is filtered or decoded on the way in: the point is to have the
// stream as the program wrote it, escape sequences and all.
//
// It is deliberately not part of tape recording. A tape is a script of what a
// person did, meant to be replayed as input; this is the other direction and
// is meant for one purpose, which is to be replayed into an emulator.

// ptyLogDir is the directory the raw logs go in, empty when logging is off.
func ptyLogDir() string { return os.Getenv("TUIOS_PTY_LOG") }

// ptyLogger appends one pane's raw output to a file.
type ptyLogger struct {
	mu sync.Mutex
	f  *os.File
}

// newPTYLogger opens the log for a pane, or returns nil when logging is off or
// the file cannot be made.
//
// A failure here is silent and costs nothing but the log. This is a debugging
// aid, and a pane that will not start because a log file could not be opened
// would be a worse bug than the one being looked for.
func newPTYLogger(paneID string) *ptyLogger {
	dir := ptyLogDir()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, paneID+".raw"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	return &ptyLogger{f: f}
}

// Write appends a chunk. It holds its own lock rather than riding the stream
// lock, so a slow disk cannot stall the pane any longer than the write itself.
func (l *ptyLogger) Write(b []byte) {
	if l == nil {
		return
	}
	l.mu.Lock()
	_, _ = l.f.Write(b)
	l.mu.Unlock()
}

// Close closes the file.
func (l *ptyLogger) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	_ = l.f.Close()
	l.mu.Unlock()
}
