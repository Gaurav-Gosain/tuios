package app

import (
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

type settingsSaveQueue struct {
	mu      sync.Mutex
	next    uint64
	pending []queuedSettingsSave
	done    map[uint64]settingsSaveResult
}

type queuedSettingsSave struct {
	seq   uint64
	write func() (config.WriteNote, error)
}

type settingsSaveResult struct {
	note config.WriteNote
	err  error
}

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
	for k := range q.done {
		if k+settingsSaveKeep < seq {
			delete(q.done, k)
		}
	}
	return r.note, r.err
}

const settingsSaveKeep = 64
