package session

import (
	"sync"
	"time"
)

// queueClientIDMax bounds a queue-prompt client_id, in bytes.
const queueClientIDMax = 128

// queueDedupKeep is how many answered client ids the daemon keeps. A phone
// sends a message again only after a lost answer, within minutes.
const queueDedupKeep = 256

// queueDedupAge is how long an answered client id is kept.
const queueDedupAge = time.Hour

// queueDedup remembers what queue-prompt answered for a client's message id,
// so a client that lost the answer and sends again does not queue it twice.
// It lives in memory: a daemon restart drops the queue's in-flight state too.
type queueDedup struct {
	mu   sync.Mutex
	seen map[string]queueDedupEntry
	// order is the keys oldest first, for the bound.
	order []string
}

type queueDedupEntry struct {
	res queuedResult
	at  time.Time
}

// once runs queue the first time key is seen and records a success. It holds
// the lock across queue, so two sends of one message cannot both queue it.
// repeat is true when the answer is the remembered one.
func (q *queueDedup) once(key string, now time.Time, queue func() (queuedResult, *verbError)) (res queuedResult, repeat bool, verr *verbError) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if e, ok := q.seen[key]; ok && now.Sub(e.at) < queueDedupAge {
		return e.res, true, nil
	}
	res, verr = queue()
	if verr != nil {
		return res, false, verr
	}
	if q.seen == nil {
		q.seen = make(map[string]queueDedupEntry)
	}
	q.seen[key] = queueDedupEntry{res: res, at: now}
	q.order = append(q.order, key)
	for len(q.order) > queueDedupKeep {
		delete(q.seen, q.order[0])
		q.order = q.order[1:]
	}
	return res, false, nil
}
