//go:build !windows

package agentproto

import (
	"io"
	"os"
	"strings"
	"testing"
)

// TestProcessHasNoTerminal: the agent cannot open the pane's terminal, so
// nothing it writes reaches the pane except through the transcript. Skipped
// where the test itself has no terminal, since nothing is proved there.
func TestProcessHasNoTerminal(t *testing.T) {
	if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err != nil {
		t.Skip("the test has no controlling terminal")
	} else {
		_ = f.Close()
	}
	p, err := StartProcess([]string{"/bin/sh", "-c", "if (: > /dev/tty) 2>/dev/null; then echo tty; else echo none; fi"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	out, _ := io.ReadAll(p.Stdout)
	if got := strings.TrimSpace(string(out)); got != "none" {
		t.Fatalf("the agent could open /dev/tty (%q)", got)
	}
}
