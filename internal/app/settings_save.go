package app

import (
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Config writes in order.
//
// A settings change renders the config on the Update goroutine and writes it
// in a command, off that goroutine. Bubble Tea runs each command in a
// goroutine of its own, so two writes made in a row can run in either order.
// Each write is the change from the config the one before it rendered, and
// config.RenderUserConfig's own guard skips only a key a newer write already
// wrote. A newer write that found its value already in the file wrote
// nothing, so the older write that ran after it put the old value back: a
// section taken off the rail and put back again stayed off.
//
// The queue keeps the writes in the order they were rendered. A write that
// runs first runs every older write still waiting, then its own. An older
// write's command that runs later finds its work done and returns what it
// got. A write whose command is never run still lands with the next one, so
// no command waits on another.

// settingsSaveQueue holds the writes rendered and not yet run, oldest first.
type settingsSaveQueue struct {
	mu      sync.Mutex
	next    uint64
	pending []queuedSettingsSave
	// done is what each write that another write's command ran returned,
	// until its own command asks for it.
	done map[uint64]settingsSaveResult
}

// queuedSettingsSave is one rendered write and its place in the order.
type queuedSettingsSave struct {
	seq   uint64
	write func() (config.WriteNote, error)
}

// settingsSaveResult is what one write returned.
type settingsSaveResult struct {
	note config.WriteNote
	err  error
}

// queueSettingsSave puts write in the queue and returns the function a
// command calls in its place. Call it on the Update goroutine, right after
// config.RenderUserConfig, so the queue's order is the render order.
func (m *OS) queueSettingsSave(write func() (config.WriteNote, error)) func() (config.WriteNote, error) {
	if m.settingsSaves == nil {
		m.settingsSaves = &settingsSaveQueue{done: map[uint64]settingsSaveResult{}}
	}
	q := m.settingsSaves
	q.mu.Lock()
	q.next++
	seq := q.next
	q.pending = append(q.pending, queuedSettingsSave{seq: seq, write: write})
	q.mu.Unlock()
	return func() (config.WriteNote, error) { return q.run(seq) }
}

// run runs every write up to seq that is still waiting, oldest first, and
// returns what the write seq returned.
func (q *settingsSaveQueue) run(seq uint64) (config.WriteNote, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.pending) > 0 && q.pending[0].seq <= seq {
		s := q.pending[0]
		q.pending = q.pending[1:]
		note, err := s.write()
		q.done[s.seq] = settingsSaveResult{note: note, err: err}
	}
	r := q.done[seq]
	delete(q.done, seq)
	// A write whose command never ran never asks for its result.
	for k := range q.done {
		if k+settingsSaveKeep < seq {
			delete(q.done, k)
		}
	}
	return r.note, r.err
}

// settingsSaveKeep is how far behind the newest write a result waits for its
// command before it is dropped.
const settingsSaveKeep = 64
