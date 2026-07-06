package terminal

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/pool"
)

// outputWriter is a goroutine that serializes writes to the terminal emulator.
// It batches pending chunks into capped VT writes and coalesces render
// signals to prevent partial-frame flickering.
//
// The anti-flicker mechanism: instead of signaling a re-render on every
// VT write (which shows incomplete frames mid-sync-update), we defer the
// signal. A separate renderCoalescer goroutine fires at a capped rate
// (~120fps) and only signals when there's actually new output. This is
// the same technique prise uses (8ms render timer) to eliminate flicker
// from fast-updating TUIs.
func (w *Window) outputWriter() {
	if w.outputDone == nil || w.outputChan == nil {
		return
	}

	const maxBatch = 256 * 1024
	batch := make([]byte, 0, maxBatch)

	for {
		select {
		case <-w.outputDone:
			return
		case data, ok := <-w.outputChan:
			if !ok {
				return
			}
			batch = append(batch[:0], data...)
		}

		for len(batch) < maxBatch {
			select {
			case more, ok := <-w.outputChan:
				if !ok {
					goto write
				}
				batch = append(batch, more...)
			default:
				goto write
			}
		}

	write:
		if w.Terminal != nil {
			w.ioMu.Lock()
			_, _ = w.Terminal.Write(batch)
			w.ioMu.Unlock()

			w.HasNewOutput.Store(true)
			w.MarkContentDirty()
			// Don't signal PTYDataChan here. The renderCoalescer
			// goroutine checks HasNewOutput at a capped rate and
			// signals then. This prevents partial-frame renders.
		}
	}
}

// renderCoalescer runs for daemon mode windows and fires render signals
// at a capped rate. Multiple VT writes between ticks coalesce into a
// single render that shows the latest complete frame.
func (w *Window) renderCoalescer() {
	const interval = 8 * time.Millisecond // ~120fps cap
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.outputDone:
			return
		case <-ticker.C:
			if w.HasNewOutput.CompareAndSwap(true, false) {
				if w.PTYDataChan != nil {
					select {
					case w.PTYDataChan <- struct{}{}:
					default:
					}
				}
			}
		}
	}
}

// StartDaemonResponseReader starts a goroutine to read and DRAIN responses from
// the terminal emulator. We don't forward these to the PTY because:
//  1. Responses were appearing as visible escape sequences in the output
//  2. Applications in daemon mode receive queries from the daemon's VT emulator
//     and don't need responses from client emulators
//
// This must be called after the Terminal is set up.
func (w *Window) StartDaemonResponseReader() {
	if !w.DaemonMode || w.Terminal == nil {
		return
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			// Terminal.Read() blocks, so we can't use select here.
			// The goroutine will exit when Terminal is closed (returns error).
			_, err := w.Terminal.Read(buf)
			if err != nil {
				return
			}
			// Drain responses - don't send to PTY to avoid escape sequence leaks
		}
	}()
}

// WriteOutput writes output data to the terminal emulator.
// Used in daemon mode to process PTY output received from the daemon.
func (w *Window) WriteOutput(data []byte) {
	if w.Terminal != nil {
		w.HasNewOutput.Store(true)
		if w.PTYDataChan != nil {
			select {
			case w.PTYDataChan <- struct{}{}:
			default:
			}
		}
		w.ioMu.Lock()
		_, _ = w.Terminal.Write(data)
		w.ioMu.Unlock()
		w.MarkContentDirty()
	}
}

// WriteOutputAsync writes output data to the terminal emulator without blocking.
// Used in daemon mode to process PTY output received from the daemon.
// Data is queued to a channel and written in order by the outputWriter goroutine.
func (w *Window) WriteOutputAsync(data []byte) {
	if w.Terminal == nil || w.outputChan == nil {
		return
	}
	// Close() runs on the UI goroutine while this runs on the daemon readLoop
	// goroutine. outputChan is never closed (only outputDone is), so the send
	// below cannot panic; the closed flag and the outputDone case just stop
	// queuing into a channel whose reader is already gone.
	if w.closed.Load() {
		return
	}
	// Copy data since the caller's buffer may be reused
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	// Queue to channel - non-blocking with buffered channel
	select {
	case <-w.outputDone:
		// Writer goroutine has stopped, drop data
	case w.outputChan <- dataCopy:
		// Successfully queued
	default:
		// Channel full - drop data (shouldn't happen with large buffer)
	}
}

