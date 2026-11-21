package terminal

import (
	"context"
	"fmt"
	"io"
	"log"
	"runtime/debug"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/crashexit"
	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/memtrim"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"tuios.dev/vt"
)

var debugInternal = debuglog.Enabled

func debugLogf(format string, v ...any) {
	if !debugInternal() {
		return
	}
	f, err := debuglog.Open(debuglog.Path)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(f, format, v...)
	_ = f.Close()
}

func debugLine(format string, v ...any) {
	debugLogf(format+"\n", v...)
}

const (
	maxBatch = 256 * 1024

	maxVTChunk = 8 * 1024

	outputBatchKeep = 32 * 1024

	queueRoomPoll = time.Millisecond
)

var maxQueuedBytes int64 = 16 << 20

type outputChunk struct {
	data          []byte
	epoch         uint64
	width, height int
	drained       chan struct{}
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

func (w *Window) applyStreamResize(chunk outputChunk) {
	w.writesBegun.Add(1)
	w.ioMu.Lock()
	if w.Terminal != nil && w.outputEpoch.Load() == chunk.epoch &&
		(w.Terminal.Width() != chunk.width || w.Terminal.Height() != chunk.height) {
		w.Terminal.Resize(chunk.width, chunk.height)
	}
	w.ioMu.Unlock()
	w.noteWrite(false)
}

func (w *Window) noteWrite(graphics bool) {
	if graphics {
		w.HasGraphicsOutput.Store(true)
	} else {
		w.HasNewOutput.Store(true)
	}
	w.writesNoted.Add(1)
	w.wakeCoalescer()
}

// EmulatorWrites counts the changes the pane's writers made to the emulator:
// begun is incremented before each change, and noted after the change set its
// output flag. A reader that reads noted before it takes HasNewOutput, and
// later reads begun equal to that, knows the emulator holds nothing the
// flag it took did not cover. Any other answer means a change may be in the
// emulator that no pass has taken in yet. Safe from any goroutine.
func (w *Window) EmulatorWrites() (begun, noted uint64) {
	return w.writesBegun.Load(), w.writesNoted.Load()
}

// NoteRedraw asks for the pane to be drawn again although the program wrote
// nothing: something drawn from its cells, a picture shown as glyphs that
// waited for its drawing budget, is ready now. Safe from any goroutine.
func (w *Window) NoteRedraw() { w.noteOutput() }

func (w *Window) noteOutput() {
	w.HasNewOutput.Store(true)
	w.wakeCoalescer()
}

func (w *Window) wakeCoalescer() {
	w.coalesceSignal.Store(true)
	if w.coalesceWake != nil {
		select {
		case w.coalesceWake <- struct{}{}:
		default:
		}
	}
}

func (w *Window) outputWriter() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("window %s outputWriter panic: %v\n%s", w.ID, r, debug.Stack())
		}
	}()

	if w.outputDone == nil || w.outputQ == nil {
		return
	}

	var batch []byte
	var trim *time.Timer
	defer func() {
		if trim != nil {
			trim.Stop()
		}
	}()

	for {
		var epoch uint64
		var resize outputChunk
		var drained chan struct{}
		select {
		case <-w.outputDone:
			return
		default:
		}
		chunk, ok, closed := w.outputQ.pop()
		if !ok {
			if closed {
				return
			}
			var trimC <-chan time.Time
			if w.outputQ.oversized() || cap(batch) > outputBatchKeep {
				if trim == nil {
					trim = time.NewTimer(outputQueueTrimAfter)
				} else {
					trim.Reset(outputQueueTrimAfter)
				}
				trimC = trim.C
			}
			select {
			case <-w.outputDone:
				return
			case <-w.outputQ.wake():
			case <-trimC:
				w.outputQ.trim()
				if cap(batch) > outputBatchKeep {
					batch = nil
				}
			}
			continue
		}
		w.queuedBytes.Add(-int64(len(chunk.data)))
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

		for len(batch) < maxBatch {
			select {
			case <-w.outputDone:
				return
			default:
			}
			more, ok, _ := w.outputQ.pop()
			if !ok {
				goto write
			}
			w.queuedBytes.Add(-int64(len(more.data)))
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
		}

	write:
		var t vt.Terminal
		ground := w.parserAtGround()
		data := batch
		if f := w.modesOnly.Load(); f != nil {
			data = f.filter(batch)
		}
		w.writesBegun.Add(1)
		for off := 0; off < len(data) || off == 0; off += maxVTChunk {
			end := min(off+maxVTChunk, len(data))
			w.ioMu.Lock()
			t = w.Terminal
			if w.outputEpoch.Load() != epoch {
				w.ioMu.Unlock()
				break
			}
			if t != nil && end > off {
				_, _ = t.Write(data[off:end])
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
			w.noteWrite(!resize.isResize() && graphicsOnly(ground, batch) && w.cursorHidden())
		} else {
			w.writesNoted.Add(1)
		}
	}
}

