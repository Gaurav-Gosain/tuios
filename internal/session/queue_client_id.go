package session

import (
	"os"
	"slices"
	"strconv"
	"sync"
	"time"
)

// queueClientIDMax bounds a queue-prompt client_id, in bytes.
const queueClientIDMax = 128

// queueDedupKeep is how many answered client ids the daemon keeps, across
// every pane. It is what bounds the memory.
const queueDedupKeep = 256

// queueDedupAge is how long an answered client id is kept. A phone keeps a
// message through an outage and sends it again when the link is back, so this
// has to outlast a long one.
const queueDedupAge = 7 * 24 * time.Hour

// queueDedupKey names one client's message for one pane. The sender is in it,
// so two clients that count their ids from 1 do not drop each other's
// messages. It takes the origin's kind, peer and window and not the entry's
// by, which turns from link:X to human when a resend carries a nonce the first
// send did not. It leaves out the origin's conn: a resend comes on a new
// connection.
type queueDedupKey struct {
	kind, peer, window string
	target             string
	id                 string
}

func newQueueDedupKey(o queueOrigin, target, id string) queueDedupKey {
	return queueDedupKey{kind: o.kind, peer: o.peer, window: o.window, target: target, id: id}
}

// queueDedup remembers what queue-prompt answered for a client's message id,
// so a client that lost the answer and sends again does not queue it twice.
// It lives in memory: a daemon restart forgets the ids.
type queueDedup struct {
	mu   sync.Mutex
	seen map[queueDedupKey]queueDedupEntry
	// order is the keys oldest first, for the bound. Each key is in it once.
	order []queueDedupKey
}

type queueDedupEntry struct {
	res queuedResult
	at  time.Time
}

// once runs queue the first time key is seen and records a success. It holds
// the lock across queue, so two sends of one message cannot both queue it.
// repeat is true when the answer is the remembered one.
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
		// Expired: its old slot goes, or evicting that slot would delete
		// the entry made here.
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

// queueDedupNow is the time the dedupe reads, moved by the hours in the file
// TUIOS_E2E_QUEUE_DEDUP_SKEW names, so an E2E test can pass the age without
// waiting for it.
func queueDedupNow() time.Time {
	now := time.Now()
	if os.Getenv("TUIOS_E2E") == "1" {
		if path := os.Getenv("TUIOS_E2E_QUEUE_DEDUP_SKEW"); path != "" {
			if b, err := os.ReadFile(path); err == nil {
				if h, err := strconv.Atoi(string(b)); err == nil {
					return now.Add(time.Duration(h) * time.Hour)
				}
			}
		}
	}
	return now
}