func (w *Window) handleIOOperations() {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancelFunc = cancel

	// PTY to Terminal copy (output from shell) - with proper context handling
	w.ioWg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("window %s goroutine panic: %v\n%s", w.ID, r, debug.Stack())
				// A panic here leaves a zombie pane: the reader is dead so
				// the window no longer renders, but nothing marks it for
				// cleanup. Mirror the normal process-exit path (see the
				// monitor goroutine in NewWindow) by marking ProcessExited so
				// the maintenance tick in Update removes the window via
				// DeleteWindow, and signal PTYDataChan so cleanup happens
				// promptly instead of on the next poll.
				w.ProcessExited = true
				if w.PTYDataChan != nil {
					select {
					case w.PTYDataChan <- struct{}{}:
					default:
					}
				}
			}
		}()

		// Signal bubbletea when PTY reader exits so the tick handler
		// can detect ProcessExited and close the window promptly.
		defer func() {
			if w.PTYDataChan != nil {
				select {
				case w.PTYDataChan <- struct{}{}:
				default:
				}
			}
		}()

		// Get buffer from pool for better memory management
		bufPtr := pool.GetByteSlice()
		buf := *bufPtr
		defer pool.PutByteSlice(bufPtr)
		for {
			select {
			case <-ctx.Done():
				// Context cancelled, exit gracefully
				return
			default:
				// Set a reasonable timeout for read operations
				if w.Pty == nil {
					return
				}

				n, err := w.Pty.Read(buf)
				if err != nil {
					if err != io.EOF && !strings.Contains(err.Error(), "file already closed") &&
						!strings.Contains(err.Error(), "input/output error") {
						// Log unexpected errors for debugging
						_ = err
					}
					return
				}
				if n > 0 {
					w.HasNewOutput.Store(true)

					// Signal bubbletea that PTY data arrived (non-blocking, coalesces rapid updates)
					if w.PTYDataChan != nil {
						select {
						case w.PTYDataChan <- struct{}{}:
						default:
						}
					}

					// Debug: Log all data from PTY (applications sending queries)
					if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
						if len(buf[:n]) >= 2 && buf[0] == '\x1b' {
							debugMsg := fmt.Sprintf("[%s] PTY->Terminal query: %q (hex: % x)\n",
								time.Now().Format("15:04:05.000"), string(buf[:n]), buf[:n])
							if f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
								_, _ = f.WriteString(debugMsg)
								_ = f.Close()
							}
						}
					}

					// Pass through cursor style sequences to parent terminal
					// The VT emulator absorbs DECSCUSR, so we re-emit them
					passThroughCursorStyle(buf[:n])

					// Terminal.Write mutates the cell buffer, so it needs the
					// exclusive lock, not the shared read lock the renderer uses
					// (two RLock holders do not exclude each other).
					w.ioMu.Lock()
					if w.Terminal != nil {
						_, _ = w.Terminal.Write(buf[:n])
					}
					w.ioMu.Unlock()
				}
			}
		}
	})

	// Terminal to PTY copy (input to shell) - with proper context handling
	w.ioWg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("window %s goroutine panic: %v\n%s", w.ID, r, debug.Stack())
			}
		}()

		// Use a smaller buffer for terminal-to-PTY operations
		buf := make([]byte, 4096)
		for {
			select {
			case <-ctx.Done():
				// Context cancelled, exit gracefully
				return
			default:
				// Set a reasonable timeout for read operations
				// Use lock to synchronize with Close() which may set w.Terminal = nil
				w.ioMu.RLock()
				terminal := w.Terminal
				w.ioMu.RUnlock()

				if terminal == nil {
					return
				}

				n, err := terminal.Read(buf)
				if err != nil {
					if err != io.EOF && !strings.Contains(err.Error(), "file already closed") &&
						!strings.Contains(err.Error(), "input/output error") {
						// Log unexpected errors for debugging
						_ = err
					}
					return
				}
				if n > 0 {
					data := buf[:n]

					// Debug: Log ALL data from terminal response pipe when debug mode is enabled
					if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
						debugMsg := fmt.Sprintf("[%s] Terminal->PTY [%s] ALL data (%d bytes): %q (hex: % x)\n",
							time.Now().Format("15:04:05.000"), w.ID[:8], len(data), string(data), data)
						if f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
							_, _ = f.WriteString(debugMsg)
							_ = f.Close()
						}
					}

					// Fix incorrect CPR responses from VT library for nushell compatibility
					// The VT library responds to ESC[6n queries but returns stale/incorrect cursor positions
					// This causes nushell to incorrectly clear the screen thinking it's at the wrong position
					// We detect CPR responses (ESC[{row};{col}R) and replace with actual cursor position
					if len(data) >= 6 && data[0] == '\x1b' && data[1] == '[' && data[len(data)-1] == 'R' {
						// This looks like a CPR response, check if it contains semicolon
						if bytes.Contains(data, []byte(";")) {
							w.ioMu.RLock()
							if w.Terminal != nil {
								pos := w.Terminal.CursorPosition()
								// Get the actual current cursor position (1-indexed for terminal protocol)
								actualY := pos.Y + 1
								actualX := pos.X + 1
								// Replace with corrected cursor position
								data = fmt.Appendf(nil, "\x1b[%d;%dR", actualY, actualX)
							}
							w.ioMu.RUnlock()
						}
					}

					// Debug: Log XTWINOPS responses when debug mode is enabled
					if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
						if len(data) >= 6 && data[0] == '\x1b' && data[1] == '[' && data[len(data)-1] == 't' {
							// This looks like an XTWINOPS response
							debugMsg := fmt.Sprintf("[%s] XTWINOPS response to PTY: %q (hex: % x)\n",
								time.Now().Format("15:04:05.000"), string(data), data)
							// Append to debug log file
							if f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
								_, _ = f.WriteString(debugMsg)
								_ = f.Close()
							}
						}
					}

					// Write to PTY
					w.ioMu.RLock()
					if w.Pty != nil {
						if _, err := w.Pty.Write(data); err != nil {
							// Ignore write errors during I/O operations
							_ = err
						}
					}
					w.ioMu.RUnlock()
				}
			}
		}
	})
}

