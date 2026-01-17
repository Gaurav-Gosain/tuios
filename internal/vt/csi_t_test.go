package vt

import (
	"io"
	"testing"
	"time"
)

func TestCSI14t_ReportPixelSize(t *testing.T) {
	// Create an emulator with known cell size
	e := NewEmulator(80, 24)
	e.SetCellSize(10, 20) // 10x20 pixel cells

	// Read response in goroutine
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

	// Send CSI 14 t query (request terminal size in pixels)
	query := "\x1b[14t"
	n, err := e.Write([]byte(query))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(query) {
		t.Fatalf("Expected to write %d bytes, wrote %d", len(query), n)
	}

	// Wait for response with timeout
	select {
	case response := <-responseChan:
		// Expected response: CSI 4 ; height ; width t
		// With 80x24 terminal and 10x20 cells:
		// height = 24 * 20 = 480
		// width = 80 * 10 = 800
		// Expected: "\x1b[4;480;800t"
		expected := "\x1b[4;480;800t"
		if response != expected {
			t.Errorf("Expected response %q, got %q", expected, response)
		} else {
			t.Logf("Correct response: %q", response)
		}
	case err := <-errChan:
		t.Fatalf("Read error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}

func TestCSI16t_ReportCellSize(t *testing.T) {
	// Create an emulator with known cell size
	e := NewEmulator(80, 24)
	e.SetCellSize(10, 20) // 10x20 pixel cells

	// Read response in goroutine
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

	// Send CSI 16 t query (request cell size in pixels)
	query := "\x1b[16t"
	n, err := e.Write([]byte(query))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(query) {
		t.Fatalf("Expected to write %d bytes, wrote %d", len(query), n)
	}

	// Wait for response with timeout
	select {
	case response := <-responseChan:
		// Expected response: CSI 6 ; cellHeight ; cellWidth t
		// With 10x20 cells: "\x1b[6;20;10t"
		expected := "\x1b[6;20;10t"
		if response != expected {
			t.Errorf("Expected response %q, got %q", expected, response)
		} else {
			t.Logf("Correct response: %q", response)
		}
	case err := <-errChan:
		t.Fatalf("Read error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}
