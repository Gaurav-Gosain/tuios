package session

import (
	"testing"
)

// feedVT writes bytes to a PTY's daemon-side emulator the way its output reader
// would, so a test can hand a window the escape sequence an application uses to
// set its title.
func feedVT(t *testing.T, p *PTY, data string) {
	t.Helper()
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	if _, err := p.terminal.Write([]byte(data)); err != nil {
		t.Fatalf("write to the daemon emulator: %v", err)
	}
}
