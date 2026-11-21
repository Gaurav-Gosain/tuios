package session

import (
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

const queueClientIDMax = 128

const queueDedupKeep = 256

const queueDedupAge = 7 * 24 * time.Hour

type queueDedupKey struct {
	kind, peer, window string
	target             string
	id                 string
}

func newQueueDedupKey(o queueOrigin, target, id string) queueDedupKey {
	return queueDedupKey{kind: o.kind, peer: o.peer, window: o.window, target: target, id: id}
}

type queueDedup struct {
	mu    sync.Mutex
	seen  map[queueDedupKey]queueDedupEntry
	order []queueDedupKey
}

type queueDedupEntry struct {
	res queuedResult
	at  time.Time
}

func (q *queueDedup) once(key queueDedupKey, now time.Time, queue func() (queuedResult, *verbError)) (res queuedResult, repeat bool, verr *verbError) {
	q.mu.Lock()
	defer q.mu.Unlock()
	old, known := q.seen[key]
	if known && now.Sub(old.at) < queueDedupAge {
		return old.res, true, nil
	}
	res, verr = queue()
	if verr != nil {
		return res, false, verr
	}
	if q.seen == nil {
		q.seen = make(map[queueDedupKey]queueDedupEntry)
	}
	if known {
		q.order = slices.DeleteFunc(q.order, func(k queueDedupKey) bool { return k == key })
	}
	q.seen[key] = queueDedupEntry{res: res, at: now}
	q.order = append(q.order, key)
	for len(q.order) > queueDedupKeep {
		delete(q.seen, q.order[0])
		q.order = q.order[1:]
	}
	return res, false, nil
}

func queueDedupNow() time.Time {
	now := time.Now()
	if path := testknob.String("queue_dedup_skew"); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			if h, err := strconv.Atoi(string(b)); err == nil {
				return now.Add(time.Duration(h) * time.Hour)
			}
		}
	}
	return now
}
