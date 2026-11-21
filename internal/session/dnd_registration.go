package session

import (
	"sync"

	"tuios.dev/vt/dnd"
)

type dndRegistration struct {
	mu      sync.Mutex
	asm     dnd.Assembler
	accepts bool
	offers  bool
	id      int
	owner   int
}

type dndRegistrationFact struct {
	accepts bool
	offers  bool
	id      int
	owner   int
}

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

func (r *dndRegistration) fact() dndRegistrationFact {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner > 0 && !ProcessGroupAlive(r.owner) {
		r.accepts, r.offers, r.owner = false, false, 0
	}
	return dndRegistrationFact{accepts: r.accepts, offers: r.offers, id: r.id, owner: r.owner}
}
