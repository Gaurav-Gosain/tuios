package session

import (
	"hash/fnv"
	"sync"
)

// textClockRows is more rows than any screen has, so TailText reads the whole
// visible screen.
const textClockRows = 1 << 16

// textClock records when the text on a pane's screen last changed, for the
// agent-state silence timer.
//
// The timer used to read the pane's output clock, and output is not
// activity. An agent that draws its own blinking cursor (Mistral Vibe does,
// and so does any Textual program) inverts one cell and clears it again twice
// a second while it waits at its prompt. Every blink is output, so a pane the
// detector had called working was never silent and stayed working for as long
// as the agent was open. A blink changes a cell's style and not its text, so
// this clock keeps still through it.
//
// The clock is read lazily: the screen is hashed only when someone asks and
// the pane has written since the last hash, so a pane nobody asks about costs
// nothing and the PTY read loop does no extra work. A change found by a read is
// stamped with the pane's latest output time, the latest moment it can have
// happened, which errs toward working.
//
// A working agent that changes only colours (a shimmer on fixed text, with no
// counter or spinner glyph) reads as silent too. That pane falls to unknown
// after the stall window unless its harness's screen rules say working, which
// is the honest answer for a pane nothing can read.
//
// Lock order: textClock.mu, then PTY.terminalMu, which is a leaf.
type textClock struct {
	mu        sync.Mutex
	seen      bool
	hash      uint64
	checkedAt int64 // the pane's output time when hash was taken
	changedAt int64
}

// TextChangedAt returns the unix-nano time this PTY's screen text was last
// seen to change, or 0 when it has never written. It is never later than
// LastOutput.
func (p *PTY) TextChangedAt() int64 {
	out := p.lastOutput.Load()
	c := &p.textClock
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen && out <= c.checkedAt {
		return c.changedAt
	}
	h := p.screenTextHash()
	if !c.seen || h != c.hash {
		// Read after the hash: output that landed while it was taken may be
		// in it, and the stamp must not be earlier than the text it covers.
		c.changedAt = p.lastOutput.Load()
	}
	c.seen, c.hash, c.checkedAt = true, h, out
	return c.changedAt
}

// screenTextHash hashes the plain text of the whole visible screen, styles
// left out.
func (p *PTY) screenTextHash() uint64 {
	h := fnv.New64a()
	for _, line := range p.tailText(textClockRows) {
		_, _ = h.Write([]byte(line))
		_, _ = h.Write([]byte{'\n'})
	}
	return h.Sum64()
}

// textChangedAt is TextChangedAt for the PTY ptyID, 0 when there is none. It
// is the clock the silence timer reads.
func (s *Session) textChangedAt(ptyID string) int64 {
	if pty := s.GetPTY(ptyID); pty != nil {
		return pty.TextChangedAt()
	}
	return 0
}
