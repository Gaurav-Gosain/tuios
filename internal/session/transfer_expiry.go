package session

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A copy that ended stays in the list for transferKeep, and then leaves it on
// its own. Leaving is said on the event stream, as clearing one is: a
// transfer event with action removed and the row it had, so a client that
// mirrors the list (the Copies panel) drops the row instead of showing it
// until its next full listing.
//
// Every copy that ends arms one timer for the moment it is due. An expiry run
// only inside a listing or a new copy would drop the copy from the daemon's
// list at some later call with nothing to say so.

// transferRemoved is the action of the transfer event for a copy that left
// the list, by transfer-clear or by its expiry.
const transferRemoved = "removed"

// transferKeep is transferKeepDone, or TUIOS_TRANSFER_KEEP_SECONDS when it is
// a positive number, which a test uses to see the expiry in seconds.
var transferKeep = sync.OnceValue(func() time.Duration {
	if s := strings.TrimSpace(os.Getenv("TUIOS_TRANSFER_KEEP_SECONDS")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return transferKeepDone
})

// expireLater arms the timer that takes a copy that ended out of the list
// once it is due. The timer prunes every due copy, so one that was retried
// in between, and so has not ended, stays.
func (m *transferManager) expireLater(j *transferJob) {
	j.mu.Lock()
	ended := j.ended
	j.mu.Unlock()
	if ended.IsZero() {
		return
	}
	wait := max(transferKeep()-time.Since(ended), 0) + 50*time.Millisecond
	time.AfterFunc(wait, func() {
		m.mu.Lock()
		m.pruneLocked()
		m.mu.Unlock()
	})
}
