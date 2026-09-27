package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
)

// Resizes of the real PTY are coalesced. Every write of a new window size is a
// SIGWINCH for the guest, and a pane gets bursts of them: each attached client
// announces the layout it drew, so a new pane is resized several times in its
// first milliseconds, and a border drag resizes it on every mouse motion.
//
// Most guests only repaint more often than they need to. macOS /bin/bash (bash
// 3.2) dies: its SIGWINCH handler calls malloc, and a signal that lands while
// the shell is already inside malloc, which it is while readline sets LINES and
// COLUMNS at startup, deadlocks it on its own allocator lock. libplatform
// aborts it and the kernel kills it a few milliseconds after the pane opened.
//
// The rule is leading edge and trailing edge. A size change after a quiet
// period reaches the kernel at once, so the first correct size is never
// delayed. A change inside the quiet period is held, later ones replace it,
// and the last is written when the period ends. The pane's recorded size, its
// emulator and every subscriber still change at once (see PTY.Resize); only the
// guest hears about a burst later, and only once.
const (
	// winsizeQuiet is the least time between two window size writes. It is
	// about two frames: a drag still resizes the guest smoothly, and a burst
	// of announcements from several clients becomes one write.
	winsizeQuiet = 30 * time.Millisecond

	// winsizeStartup is how long after the spawn a held size waits for. A
	// shell is most fragile while it starts, and bash 3.2 was measured dying
	// 60 to 100 ms after launch on a loaded machine, so every size after the
	// first is held until the shell has had time to reach its prompt.
	winsizeStartup = 250 * time.Millisecond
)

// setWinsizeLocked asks for the real PTY to be width by height cells. It writes now
// or holds the size for later, by the rule above. The caller holds winsizeMu.
func (p *PTY) setWinsizeLocked(width, height int) error {
	if p.pty == nil || p.winsizeClosed {
		return nil
	}
	now := time.Now()
	due := p.winsizeDue(now)
	if !now.Before(due) {
		return p.writeWinsizeLocked(width, height, now)
	}
	p.winsizeHeld.Store(true)
	p.heldWidth, p.heldHeight = width, height
	if p.winsizeTimer == nil {
		p.winsizeTimer = time.AfterFunc(due.Sub(now), p.winsizeTimerFired)
	}
	return nil
}

// winsizeDue is when the next window size write may happen. The first write
// of a pane's life is due at once. The caller holds winsizeMu.
func (p *PTY) winsizeDue(now time.Time) time.Time {
	if p.winsizeWritten.IsZero() {
		return now
	}
	due := p.winsizeWritten.Add(winsizeQuiet)
	if startup := p.spawnedAt.Add(winsizeStartup); !p.spawnedAt.IsZero() && startup.After(due) {
		due = startup
	}
	return due
}

// writeWinsizeLocked writes the size to the kernel now, with the pixel size
// from the last cell size a client reported, and drops any held size. The
// caller holds winsizeMu.
func (p *PTY) writeWinsizeLocked(width, height int, now time.Time) error {
	if p.winsizeTimer != nil {
		p.winsizeTimer.Stop()
		p.winsizeTimer = nil
	}
	p.winsizeWritten = now
	err := ptyspawn.SetWinsize(p.pty, width, height, width*p.cellWidth, height*p.cellHeight)
	// Cleared once the kernel has the size, not before. flushWinsize reads the
	// flag without the lock, and a clear ahead of the ioctl let a keystroke
	// through to a guest that then read the size from before the burst.
	p.winsizeHeld.Store(false)
	return err
}

// flushWinsize writes a held size now, if there is one. Write calls it before
// input, so a guest that reads its size after a resize and a keystroke reads
// the size the resize set: `stty size` typed straight after a resize answers
// with the new size, not the one before the burst.
func (p *PTY) flushWinsize() {
	if !p.winsizeHeld.Load() {
		return
	}
	p.winsizeMu.Lock()
	defer p.winsizeMu.Unlock()
	if !p.winsizeHeld.Load() || p.winsizeClosed {
		return
	}
	p.writeHeldWinsizeLocked()
}

// winsizeTimerFired writes a held size once it is due. A timer that was
// stopped too late to keep it from firing finds nothing held, or a size held
// since that is not due yet and has a timer of its own.
func (p *PTY) winsizeTimerFired() {
	p.winsizeMu.Lock()
	defer p.winsizeMu.Unlock()
	if !p.winsizeHeld.Load() || p.winsizeClosed {
		return
	}
	now := time.Now()
	if now.Before(p.winsizeDue(now)) {
		return
	}
	p.writeHeldWinsizeLocked()
}

// writeHeldWinsizeLocked writes the held size. The caller holds winsizeMu.
func (p *PTY) writeHeldWinsizeLocked() {
	if err := p.writeWinsizeLocked(p.heldWidth, p.heldHeight, time.Now()); err != nil {
		debugLog("[DEBUG] PTY %s: held resize to %dx%d failed: %v", shortID(p.ID), p.heldWidth, p.heldHeight, err)
	}
}

// closeWinsize stops any held size from being written. Close calls it before
// the pty is closed, so a timer that fires late cannot write to a descriptor
// the process may already have reused.
func (p *PTY) closeWinsize() {
	p.winsizeMu.Lock()
	defer p.winsizeMu.Unlock()
	p.winsizeClosed = true
	p.winsizeHeld.Store(false)
	if p.winsizeTimer != nil {
		p.winsizeTimer.Stop()
		p.winsizeTimer = nil
	}
}
