package session

import (
	"os"
	"path/filepath"
	"sync"
)

func ptyLogDir() string { return os.Getenv("TUIOS_PTY_LOG") }

type ptyLogger struct {
	mu sync.Mutex
	f  *os.File
}

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

func (l *ptyLogger) Write(b []byte) {
	if l == nil {
		return
	}
	l.mu.Lock()
	_, _ = l.f.Write(b)
	l.mu.Unlock()
}

func (l *ptyLogger) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	_ = l.f.Close()
	l.mu.Unlock()
}
