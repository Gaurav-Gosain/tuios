package vt

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestOSC4_PaletteQuery(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()

	// Set a custom color for index 1
	e.Write([]byte("\x1b]4;1;rgb:ff/00/00\x1b\\"))

	// Query color index 1
	responseChan := make(chan string, 1)
	errChan := make(chan error, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := e.Read(buf)
		if err != nil && err != io.EOF {
			errChan <- err
			return
		}
		responseChan <- string(buf[:n])
	}()

	e.Write([]byte("\x1b]4;1;?\x1b\\"))

	select {
	case response := <-responseChan:
		// Should contain a color response with index 1
		if !strings.Contains(response, "4;1;") {
			t.Errorf("expected response containing '4;1;', got %q", response)
		}
	case err := <-errChan:
		t.Fatalf("Read error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for OSC 4 response")
	}
}

func TestOSC52_ClipboardQuery(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()

	// Query clipboard
	responseChan := make(chan string, 1)
	errChan := make(chan error, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := e.Read(buf)
		if err != nil && err != io.EOF {
			errChan <- err
			return
		}
		responseChan <- string(buf[:n])
	}()

	e.Write([]byte("\x1b]52;c;?\x1b\\"))

	select {
	case response := <-responseChan:
		// Should get an empty clipboard response
		if !strings.Contains(response, "52;c;") {
			t.Errorf("expected response containing '52;c;', got %q", response)
		}
	case err := <-errChan:
		t.Fatalf("Read error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for OSC 52 response")
	}
}

func TestModeSynchronizedOutput(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()

	// Enable synchronized output mode
	e.Write([]byte("\x1b[?2026h"))

	// Query the mode via DECRQM
	responseChan := make(chan string, 1)
	errChan := make(chan error, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := e.Read(buf)
		if err != nil && err != io.EOF {
			errChan <- err
			return
		}
		responseChan <- string(buf[:n])
	}()

	e.Write([]byte("\x1b[?2026$p"))

	select {
	case response := <-responseChan:
		// Should respond with mode set (1) or reset (2)
		// CSI ? 2026 ; Ps $ y where Ps=1 means set
		if !strings.Contains(response, "2026") {
			t.Errorf("expected response containing '2026', got %q", response)
		}
	case err := <-errChan:
		t.Fatalf("Read error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for DECRQM response")
	}
}
