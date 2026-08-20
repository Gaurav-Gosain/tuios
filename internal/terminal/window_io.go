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
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

const (
	// maxBatch caps how much pending PTY output one pass of outputWriter
	// coalesces before writing it to the emulator.
	maxBatch = 256 * 1024

	// maxVTChunk caps how much of that batch is written to the emulator under a
	// single acquisition of ioMu. It bounds how long the compositor can be kept
	// out of a pane by that pane's own output. It is a latency knob, not a
	// throughput one: the whole batch is still written before the next one is
	// read, so shrinking it costs only extra lock round trips and buys a
	// proportionally shorter worst-case stall for the renderer.
	maxVTChunk = 8 * 1024
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
// outputChunk is one queued batch of daemon output and the epoch it was queued
// under. See Window.outputEpoch.
type outputChunk struct {
	data  []byte
	epoch uint64
	// width and height, both set, mark the size the daemon's emulator took at
	// this point in the stream instead of bytes to write. Applying it here
	// rather than when the layout asked for it is what keeps this emulator
	// wrapping every line where the daemon wrapped it.
	width, height int
	// drained, when set, is closed once everything queued in front of it has
	// been applied to the emulator. See DrainPendingOutput.
	drained chan struct{}
}

func (c outputChunk) isResize() bool { return c.width > 0 && c.height > 0 }

// DiscardPendingOutput throws away output queued for the emulator but not yet
// applied to it.
//
// Unsubscribing from a pane stops the daemon sending, but bytes already queued
// here are older than any snapshot the pane is about to be restored from, and
// applying them afterwards paints them a second time. Bumping the epoch before
// the restore takes the I/O lock is what makes the drop cover a batch the writer
// is already holding as well as the ones still queued.
func (w *Window) DiscardPendingOutput() {
	w.outputEpoch.Add(1)
}

// applyStreamResize takes the emulator to the size the daemon's emulator took
// at this point in the stream. Dropped when the epoch has moved, because a
// restore has since put a whole snapshot in, and that snapshot's size is newer
// than this.
func (w *Window) applyStreamResize(chunk outputChunk) {
	w.ioMu.Lock()
	// Re-checked under the lock the restore also takes, for the same reason
	// the batch write below re-checks it.
	//
	// A resize to the size the emulator already has is not a no-op inside the
	// emulator: it resets the scroll region and the tab stops, which the guest
	// set and the daemon has not been told to forget. A subscribe states the
	// pane's size whether or not the client has it already, so this is the
	// common case rather than an odd one.
	if w.Terminal != nil && w.outputEpoch.Load() == chunk.epoch &&
		(w.Terminal.Width() != chunk.width || w.Terminal.Height() != chunk.height) {
		w.Terminal.Resize(chunk.width, chunk.height)
	}
	w.ioMu.Unlock()
	// Dirty flags belong to the UI goroutine; these two are what the write
	// path signals from here, and MarkTerminalsWithNewContent does the rest.
	w.HasNewOutput.Store(true)
	w.coalesceSignal.Store(true)
}

func (w *Window) outputWriter() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("window %s outputWriter panic: %v\n%s", w.ID, r, debug.Stack())
		}
	}()

	if w.outputDone == nil || w.outputChan == nil {
		return
	}

	batch := make([]byte, 0, maxBatch)

	for {
		var epoch uint64
		// A resize ends the batch in front of it and is applied after that
		// batch is written. Folding it into the bytes either side would lay
		// one of them out at a width the daemon never used them at.
		var resize outputChunk
		// A drain sentinel is answered whatever its epoch: it asks about queue
		// position, not about whether the bytes around it are still wanted.
		var drained chan struct{}
		select {
		case <-w.outputDone:
			return
		case chunk, ok := <-w.outputChan:
			if !ok {
				return
			}
			if chunk.drained != nil {
				close(chunk.drained)
				continue
			}
			if chunk.epoch != w.outputEpoch.Load() {
				continue
			}
			epoch = chunk.epoch
			if chunk.isResize() {
				w.applyStreamResize(chunk)
				continue
			}
			batch = append(batch[:0], chunk.data...)
		}

		for len(batch) < maxBatch {
			select {
			case more, ok := <-w.outputChan:
				if !ok {
					goto write
				}
				if more.drained != nil {
					drained = more.drained
					goto write
				}
				if more.epoch != epoch {
					continue
				}
				if more.isResize() {
					resize = more
					goto write
				}
				batch = append(batch, more.data...)
			default:
				goto write
			}
		}

	write:
		// Snapshot and dereference Terminal entirely under ioMu: Close()
		// nils the field under the same lock, so an unlocked check-then-use
		// would panic once teardown races an in-flight batch.
		//
		// Write the batch in bounded chunks, taking the lock once per chunk
		// rather than once for the whole batch. Terminal.Write parses the bytes
		// and mutates the cell buffer, and for a pane emitting thousands of
		// lines a second most of that cost is scrolling the buffer once per
		// line, so a full 256KiB batch holds the exclusive lock for tens of
		// milliseconds. The compositor must take the read side of this same
		// lock on every window on every frame, and sync.RWMutex parks readers
		// behind a queued writer. A pane whose outputChan always has more data
		// therefore re-queues the writer immediately and starves the renderer
		// indefinitely: frames stop, and the keystroke echo the user is waiting
		// for is never composited, however promptly the input itself was
		// delivered. Unlock releases the readers waiting at that moment, so
		// chunking hands the renderer a turn between chunks and bounds the
		// stall at one chunk's parse instead of one batch's.
		var t *vt.Emulator
		for off := 0; off < len(batch); off += maxVTChunk {
			end := min(off+maxVTChunk, len(batch))
			w.ioMu.Lock()
			t = w.Terminal
			// Re-checked under the lock the restore also takes, so a batch that
			// was already in hand when the epoch moved is dropped rather than
			// written over what the restore just put there.
			if w.outputEpoch.Load() != epoch {
				w.ioMu.Unlock()
				break
			}
			if t != nil {
				_, _ = t.Write(batch[off:end])
			}
			w.ioMu.Unlock()
			if t == nil {
				break
			}
		}

		if resize.isResize() {
			w.applyStreamResize(resize)
		}
		if drained != nil {
			close(drained)
		}

		if t != nil {
			// HasNewOutput drives the UI goroutine's dirty-marking;
			// coalesceSignal drives the render trigger. Do NOT mark the
			// window dirty here: Dirty/ContentDirty/CachedContent are
			// window model fields owned by the UI goroutine, and writing
			// them from this background goroutine races the renderer and
			// Close(). MarkTerminalsWithNewContent marks them on the UI
			// goroutine once the coalescer fires PTYDataChan.
			w.HasNewOutput.Store(true)
			w.coalesceSignal.Store(true)
			// Don't signal PTYDataChan here. The renderCoalescer
			// goroutine checks coalesceSignal at a capped rate and
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
			// Consume the coalescer's own flag, not HasNewOutput, so the
			// latter survives for the UI goroutine's MarkTerminalsWithNewContent.
			if w.coalesceSignal.CompareAndSwap(true, false) {
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

// terminalRef returns the emulator, or nil once Close() has taken it away.
//
// Close() owns w.Terminal: it closes the emulator and nils the field under
// ioMu, and nobody may observe the field after that without the same lock.
// Every reader outside Close() therefore goes through ioMu, and a goroutine
// that has to block on the emulator takes one reference here and then uses
// that reference rather than the field, so the lock is never held across a
// blocking call and cannot deadlock against Close()'s own wait for the I/O
// goroutines. A reference outliving Close() is safe: Terminal.Close() unblocks
// a pending Read with an error, so a late reader observes a closed emulator
// instead of a nil one. This mirrors the snapshot discipline the w.Pty readers
// already use.
func (w *Window) terminalRef() *vt.Emulator {
	w.ioMu.RLock()
	defer w.ioMu.RUnlock()
	return w.Terminal
}

// StartDaemonResponseReader starts a goroutine to read and DRAIN responses from
// the terminal emulator. We don't forward these to the PTY because:
//  1. Responses were appearing as visible escape sequences in the output
//  2. Applications in daemon mode receive queries from the daemon's VT emulator
//     and don't need responses from client emulators
//
// This must be called after the Terminal is set up. DaemonMode is set at
// construction and never written again, so reading it here needs no lock;
// w.Terminal does, and is read inside the goroutine via terminalRef.
func (w *Window) StartDaemonResponseReader() {
	if !w.DaemonMode {
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("window %s daemon response reader panic: %v\n%s", w.ID, r, debug.Stack())
			}
		}()

		// One reference, taken under the lock Close() writes the field under.
		// Reading w.Terminal unlocked raced Close() even as a single snapshot,
		// and re-reading it per iteration would dereference the nil Close()
		// leaves behind. Terminal.Close() unblocks the pending Read with an
		// error, so a reader holding a stale reference sees the emulator as
		// closed and exits promptly.
		term := w.terminalRef()
		if term == nil {
			return
		}
		buf := make([]byte, 4096)
		for {
			// Terminal.Read() blocks, so we can't use select here.
			// The goroutine will exit when Terminal is closed (returns error).
			_, err := term.Read(buf)
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
	// Snapshot and write under ioMu so Close() nilling w.Terminal cannot
	// slip between the check and the dereference.
	w.ioMu.Lock()
	t := w.Terminal
	if t != nil {
		_, _ = t.Write(data)
	}
	w.ioMu.Unlock()

	if t == nil {
		return
	}
	w.HasNewOutput.Store(true)
	if w.PTYDataChan != nil {
		select {
		case w.PTYDataChan <- struct{}{}:
		default:
		}
	}
	w.MarkContentDirty()
}

// WriteOutputAsync writes output data to the terminal emulator without blocking.
// Used in daemon mode to process PTY output received from the daemon.
// Data is queued to a channel and written in order by the outputWriter goroutine.
func (w *Window) WriteOutputAsync(data []byte) {
	// outputChan is set once at construction and never nilled, so reading it
	// here is safe. w.Terminal must NOT be read: Close() nils it under ioMu,
	// so an unlocked read would race teardown. The closed flag below is the
	// real lifecycle guard, and outputWriter drops the batch under ioMu if
	// Terminal is already gone.
	if w.outputChan == nil {
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
	chunk := outputChunk{data: dataCopy, epoch: w.outputEpoch.Load()}

	// Queue to channel - non-blocking with buffered channel
	select {
	case <-w.outputDone:
		// Writer goroutine has stopped, drop data
	case w.outputChan <- chunk:
		// Successfully queued
	default:
		// Channel full - drop data (shouldn't happen with large buffer)
	}
}

// DrainPendingOutput blocks until everything queued for the emulator ahead of
// this call has been applied to it. A pane about to be primed is unsubscribed,
// so the queue is finite and this returns once the writer works through it.
//
// It exists because throwing the queue away instead loses history: the
// snapshot that follows is newer than anything queued, but it carries a
// bounded scrollback window, and the queue of a pane that outpaced its client
// can hold far more scrollback than the snapshot brings back.
func (w *Window) DrainPendingOutput() {
	if w.outputChan == nil || w.closed.Load() {
		return
	}
	done := make(chan struct{})
	select {
	case w.outputChan <- outputChunk{drained: done}:
	case <-w.outputDone:
		return
	}
	select {
	case <-done:
	case <-w.outputDone:
	}
}

// ResizeFromStream queues the size the daemon's emulator took, to be applied in
// the order it arrived among the bytes around it. StreamOwnsSize says whether
// this is the pane's only route to a new grid size.
func (w *Window) ResizeFromStream(width, height int) {
	if w.outputChan == nil || w.closed.Load() || width <= 0 || height <= 0 {
		return
	}
	chunk := outputChunk{epoch: w.outputEpoch.Load(), width: width, height: height}
	select {
	case <-w.outputDone:
	case w.outputChan <- chunk:
	default:
	}
}

// ResizeEmulatorToSnapshot takes the emulator to the size a daemon snapshot was
// serialized at, whatever owns the size otherwise. A snapshot is a grid as much
// as it is contents, and the stream resumes at the position it was taken at, so
// everything that lands next is written against these bounds. It is the only
// way back for a pane whose emulator was left at another size, by a resize
// announced while it was hidden or by one the stream carried and a restore then
// discarded.
func (w *Window) ResizeEmulatorToSnapshot(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	w.ioMu.Lock()
	// Re-check under the lock; Close() nils Terminal while holding it.
	if w.Terminal != nil && (w.Terminal.Width() != width || w.Terminal.Height() != height) {
		w.Terminal.Resize(width, height)
	}
	w.ioMu.Unlock()
}

// SetStreamOwnsSize records whether this pane's emulator is sized by its daemon
// output stream. It is true exactly while a subscription is feeding the pane:
// the daemon then announces every size change at the byte it made it, and a
// layout that resized this emulator itself would lay out whatever the guest
// produced before the daemon heard at a width the daemon never used.
//
// A pane with no subscription has no stream to be ordered against, so it is
// sized by the layout as before, and by the snapshot when it is primed.
func (w *Window) SetStreamOwnsSize(v bool) { w.streamOwnsSize.Store(v) }

// StreamOwnsSize reports whether the daemon output stream sizes this emulator.
func (w *Window) StreamOwnsSize() bool { return w.streamOwnsSize.Load() }

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
				w.SetProcessExited(true)
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

		// Snapshot the PTY once under the lock. Close() nils w.Pty under
		// ioMu; re-reading the interface value each iteration without the
		// lock is a torn read (undefined behaviour) and calling Read on a
		// nilled interface panics. Pty.Close() unblocks the pending Read,
		// so the snapshot still tears down promptly. The sibling
		// Terminal->PTY goroutine below uses the same discipline.
		w.ioMu.RLock()
		pty := w.Pty
		w.ioMu.RUnlock()
		if pty == nil {
			return
		}
		for {
			select {
			case <-ctx.Done():
				// Context cancelled, exit gracefully
				return
			default:
				n, err := pty.Read(buf)
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
				terminal := w.terminalRef()
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
							time.Now().Format("15:04:05.000"), shortID(w.ID), len(data), string(data), data)
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

					// Write to PTY. Snapshot the handle under the read lock
					// and write outside it: Pty.Write blocks on a full kernel
					// input buffer, and holding RLockIO across that block
					// starves the renderer behind the PTY reader's queued
					// LockIO. See SendInput for the full reasoning.
					w.ioMu.RLock()
					pty := w.Pty
					w.ioMu.RUnlock()
					if pty != nil {
						if _, err := pty.Write(data); err != nil {
							// Ignore write errors during I/O operations
							_ = err
						}
					}
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
			time.Now().Format("15:04:05.000"), shortID(w.ID), len(input), string(input), input)
		if f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			_, _ = f.WriteString(debugMsg)
			_ = f.Close()
		}
	}

	// Local mode - write directly to PTY.
	//
	// Snapshot the PTY under the read lock and write OUTSIDE it. Pty.Write
	// blocks once the kernel input buffer fills (a guest that is not reading
	// stdin, e.g. a paste into a stopped process), and holding the read lock
	// across that block wedges the whole UI: the render path cannot take its
	// own read side, because a queued LockIO writer (the PTY reader, which
	// runs constantly) starves all later readers on a sync.RWMutex.
	//
	// Snapshotting is what makes this safe: Close() nils w.Pty under the
	// exclusive lock, so an unlocked field read would be a torn read of an
	// interface value and a nil dereference. The snapshot keeps the handle
	// alive for the call; a concurrent Close() closes the descriptor and the
	// write just returns an error, which is the same discipline the PTY
	// reader and Terminal->PTY goroutines already use.
	w.ioMu.RLock()
	pty := w.Pty
	w.ioMu.RUnlock()

	if pty == nil {
		return fmt.Errorf("no PTY available")
	}

	n, err := pty.Write(input)
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
	// CompareAndSwap makes Close idempotent: a second (or concurrent) call
	// returns early instead of double-closing outputDone below.
	if !w.closed.CompareAndSwap(false, true) {
		return
	}

	// Disable terminal features before closing
	w.disableTerminalFeatures()

	// Stop daemon output writer goroutine if running. outputChan has an
	// external sender (WriteOutputAsync), so it is never closed; closing
	// outputDone stops outputWriter and renderCoalescer, which select on it.
	// The field is deliberately NOT nilled: both goroutines re-read it on
	// every select iteration, and a nil channel blocks forever (leaking the
	// goroutines, the 8ms ticker, and the whole Window/Emulator). The CAS
	// above guarantees this close runs exactly once.
	if w.outputDone != nil {
		close(w.outputDone)
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

	// Kill the process. w.Cmd is deliberately left set: the process-monitor
	// goroutine reads it unlocked for as long as it lives, and nilling it here
	// raced that read. Nothing treats a nil Cmd as "closed", and the exec.Cmd
	// dies with the Window anyway, so leaving it costs nothing.
	if w.Cmd != nil && w.Cmd.Process != nil {
		_ = w.Cmd.Process.Kill()
		w.waitForCmd()
	}

	// Clear caches to free memory
	w.CachedContent = ""
	w.CachedLayer = nil

	// Clear copy mode to free memory
	if w.CopyMode != nil {
		w.CopyMode.SearchMatches = nil
		w.CopyMode.SearchCache.Matches = nil
		w.CopyMode = nil
	}
}
