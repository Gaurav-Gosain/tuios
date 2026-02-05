package terminal

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// TestResetTerminal verifies that ResetTerminal doesn't panic and produces output.
// Since it writes escape sequences to stdout, we capture output to verify behavior.
func TestResetTerminal(t *testing.T) {
	// Save original stdout
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}

	// Redirect stdout to our pipe
	os.Stdout = w

	// Call ResetTerminal - should not panic
	ResetTerminal()

	// Restore stdout
	_ = w.Close()
	os.Stdout = oldStdout

	// Read captured output
	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	if err != nil {
		t.Fatalf("Failed to read captured output: %v", err)
	}

	output := buf.Bytes()

	// Verify some escape sequences are present
	if len(output) == 0 {
		t.Error("Expected ResetTerminal to produce output")
	}

	// Check for ESC character (0x1b)
	if !bytes.Contains(output, []byte{0x1b}) {
		t.Error("Expected output to contain escape sequences")
	}

	// Check for reset sequence (ESC c)
	if !bytes.Contains(output, []byte{0x1b, 'c'}) {
		t.Error("Expected output to contain terminal reset sequence (ESC c)")
	}

	// Check for cursor show sequence (ESC [?25h)
	if !bytes.Contains(output, []byte{0x1b, '[', '?', '2', '5', 'h'}) {
		t.Error("Expected output to contain cursor show sequence")
	}

	// Check for attribute reset (ESC [0m)
	if !bytes.Contains(output, []byte{0x1b, '[', '0', 'm'}) {
		t.Error("Expected output to contain attribute reset sequence")
	}

	// Check for line ending
	if !bytes.Contains(output, []byte{'\r', '\n'}) {
		t.Error("Expected output to contain CRLF line ending")
	}
}

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
