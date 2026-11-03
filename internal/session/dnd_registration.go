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
//
// A program unregisters when it exits, if it exits cleanly. One that crashes
// or is killed says nothing, and the shell its pane goes back to would be
// sent drops it cannot read. So a registration is bound to the process group
// that held the pane's foreground when it was made, and it ends when that
// group is gone. That is checked when the registration is read, so no timer
// runs for it; the clients check the same group the same way.

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
	// owner is the process group that held the pane's foreground when the
	// program registered, 0 when it could not be read.
	owner int
}

// dndRegistrationFact is a copy of a registration for a state snapshot.
type dndRegistrationFact struct {
	accepts bool
	offers  bool
	id      int
	owner   int
}

// note takes the body of one OSC 72 the program wrote. It returns the reply
// the program is owed, which only a query gets. foreground reads the pane's
// foreground process group, which a registration is bound to.
func (r *dndRegistration) note(body []byte, foreground func() int) []byte {
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
			r.owner = foreground()
		}
	case 'A':
		r.accepts = false
	case 'o':
		switch cmd.X {
		case 1:
			r.offers = true
			r.owner = foreground()
		case 2:
			r.offers = false
		}
	}
	if !r.accepts && !r.offers {
		r.owner = 0
	}
	return nil
}

// fact copies the registration, and ends it first when the process group it
// is bound to is gone.
func (r *dndRegistration) fact() dndRegistrationFact {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner > 0 && !ProcessGroupAlive(r.owner) {
		r.accepts, r.offers, r.owner = false, false, 0
	}
	return dndRegistrationFact{accepts: r.accepts, offers: r.offers, id: r.id, owner: r.owner}
}