// SetFrameInterval sets the shortest interval between two render signals from
// this pane: one frame at the frame rate of the client that owns it. A value
// of zero or less restores the default, minCoalesceInterval.
//
// A constant would ignore max_fps. At 8 ms a pane can never be drawn more
// than 125 times a second, so max_fps 240 would draw 125 frames of an
// animating pane, and a guest that draws at exactly 120 would race a floor
// 0.33 ms shorter than its own period.
// It is kept per pane because a server holds one client per connection, each
// with its own panes and its own max_fps.
func (w *Window) SetFrameInterval(d time.Duration) {
	if d <= 0 {
		d = minCoalesceInterval
	}
	w.frameInterval.Store(int64(d))
}

func (w *Window) minFrameInterval() time.Duration {
	if d := w.frameInterval.Load(); d > 0 {
		return time.Duration(d)
	}
	return minCoalesceInterval
}

const (
	minCoalesceInterval = 8 * time.Millisecond

	maxCoalesceInterval = 96 * time.Millisecond

	catchUpBacklog = 4 << 20

	catchUpFrames = 2

	coalescePaceFactor = 2
)

func (w *Window) catchUpCoalesceInterval() time.Duration { return catchUpFrames * w.minFrameInterval() }

func (w *Window) coalesceInterval() time.Duration {
	cost := time.Duration(w.renderCostNanos.Load()) * coalescePaceFactor
	interval := min(max(cost, w.minFrameInterval()), maxCoalesceInterval)
	if w.queuedBytes.Load() >= catchUpBacklog {
		return max(interval, w.catchUpCoalesceInterval())
	}
	return interval
}

// ChargeRenderCost records what the client's last composed frame cost, so the
// coalescer can pace itself against it. Only a real compose may be charged: a
// frame served from the view cache costs nothing and would reset the pace to
// the floor just as the expensive frames need it raised.
func (w *Window) ChargeRenderCost(d time.Duration) {
	if d < 0 {
		return
	}
	w.renderCostNanos.Store(int64(d))
}

func (w *Window) renderCoalescer() {
	defer crashexit.Recover()
	timer := time.NewTimer(w.minFrameInterval())
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	var armed bool
	var last time.Time

	emit := func(now time.Time, interval time.Duration) {
		if !w.coalesceSignal.CompareAndSwap(true, false) {
			return
		}
		last = NextFrameTime(last, interval, now)
		if w.PTYDataChan != nil {
			select {
			case w.PTYDataChan <- struct{}{}:
			default:
			}
		}
	}

	for {
		select {
		case <-w.outputDone:
			return

		case <-w.coalesceWake:
			if armed {
				continue
			}
			now := time.Now()
			interval := w.coalesceInterval()
			if wait := last.Add(interval).Sub(now); wait > FrameSlack(interval) {
				timer.Reset(wait - FrameSlack(interval))
				armed = true
				continue
			}
			emit(now, interval)

		case <-timer.C:
			armed = false
			emit(time.Now(), w.coalesceInterval())
		}
	}
}

// FrameSlack is how long before the end of an interval a frame may be drawn:
// a quarter of the interval. See renderCoalescer and NextFrameTime.
//
// A quarter is wide enough that a guest drawing at the frame rate, whose
// frames arrive with a jitter of a millisecond or so, is never held back, and
// that a timer firing late still lands inside it. Two frames can then be as
// little as three quarters of an interval apart, but never more of them than
// the frame rate allows over time.
func FrameSlack(interval time.Duration) time.Duration { return interval / 4 }

