package session

import (
	"sync"
	"time"
)

// Telling clients where a pane is.
//
// A client on the pane's own machine can find out for itself: it parses the
// shell's OSC 7 as the bytes arrive, and it can read the shell's process. A
// client on another machine can do neither. The OSC 7 report names a host the
// client judges against its own name, and the pid is a pid on a machine it is
// not on. So the folder in the window state is the only answer such a client
// has, and the state is only pushed when the session changes. A cd changes
// nothing in the session, so before this the client was told where the pane
// started and never told again (issue #313).
//
// So a move is pushed here, as a state push with no change in it, which is
// what PublishLiveFacts already does for a pane on another machine. A move is
// noticed three ways:
//
//   - The shell announces a folder over OSC 7. The emulator callback sees it
//     at once (placeRecord.announce).
//   - A shell that never announces is read from its process, after it prints.
//     A prompt after a cd is output, so output is the hint. The read is paced
//     to once a second, with one more read after the pane goes quiet, because
//     the prompt that follows a cd often comes inside the second the typing
//     of the cd already spent.
//   - A pane on another machine is asked about over the link, on the same
//     pacing (remotePane.askCwd).
//
// Nothing runs while a pane prints nothing, and a pane that prints without
// moving costs one process read a second and no push.

// cwdCheckInterval is the least time between two looks at one pane's folder.
const cwdCheckInterval = time.Second

// paneCwdCheck is one pane's pacing of the look, and what the last look saw.
type paneCwdCheck struct {
	mu       sync.Mutex
	at       time.Time
	trailing bool
	seen     string
}

// noteCwdOnOutput looks at where p is, if a second has passed since the last
// look, and otherwise makes sure one more look follows when it has.
func (s *Session) noteCwdOnOutput(p *PTY) {
	if p == nil {
		return
	}
	// A local shell that announces its folder, and has no report from
	// another machine to clear, gives checkPaneCwd nothing to do.
	if p.place.announced.Load() && p.place.Elsewhere() == "" {
		if _, remote := p.pty.(*remotePane); !remote {
			return
		}
	}
	c := &p.cwdCheck
	c.mu.Lock()
	wait := cwdCheckInterval - time.Since(c.at)
	if wait > 0 {
		if !c.trailing {
			c.trailing = true
			time.AfterFunc(wait, func() {
				c.mu.Lock()
				c.trailing = false
				c.at = time.Now()
				c.mu.Unlock()
				s.checkPaneCwd(p)
			})
		}
		c.mu.Unlock()
		return
	}
	c.at = time.Now()
	c.mu.Unlock()
	s.checkPaneCwd(p)
}

// checkPaneCwd looks at where p is and tells the clients when it moved.
func (s *Session) checkPaneCwd(p *PTY) {
	if rp, ok := p.pty.(*remotePane); ok {
		// The answer lands later and publishes itself when it differs.
		rp.askCwd()
		return
	}
	if p.IsExited() {
		return
	}
	// A report from another machine lasts while the program that made it,
	// ssh or what it runs, holds the terminal. Once the shell has it back the
	// pane is on this machine again, whether or not this shell announces.
	if p.place.Elsewhere() != "" && shellAtPrompt(p) && p.place.setElsewhere("") {
		s.publishPlaceMove()
	}
	if p.place.announced.Load() {
		// The shell reports its own moves, at once and more exactly than
		// its process can.
		return
	}
	cwd, ok := p.ProcessCwd()
	if !ok || cwd == "" {
		return
	}
	c := &p.cwdCheck
	c.mu.Lock()
	if c.seen == "" {
		// The first look compares with where the shell was started, which
		// every snapshot since the spawn has carried.
		c.seen = p.place.Cwd()
	}
	moved := c.seen != cwd
	c.seen = cwd
	c.mu.Unlock()
	if moved {
		s.publishPlaceMove()
	}
}

// publishPlaceMove pushes the session's state because a pane moved. It runs on
// its own goroutine, because a caller can hold the emulator's lock, and moves
// that arrive while one push is waiting to start share it.
func (s *Session) publishPlaceMove() {
	if !s.placePushQueued.CompareAndSwap(false, true) {
		return
	}
	go func() {
		s.placePushQueued.Store(false)
		s.PublishLiveFacts()
	}()
}
