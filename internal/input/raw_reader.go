// Package input implements raw terminal input reading for terminal mode.
//
// This module provides direct TTY reading that bypasses Bubbletea's input parsing,
// allowing raw byte sequences to be forwarded directly to PTY without conversion.
package input

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

// RawInputReader reads raw bytes from /dev/tty for direct PTY forwarding.
// This bypasses Bubbletea's input parsing to ensure perfect terminal compatibility.
type RawInputReader struct {
	tty           *os.File
	originalState *term.State
	stopChan      chan struct{}
	inputChan     chan []byte
	running       bool
	mu            sync.Mutex
}

// NewRawInputReader creates a new raw input reader.
func NewRawInputReader() *RawInputReader {
	return &RawInputReader{
		stopChan: make(chan struct{}),
		// Buffer 100 input sequences to handle bursts (paste operations, etc.)
		// This prevents blocking on channel send while maintaining low latency
		inputChan: make(chan []byte, 100),
	}
}

// Start begins reading raw input from /dev/tty.
// Opens /dev/tty, sets terminal to raw mode, and starts reading goroutine.
func (r *RawInputReader) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("raw reader already running")
	}

	// Open /dev/tty for raw reading
	// We use /dev/tty instead of stdin because Bubbletea owns stdin
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open /dev/tty: %w", err)
	}
	r.tty = tty

	// Get file descriptor for terminal operations
	fd := int(tty.Fd())

	// Save original terminal state for restoration
	originalState, err := term.GetState(fd)
	if err != nil {
		tty.Close()
		return fmt.Errorf("failed to get terminal state: %w", err)
	}
	r.originalState = originalState

	// Set terminal to raw mode
	// In raw mode, input is available byte-by-byte without line buffering
	_, err = term.MakeRaw(fd)
	if err != nil {
		tty.Close()
		return fmt.Errorf("failed to set raw mode: %w", err)
	}

	r.running = true

	// Start goroutine to read bytes from TTY
	go r.readLoop()

	return nil
}

// Stop gracefully stops the raw reader and restores terminal state.
func (r *RawInputReader) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}

	r.running = false

	// Signal stop to read loop
	close(r.stopChan)

	// Restore original terminal state before closing TTY
	if r.originalState != nil && r.tty != nil {
		fd := int(r.tty.Fd())
		if err := term.Restore(fd, r.originalState); err != nil {
			// Continue cleanup even if restore fails
			_ = err
		}
		r.originalState = nil
	}

	// Close TTY
	if r.tty != nil {
		if err := r.tty.Close(); err != nil {
			return fmt.Errorf("failed to close tty: %w", err)
		}
		r.tty = nil
	}

	// Close input channel
	close(r.inputChan)

	// Recreate channels for potential restart
	r.stopChan = make(chan struct{})
	r.inputChan = make(chan []byte, 100)

	return nil
}

// ReadBytes returns a channel of raw input bytes.
// Each []byte may contain a single character or a multi-byte sequence (escape codes, UTF-8, etc.)
func (r *RawInputReader) ReadBytes() <-chan []byte {
	return r.inputChan
}

// readLoop continuously reads bytes from TTY and handles Ctrl+B prefix detection.
func (r *RawInputReader) readLoop() {
	// Use a 1KB buffer to efficiently handle paste operations and escape sequences
	// This reduces system calls while still being responsive
	buf := make([]byte, 1024)

	// Defer panic recovery to ensure terminal state is restored
	defer func() {
		if rec := recover(); rec != nil {
			// If we panic, try to restore terminal
			if r.originalState != nil && r.tty != nil {
				fd := int(r.tty.Fd())
				_ = term.Restore(fd, r.originalState)
			}
		}
	}()

	for {
		select {
		case <-r.stopChan:
			return
		default:
			// Read from TTY with short timeout to check stopChan periodically
			n, err := r.tty.Read(buf)
			if err != nil {
				if err == io.EOF {
					return
				}
				// On error, continue reading (might be temporary)
				continue
			}

			if n > 0 {
				// Make a copy of the bytes read
				data := make([]byte, n)
				copy(data, buf[:n])

				// Handle Ctrl+B (0x02) prefix key detection
				if len(data) == 1 && data[0] == 0x02 {
					// Ctrl+B detected - wait briefly for next byte
					if handled := r.handlePrefixKey(); handled {
						continue // Don't forward Ctrl+B or Ctrl+B Esc
					}
					// If not handled as prefix, forward the Ctrl+B byte
				}

				// Send data to channel (non-blocking)
				select {
				case r.inputChan <- data:
				case <-r.stopChan:
					return
				default:
					// Channel full, skip this input (should rarely happen with buffer)
				}
			}
		}
	}
}

// handlePrefixKey handles Ctrl+B prefix key detection.
// Returns true if the sequence was handled (Ctrl+B followed by Esc to exit terminal mode),
// false if Ctrl+B should be forwarded to PTY.
func (r *RawInputReader) handlePrefixKey() bool {
	buf := make([]byte, 1)

	// Set read deadline for next byte (500ms window)
	deadline := time.Now().Add(500 * time.Millisecond)
	if err := r.tty.SetReadDeadline(deadline); err != nil {
		// Failed to set deadline, forward Ctrl+B
		r.inputChan <- []byte{0x02}
		return true
	}
	defer r.tty.SetReadDeadline(time.Time{}) // Clear deadline

	// Try to read next byte with deadline
	n, err := r.tty.Read(buf)

	// Check for timeout or error
	if err != nil {
		if os.IsTimeout(err) {
			// Timeout - forward Ctrl+B to PTY
			r.inputChan <- []byte{0x02}
			return true
		}
		// Other error - forward Ctrl+B
		r.inputChan <- []byte{0x02}
		return true
	}

	if n == 0 {
		// No data - forward Ctrl+B
		r.inputChan <- []byte{0x02}
		return true
	}

	// Check if next byte is Esc (0x1b)
	if buf[0] == 0x1b {
		// Ctrl+B Esc sequence detected - signal to exit terminal mode
		// Send special message (empty byte slice signals mode switch)
		r.inputChan <- []byte{}
		return true
	}

	// Forward both Ctrl+B and the next byte
	r.inputChan <- []byte{0x02}
	r.inputChan <- buf[:n]
	return true
}

// IsRunning returns whether the raw reader is currently active.
func (r *RawInputReader) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}
