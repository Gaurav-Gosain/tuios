package session

import (
	"sync"
	"testing"
	"time"
)

// Every client of a session is written to on a goroutine of its own, so two
// announcements settled close together race for the connection and arrive in
// whichever order the scheduler picks. Each one carries the whole answer - the
// size and the chrome reserve - so taking the older one last does not lose an
// update, it reinstates a wrong one, and it stays wrong until something else
// moves.
//
// That is what the generation on the announcement is for.

// TestClientsConvergeOnOneLayoutUnderConcurrentAnnouncements has two clients
// announcing different chrome at the same time, over and over. Whatever order
// the broadcasts land in, the two have to end up holding the same answer, and
// it has to be the one the daemon holds.
//
// NEGATIVE CONTROL: measured. Without the generation - dropping the check in
// noteSessionLayout - the two clients end up holding different reserves within
// a few rounds, one of them stuck on a value the session has moved past. It is
// the same fault that showed up in internal/app as
// TestFocusSwitchResizesNothing failing about once in fifty, with the two
// clients laying panes out in boxes 21 columns apart.
func TestClientsConvergeOnOneLayoutUnderConcurrentAnnouncements(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "racing")

	left := attachTestClient(t, "racing")
	right := attachTestClient(t, "racing")

	// Both announce at once, with chrome that disagrees, so every round settles
	// the session twice and the two broadcasts chase each other to both clients.
	for round := range 40 {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			left.SetOwnLayoutReserve(LayoutReserve{Left: 4 + round%3})
			_ = left.NotifyTerminalSize(100, 30)
		}()
		go func() {
			defer wg.Done()
			right.SetOwnLayoutReserve(LayoutReserve{Left: 20 + round%3})
			_ = right.NotifyTerminalSize(100, 30)
		}()
		wg.Wait()
	}

	// Everything has been sent; let the last broadcasts land.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if left.SessionLayoutReserve() == right.SessionLayoutReserve() &&
			left.SessionLayoutReserve() == sess.LayoutReserve() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the clients never agreed on the session's box:\n left %+v\n right %+v\n daemon %+v",
		left.SessionLayoutReserve(), right.SessionLayoutReserve(), sess.LayoutReserve())
}
