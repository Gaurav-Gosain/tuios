package session

import (
	"time"
)

const transferRemoved = "removed"

func (m *transferManager) expireLater(j *transferJob) {
	j.mu.Lock()
	ended := j.ended
	j.mu.Unlock()
	if ended.IsZero() {
		return
	}
	wait := max(m.keep-time.Since(ended), 0) + 50*time.Millisecond
	time.AfterFunc(wait, func() {
		m.mu.Lock()
		m.pruneLocked()
		m.mu.Unlock()
	})
}