// NextFrameTime is the time a frame drawn at now stands for, when the last one
// stood for last and frames are spaced interval apart: the end of the
// interval for a frame drawn before it, and now for one drawn after. This is
// the generic cell rate algorithm with FrameSlack as its tolerance: frames
// keep to the interval on average, an early one does not move the frames after
// it earlier, and a late one moves them later, so they follow a guest that
// draws at the frame rate whatever its phase.
func NextFrameTime(last time.Time, interval time.Duration, now time.Time) time.Time {
	if end := last.Add(interval); now.Before(end) {
		return end
	}
	return now
}

func (w *Window) parserAtGround() bool {
	w.ioMu.RLock()
	defer w.ioMu.RUnlock()
	g, ok := w.Terminal.(interface{ AtGround() bool })
	return ok && g.AtGround()
}

func (w *Window) cursorHidden() bool {
	w.ioMu.RLock()
	defer w.ioMu.RUnlock()
	return w.Terminal != nil && w.Terminal.IsCursorHidden()
}

func (w *Window) terminalRef() vt.Terminal {
	w.ioMu.RLock()
	defer w.ioMu.RUnlock()
	return w.Terminal
}

// FocusReportingOn reports whether the guest requested DECSET 1004.
func (w *Window) FocusReportingOn() bool {
	term := w.terminalRef()
	return term != nil && term.FocusReportingEnabled()
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

		term := w.terminalRef()
		if term == nil {
			return
		}
		buf := make([]byte, 4096)
		for {
			_, err := term.Read(buf)
			if err != nil {
				return
			}
		}
	}()
}

