package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
)

const (
	winsizeQuiet = 30 * time.Millisecond

	winsizeStartup = 250 * time.Millisecond
)

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

func (p *PTY) writeWinsizeLocked(width, height int, now time.Time) error {
	if p.winsizeTimer != nil {
		p.winsizeTimer.Stop()
		p.winsizeTimer = nil
	}
	p.winsizeWritten = now
	err := ptyspawn.SetWinsize(p.pty, width, height, width*p.cellWidth, height*p.cellHeight)
	p.winsizeHeld.Store(false)
	return err
}

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

func (p *PTY) writeHeldWinsizeLocked() {
	if err := p.writeWinsizeLocked(p.heldWidth, p.heldHeight, time.Now()); err != nil {
		debugLog("[DEBUG] PTY %s: held resize to %dx%d failed: %v", shortID(p.ID), p.heldWidth, p.heldHeight, err)
	}
}

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
