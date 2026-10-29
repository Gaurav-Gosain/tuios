package session

import (
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/dnd"
)

// The drag and drop protocol (OSC 72) on the daemon's side.
//
// A program says once, when it starts, that it takes drops (t=a) or offers
// drags (t=o:x=1). Every client acts on what the program said, and only the
// daemon sees every byte the program writes: a client that attaches after the
// program started never saw its registration, and with nothing to go on would
// leave yazi without drag and drop until yazi restarts. So the daemon keeps the
// registration, as it keeps the title, and hands it to every client in the
// window state (WindowState.DnD*). A client takes it for a pane it has heard
// nothing from itself.
//
// The daemon also answers the protocol's query (t=q), once, whoever is
// attached. Each client answering for itself would give a program one answer
// per client.
//
// Everything else of the protocol, the drags themselves, is the clients'
// business: a drag belongs to the client whose pointer made it. See
// internal/app/dnd.go.

// dndRegistration is what a pane's program said about drag and drop. It is
// written from the vtWriter goroutine, under the terminal lock, and read by
// the state snapshot on another, so it has a lock of its own.
type dndRegistration struct {
	mu sync.Mutex
	// asm joins chunked messages. Only the vtWriter touches it.
	asm     dnd.Assembler
	accepts bool
	offers  bool
	// id is the multiplexer id (i key) the program used, which a client
	// echoes on everything it sends that program.
	id int
}

// dndRegistrationFact is a copy of a registration for a state snapshot.
type dndRegistrationFact struct {
	accepts bool
	offers  bool
	id      int
}

// note takes the body of one OSC 72 the program wrote. It returns the reply
// the program is owed, which only a query gets.
func (r *dndRegistration) note(body []byte) []byte {
	cmd, ok := dnd.Parse(body)
	if !ok {
		return nil
	}
	cmd, ok = r.asm.Add(cmd)
	if !ok {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if cmd.ID != 0 {
		r.id = cmd.ID
	}
	switch cmd.Type {
	case 'q':
		return dnd.Command{Type: 'q', ID: r.id}.Encode()
	case 'a':
		// x=1 carries a machine id. Every pane is local to the daemon that
		// runs it.
		if cmd.X != 1 {
			r.accepts = true
		}
	case 'A':
		r.accepts = false
	case 'o':
		switch cmd.X {
		case 1:
			r.offers = true
		case 2:
			r.offers = false
		}
	}
	return nil
}

// fact copies the registration.
func (r *dndRegistration) fact() dndRegistrationFact {
	r.mu.Lock()
	defer r.mu.Unlock()
	return dndRegistrationFact{accepts: r.accepts, offers: r.offers, id: r.id}
}