// SendInput sends input to the window's terminal with enhanced error handling.
func (w *Window) SendInput(input []byte) error {
	if w == nil {
		return fmt.Errorf("window is nil")
	}

	if len(input) == 0 {
		return nil // Nothing to send
	}

	// In daemon mode, use the callback to send input to daemon PTY
	if w.DaemonMode {
		if w.DaemonWriteFunc == nil {
			// Debug: this might be why input fails
			if f, _ := os.OpenFile("/tmp/tuios-input-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); f != nil {
				_, _ = fmt.Fprintf(f, "[%s] SendInput: DaemonWriteFunc is nil! PTYID=%s\n",
					time.Now().Format("15:04:05.000"), w.PTYID)
				_ = f.Close()
			}
			return fmt.Errorf("daemon write function not set")
		}
		return w.DaemonWriteFunc(input)
	}

	// Debug: Log all SendInput calls when debug mode is enabled
	if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
		debugMsg := fmt.Sprintf("[%s] SendInput [%s] (%d bytes): %q (hex: % x)\n",
			time.Now().Format("15:04:05.000"), w.ID[:8], len(input), string(input), input)
		if f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			_, _ = f.WriteString(debugMsg)
			_ = f.Close()
		}
	}

	// Local mode - write directly to PTY
	w.ioMu.RLock()
	defer w.ioMu.RUnlock()

	if w.Pty == nil {
		return fmt.Errorf("no PTY available")
	}

	n, err := w.Pty.Write(input)
	if err != nil {
		return fmt.Errorf("failed to write to PTY: %w", err)
	}

	if n != len(input) {
		return fmt.Errorf("partial write to PTY: wrote %d of %d bytes", n, len(input))
	}

	// Only mark as dirty - don't clear cache here for better input performance
	// Cache will be invalidated during render if content actually changed
	w.Dirty = true
	w.ContentDirty = true

	return nil
}

// waitForCmd waits for the command to exit, ensuring Wait() is only called once.
// This prevents race conditions when both the process monitor goroutine and Close()
// try to wait for the process.
func (w *Window) waitForCmd() {
	if w == nil || w.Cmd == nil {
		return
	}
	w.cmdWaitOnce.Do(func() {
		_ = w.Cmd.Wait() // Best effort, ignore error
	})
}

// Close closes the window and cleans up resources.
func (w *Window) Close() {
	// Nil safety check
	if w == nil {
		return
	}

	// Mark closed before touching outputChan so the external sender
	// (WriteOutputAsync on the daemon readLoop goroutine) stops queuing.
	w.closed.Store(true)

	// Disable terminal features before closing
	w.disableTerminalFeatures()

	// Stop daemon output writer goroutine if running. outputChan has an
	// external sender (WriteOutputAsync), so it is never closed; closing
	// outputDone stops outputWriter, which selects on it.
	if w.outputDone != nil {
		close(w.outputDone)
		w.outputDone = nil
	}

	// Cancel all goroutines first
	if w.cancelFunc != nil {
		w.cancelFunc()
		w.cancelFunc = nil
	}

	// Close PTY and Terminal to unblock I/O goroutines
	// Must close both because:
	// - PTY close unblocks the PTY->Terminal goroutine
	// - Terminal close unblocks the Terminal->PTY goroutine (reads from emulator response pipe)
	w.ioMu.Lock()
	if w.Pty != nil {
		_ = w.Pty.Close()
		w.Pty = nil
	}
	if w.Terminal != nil {
		_ = w.Terminal.Close()
		w.Terminal = nil
	}
	w.ioMu.Unlock()

	// Wait briefly for I/O goroutines (they should exit fast after PTY/Terminal close)
	done := make(chan struct{})
	go func() {
		w.ioWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Millisecond):
	}

	// Kill the process
	if w.Cmd != nil && w.Cmd.Process != nil {
		_ = w.Cmd.Process.Kill()
		w.waitForCmd()
		w.Cmd = nil
	}

	// Clear caches to free memory
	w.CachedContent = ""
	w.CachedLayer = nil
	w.SelectedText = ""

	// Clear copy mode to free memory
	if w.CopyMode != nil {
		w.CopyMode.SearchMatches = nil
		w.CopyMode.SearchCache.Matches = nil
		w.CopyMode = nil
	}
}
