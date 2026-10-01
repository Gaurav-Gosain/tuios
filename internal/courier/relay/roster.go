package relay

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/courier"
)

// The roster is the identities a relay serves, one per line:
//
//	# the platform team
//	ghaith tc1.AbC…
//	gg     tc1.XyZ…   # Gaurav
//
// The name is for the operator reading the file; the relay goes by the key.
// The file is read again when its modification time changes, so adding or
// removing a teammate needs no restart.

type rosterSet struct {
	ids       map[courier.Identity]string
	mailboxes map[string]courier.Identity
}

func (rs *rosterSet) has(id courier.Identity) bool {
	_, ok := rs.ids[id]
	return ok
}

func (rs *rosterSet) byMailbox(mb string) (courier.Identity, bool) {
	id, ok := rs.mailboxes[mb]
	return id, ok
}

type roster struct {
	path string
	now  func() time.Time

	mu        sync.Mutex
	set       *rosterSet
	mtime     time.Time
	checkedAt time.Time
}

func loadRoster(path string, now func() time.Time) (*roster, error) {
	r := &roster{path: path, now: now}
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("roster: %v", err)
	}
	set, err := parseRoster(path)
	if err != nil {
		return nil, err
	}
	r.set, r.mtime, r.checkedAt = set, st.ModTime(), now()
	return r, nil
}

// current is the roster now, read again first if the file changed. A file
// that no longer parses keeps the last good roster: a half-saved edit should
// not lock the whole team out.
func (r *roster) current() *rosterSet {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if now.Sub(r.checkedAt) < rosterCheckEvery {
		return r.set
	}
	r.checkedAt = now
	st, err := os.Stat(r.path)
	if err != nil || st.ModTime().Equal(r.mtime) {
		return r.set
	}
	if set, err := parseRoster(r.path); err == nil {
		r.set, r.mtime = set, st.ModTime()
	}
	return r.set
}

func parseRoster(path string) (*rosterSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("roster: %v", err)
	}
	set := &rosterSet{ids: map[courier.Identity]string{}, mailboxes: map[string]courier.Identity{}}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		var name, idText string
		switch len(fields) {
		case 0:
			continue
		case 1:
			idText = fields[0]
		case 2:
			name, idText = fields[0], fields[1]
		default:
			return nil, fmt.Errorf("roster %s line %d: want NAME IDENTITY", path, n)
		}
		id, err := courier.ParseIdentity(idText)
		if err != nil {
			return nil, fmt.Errorf("roster %s line %d: %v", path, n, err)
		}
		set.ids[id] = name
		set.mailboxes[id.MailboxID()] = id
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("roster %s: %v", path, err)
	}
	if len(set.ids) == 0 {
		return nil, fmt.Errorf("roster %s names nobody", path)
	}
	return set, nil
}