// WriteOutput writes output data to the terminal emulator.
// Used in daemon mode to process PTY output received from the daemon.
func (w *Window) WriteOutput(data []byte) {
	w.writesBegun.Add(1)
	w.ioMu.Lock()
	t := w.Terminal
	if t != nil {
		_, _ = t.Write(data)
	}
	w.ioMu.Unlock()

	if t == nil {
		w.writesNoted.Add(1)
		return
	}
	w.HasNewOutput.Store(true)
	w.writesNoted.Add(1)
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
// Data is queued and written in order by the outputWriter goroutine.
//
// The window takes ownership of data: the caller must not write to it after
// the call, because the bytes are read later on the writer goroutine.
func (w *Window) WriteOutputAsync(data []byte) {
	if w.outputQ == nil {
		return
	}
	if w.closed.Load() {
		return
	}
	chunk := outputChunk{data: data, epoch: w.outputEpoch.Load()}

	if !w.waitForQueueRoom(int64(len(data))) {
		return
	}
	if w.outputQ.push(chunk) {
		w.queuedBytes.Add(int64(len(data)))
	}
}

func (w *Window) waitForQueueRoom(n int64) bool {
	if w.queuedBytes.Load()+n <= maxQueuedBytes {
		return true
	}
	timer := time.NewTimer(queueRoomPoll)
	defer timer.Stop()
	for w.queuedBytes.Load()+n > maxQueuedBytes {
		if w.closed.Load() {
			return false
		}
		select {
		case <-w.outputDone:
			return false
		case <-timer.C:
			timer.Reset(queueRoomPoll)
		}
	}
	return true
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
	if w.outputQ == nil || w.closed.Load() {
		return
	}
	done := make(chan struct{})
	for !w.outputQ.push(outputChunk{drained: done}) {
		select {
		case <-w.outputDone:
			return
		case <-time.After(queueRoomPoll):
		}
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
	if w.outputQ == nil || w.closed.Load() || width <= 0 || height <= 0 {
		return
	}
	w.outputQ.push(outputChunk{epoch: w.outputEpoch.Load(), width: width, height: height})
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

	w.ioWg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("window %s goroutine panic: %v\n%s", w.ID, r, debug.Stack())
				w.SetProcessExited(true)
				if w.PTYDataChan != nil {
					select {
					case w.PTYDataChan <- struct{}{}:
					default:
					}
				}
			}
		}()

		defer func() {
			if w.PTYDataChan != nil {
				select {
				case w.PTYDataChan <- struct{}{}:
				default:
				}
			}
		}()

		bufPtr := pool.GetByteSlice()
		buf := *bufPtr
		defer pool.PutByteSlice(bufPtr)

		w.ioMu.RLock()
		pty := w.Pty
		w.ioMu.RUnlock()
		if pty == nil {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := pty.Read(buf)
				if err != nil {
					if err != io.EOF && !strings.Contains(err.Error(), "file already closed") &&
						!strings.Contains(err.Error(), "input/output error") {
						_ = err
					}
					return
				}
				if n > 0 {
					if n >= 2 && buf[0] == '\x1b' && debugInternal() {
						debugLogf("[%s] PTY->Terminal query: %q (hex: % x)\n",
							time.Now().Format("15:04:05.000"), string(buf[:n]), buf[:n])
					}

					w.writesBegun.Add(1)
					w.ioMu.Lock()
					if w.Terminal != nil {
						_, _ = w.Terminal.Write(buf[:n])
						on := w.Terminal.FocusReportingEnabled()
						if on && !w.focusReporting && w.hasFocus.Load() {
							w.Terminal.WriteResponse([]byte("\x1b[I"))
						}
						w.focusReporting = on
					}
					w.ioMu.Unlock()

					w.noteWrite(false)
				}
			}
		}
	})

	w.ioWg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("window %s goroutine panic: %v\n%s", w.ID, r, debug.Stack())
			}
		}()

		buf := make([]byte, 4096)
		for {
			select {
			case <-ctx.Done():
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
						_ = err
					}
					return
				}
				if n > 0 {
					data := buf[:n]

					if debugInternal() {
						debugLogf("[%s] Terminal->PTY [%s] ALL data (%d bytes): %q (hex: % x)\n",
							time.Now().Format("15:04:05.000"), shortID(w.ID), len(data), string(data), data)

						if len(data) >= 6 && data[0] == '\x1b' && data[1] == '[' && data[len(data)-1] == 't' {
							debugLogf("[%s] XTWINOPS response to PTY: %q (hex: % x)\n",
								time.Now().Format("15:04:05.000"), string(data), data)
						}
					}

					w.ioMu.RLock()
					pty := w.Pty
					w.ioMu.RUnlock()
					if pty != nil {
						if _, err := pty.Write(data); err != nil {
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
		return errfmt.Errorf("window is nil")
	}

	if len(input) == 0 {
		return nil
	}

	if w.DaemonMode {
		if w.DaemonWriteFunc == nil {
			if debugInternal() {
				debugLogf("[%s] SendInput: DaemonWriteFunc is nil! PTYID=%s\n",
					time.Now().Format("15:04:05.000"), w.PTYID)
			}
			return errfmt.Errorf("daemon write function not set")
		}
		return w.DaemonWriteFunc(input)
	}

	if debugInternal() {
		debugLogf("[%s] SendInput [%s] (%d bytes): %q (hex: % x)\n",
			time.Now().Format("15:04:05.000"), shortID(w.ID), len(input), string(input), input)
	}

	w.ioMu.RLock()
	pty := w.Pty
	w.ioMu.RUnlock()

	if pty == nil {
		return errfmt.Errorf("no PTY available")
	}

	n, err := pty.Write(input)
	if err != nil {
		return errfmt.Errorf("failed to write to PTY: %w", err)
	}

	if n != len(input) {
		return errfmt.Errorf("partial write to PTY: wrote %d of %d bytes", n, len(input))
	}

	w.Dirty = true
	w.ContentDirty = true

	return nil
}

func (w *Window) waitForCmd() {
	if w == nil || w.Cmd == nil {
		return
	}
	w.cmdWaitOnce.Do(func() {
		_ = waitProcess(w)
	})
}

// Close closes the window and cleans up resources.
func (w *Window) Close() {
	if w == nil {
		return
	}

	if !w.closed.CompareAndSwap(false, true) {
		return
	}

	w.disableTerminalFeatures()

	if w.outputDone != nil {
		close(w.outputDone)
	}
	if w.outputQ != nil {
		w.outputQ.close()
	}

	if w.cancelFunc != nil {
		w.cancelFunc()
		w.cancelFunc = nil
	}

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

	done := make(chan struct{})
	go func() {
		w.ioWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Millisecond):
	}

	if w.Cmd != nil && w.Cmd.Process != nil {
		_ = w.Cmd.Process.Kill()
		w.waitForCmd()
	}

	w.CachedContent = ""
	w.CachedContentCols, w.CachedContentRows = 0, 0
	w.SyncHoldContent = ""
	w.CachedLayer = nil

	if w.CopyMode != nil {
		w.CopyMode.SearchMatches = nil
		w.CopyMode.SearchCache.Matches = nil
		w.CopyMode = nil
	}

	memtrim.Request()
}
