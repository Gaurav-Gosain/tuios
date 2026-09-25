package terminal

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// TestResetTerminalSequences verifies specific escape sequences are in correct order.
func TestResetTerminalSequences(t *testing.T) {
	// Save original stdout
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}

	os.Stdout = w
	ResetTerminal()
	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.Bytes()

	// Expected sequences in order
	sequences := []struct {
		name string
		seq  []byte
	}{
		{"terminal reset", []byte{0x1b, 'c'}},
		{"disable normal tracking", []byte("\033[?1000l")},
		{"disable button event tracking", []byte("\033[?1002l")},
		{"disable all motion tracking", []byte("\033[?1003l")},
		{"disable focus tracking", []byte("\033[?1004l")},
		{"disable SGR extended mouse", []byte("\033[?1006l")},
		{"show cursor", []byte("\033[?25h")},
		{"exit alternate screen", []byte("\033[?47l")},
		{"reset attributes", []byte("\033[0m")},
	}

	lastIndex := 0
	for _, seq := range sequences {
		idx := bytes.Index(output[lastIndex:], seq.seq)
		if idx == -1 {
			t.Errorf("Expected to find %s sequence after position %d", seq.name, lastIndex)
			continue
		}
		lastIndex += idx + len(seq.seq)
	}
}
