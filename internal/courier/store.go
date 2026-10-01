package courier

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The local store is a directory of small files, so that every process the
// person runs (an agent's hook, a waiting agent, the person's own inbox
// command) can share it without a daemon and without a lock:
//
//	inbox/<key>.json       the message, written once
//	inbox/<key>.released   the person let the agents read it
//	inbox/<key>.claim      a reader is printing it now (holds the time)
//	inbox/<key>.read       an agent was handed it
//	inbox/<key>.dropped    the person deleted it
//	sent/<id>.json         a message this person sent
//	sent/<thread>-<mailbox>.to  this person sent into the thread to that mailbox
//	pending/<relay id>.json     mail from an identity not yet in peers
//
// Every state change creates a file with O_EXCL, which the filesystem makes
// atomic, so two readers cannot both claim a message. A message file is put in
// place with a hard link, which fails when the name exists, so a duplicate
// never replaces the first copy. Nothing is deleted except by GC, after the
// message is too old to be accepted again: a dropped message stays as a
// tombstone, so a relay that hands it over again cannot bring it back.

// claimLease is how long a claim holds. A reader that dies after claiming and
// before marking read leaves a claim, and after the lease the message can be
// handed out again: a repeat, never a loss.
const claimLease = 2 * time.Minute

// maxPending bounds the mail kept from senders not yet in peers.
const maxPending = 64

// Record is a received message as the store keeps it.
type Record struct {
	Msg Message `json:"msg"`
	// From is the sender's identity, Peer the name the person gave them.
	From       string    `json:"from"`
	Peer       string    `json:"peer"`
	RelayID    string    `json:"relay_id"`
	ReceivedAt time.Time `json:"received_at"`
}

// Key names the record in the store: the sender's mailbox prefix and the
// message id. Two senders can choose the same id; one sender's replay of an id
// is the same message.
func (r Record) Key() string {
	mb := "0000000000000000"
	if id, err := ParseIdentity(r.From); err == nil {
		mb = id.MailboxID()[:16]
	}
	return mb + "-" + r.Msg.ID
}

// Entry is a record and its state.
type Entry struct {
	Record
	Key      string
	Released bool
	Read     bool
	Dropped  bool
}

// SentRecord is a message this person sent.
type SentRecord struct {
	Msg  Message `json:"msg"`
	To   string  `json:"to"`
	Peer string  `json:"peer"`
}

// Filter picks which deliverable mail a reader gets.
type Filter struct {
	// Agent is the reader's label. A labeled reader gets mail for its label
	// and unlabeled mail; an unlabeled reader gets all of it.
	Agent string
	// Thread limits delivery to one thread.
	Thread string
}

func (f Filter) match(e Entry) bool {
	if f.Agent != "" && e.Msg.Agent != "" && e.Msg.Agent != f.Agent {
		return false
	}
	return f.Thread == "" || e.Msg.Thread == f.Thread
}

// Store is the local mail store.
type Store struct {
	dir string
	now func() time.Time
}

// OpenStore opens, creating, the store under dir.
func OpenStore(dir string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	for _, sub := range []string{"inbox", "sent", "pending"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir, now: now}, nil
}

func (s *Store) inbox(name string) string   { return filepath.Join(s.dir, "inbox", name) }
func (s *Store) sent(name string) string    { return filepath.Join(s.dir, "sent", name) }
func (s *Store) pending(name string) string { return filepath.Join(s.dir, "pending", name) }

// Put stores a record and reports whether it is new. A record already there,
// dropped or not, wins.
func (s *Store) Put(r Record) (bool, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return false, err
	}
	return createOnce(s.inbox(r.Key()+".json"), data)
}

// createOnce writes data to path unless path exists, and reports whether it
// wrote. The file appears whole: it is written aside and linked into place.
func createOnce(path string, data []byte) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		return false, nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return false, err
	}
	tmp := f.Name()
	defer os.Remove(tmp) //nolint:errcheck // the link holds the data; the temp name goes either way
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return false, werr
	}
	err = os.Link(tmp, path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	// A filesystem without hard links: create the name exclusively and write
	// it. A reader that catches it half written skips it until the next read.
	f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, werr = f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr == nil, werr
}

// mark creates a marker file, and is not an error when it exists.
func mark(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func (s *Store) load(key string) (Entry, error) {
	data, err := os.ReadFile(s.inbox(key + ".json"))
	if err != nil {
		return Entry{}, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Entry{}, err
	}
	return Entry{
		Record:   r,
		Key:      key,
		Released: exists(s.inbox(key + ".released")),
		Read:     exists(s.inbox(key + ".read")),
		Dropped:  exists(s.inbox(key + ".dropped")),
	}, nil
}

// Get returns one entry by key.
func (s *Store) Get(key string) (Entry, error) {
	if !validKey(key) {
		return Entry{}, fmt.Errorf("no message %q", clip(key))
	}
	return s.load(key)
}

func validKey(key string) bool {
	mb, id, ok := strings.Cut(key, "-")
	return ok && len(mb) == 16 && isHex(mb) && ValidID(id)
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// all is every entry, dropped ones included, oldest first.
func (s *Store) all() []Entry {
	names, _ := os.ReadDir(filepath.Join(s.dir, "inbox"))
	var out []Entry
	for _, n := range names {
		key, ok := strings.CutSuffix(n.Name(), ".json")
		if !ok || !validKey(key) {
			continue
		}
		e, err := s.load(key)
		if err != nil {
			continue
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b Entry) int {
		if c := a.ReceivedAt.Compare(b.ReceivedAt); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	return out
}

// List is every entry the person has not dropped, oldest first.
func (s *Store) List() []Entry {
	return slices.DeleteFunc(s.all(), func(e Entry) bool { return e.Dropped })
}

// Find resolves what a person typed: a key, a message id, or a unique prefix
// of a message id of at least six characters.
func (s *Store) Find(ref string) (Entry, error) {
	if len(ref) < 6 || !isHex(strings.ReplaceAll(ref, "-", "")) {
		return Entry{}, fmt.Errorf("no message %q: give its id, or at least six characters of it", clip(ref))
	}
	var hits []Entry
	for _, e := range s.List() {
		if e.Key == ref || e.Msg.ID == ref {
			return e, nil
		}
		if strings.HasPrefix(e.Msg.ID, ref) {
			hits = append(hits, e)
		}
	}
	switch len(hits) {
	case 0:
		return Entry{}, fmt.Errorf("no message %q", clip(ref))
	case 1:
		return hits[0], nil
	}
	return Entry{}, fmt.Errorf("%q matches %d messages: give more of the id", ref, len(hits))
}

// Release lets the agents read a message.
func (s *Store) Release(key string) error {
	if _, err := s.Get(key); err != nil {
		return err
	}
	return mark(s.inbox(key+".released"), nil)
}

// Drop deletes a message for the person. Its record stays as a tombstone until
// GC, so the same message cannot arrive again.
func (s *Store) Drop(key string) error {
	if _, err := s.Get(key); err != nil {
		return err
	}
	return mark(s.inbox(key+".dropped"), nil)
}

func (s *Store) claimFresh(key string) bool {
	data, err := os.ReadFile(s.inbox(key + ".claim"))
	if err != nil {
		return false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		// A claim being written right now reads empty: it is fresh.
		return true
	}
	return s.now().Sub(time.Unix(0, n)) < claimLease
}

// claim takes a message for one reader, retaking a stale claim.
func (s *Store) claim(key string) bool {
	path := s.inbox(key + ".claim")
	stamp := []byte(strconv.FormatInt(s.now().UnixNano(), 10))
	for range 2 {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, werr := f.Write(stamp)
			cerr := f.Close()
			return werr == nil && cerr == nil
		}
		if !errors.Is(err, fs.ErrExist) || s.claimFresh(key) {
			return false
		}
		// Stale: remove it and race the other stale-takers for a new one.
		_ = os.Remove(path)
	}
	return false
}

// Deliverable is the released, unread mail for f, oldest first.
func (s *Store) Deliverable(f Filter) []Entry {
	var out []Entry
	for _, e := range s.List() {
		if e.Released && !e.Read && f.match(e) && !s.claimFresh(e.Key) {
			out = append(out, e)
		}
	}
	return out
}

// Deliver hands the deliverable mail for f to write and marks it read once
// write succeeds. Each message goes to one reader: Deliver claims it first.
// When write fails the claims are given back and nothing is marked.
func (s *Store) Deliver(f Filter, write func([]Entry) error) (int, error) {
	var claimed []Entry
	for _, e := range s.Deliverable(f) {
		if !s.claim(e.Key) {
			continue
		}
		// Another reader may have delivered it between the listing and the
		// claim: it marks read before it gives its claim back, so a claim
		// taken after that sees the mark.
		if exists(s.inbox(e.Key + ".read")) {
			_ = os.Remove(s.inbox(e.Key + ".claim"))
			continue
		}
		claimed = append(claimed, e)
	}
	if len(claimed) == 0 {
		return 0, nil
	}
	if err := write(claimed); err != nil {
		for _, e := range claimed {
			_ = os.Remove(s.inbox(e.Key + ".claim"))
		}
		return 0, err
	}
	var firstErr error
	for _, e := range claimed {
		if err := mark(s.inbox(e.Key+".read"), nil); err != nil && firstErr == nil {
			firstErr = err
		}
		_ = os.Remove(s.inbox(e.Key + ".claim"))
	}
	return len(claimed), firstErr
}

// RecordSent notes a sent message, and that this person sent into its thread
// to that recipient.
func (s *Store) RecordSent(r SentRecord) error {
	to, err := ParseIdentity(r.To)
	if err != nil {
		return err
	}
	if !ValidID(r.Msg.ID) || !ValidID(r.Msg.Thread) {
		return errors.New("sent message has an invalid id")
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := createOnce(s.sent(r.Msg.ID+".json"), data); err != nil {
		return err
	}
	return mark(s.sent(r.Msg.Thread+"-"+to.MailboxID()+".to"), []byte(strconv.FormatInt(s.now().UnixNano(), 10)))
}

// SentInto reports whether this person sent into thread, to from.
func (s *Store) SentInto(thread string, from Identity) bool {
	return ValidID(thread) && exists(s.sent(thread+"-"+from.MailboxID()+".to"))
}

// PendingBox is mail from an identity not yet in peers, kept unopened.
type PendingBox struct {
	RelayID    string    `json:"-"`
	From       string    `json:"from"`
	ReceivedAt time.Time `json:"received_at"`
	Box        []byte    `json:"box"`
}

// SavePending keeps a box until the person adds its sender, or it expires.
func (s *Store) SavePending(relayID string, from Identity, box []byte, now time.Time) error {
	if !ValidID(relayID) {
		return errors.New("invalid relay id")
	}
	if len(s.Pending()) >= maxPending {
		return fmt.Errorf("already holding %d messages from senders not in peers", maxPending)
	}
	data, err := json.Marshal(PendingBox{From: from.String(), ReceivedAt: now, Box: box})
	if err != nil {
		return err
	}
	_, err = createOnce(s.pending(relayID+".json"), data)
	return err
}

// Pending is every kept box, oldest first.
func (s *Store) Pending() []PendingBox {
	names, _ := os.ReadDir(filepath.Join(s.dir, "pending"))
	var out []PendingBox
	for _, n := range names {
		id, ok := strings.CutSuffix(n.Name(), ".json")
		if !ok || !ValidID(id) {
			continue
		}
		data, err := os.ReadFile(s.pending(n.Name()))
		if err != nil {
			continue
		}
		var p PendingBox
		if json.Unmarshal(data, &p) != nil {
			continue
		}
		p.RelayID = id
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b PendingBox) int { return a.ReceivedAt.Compare(b.ReceivedAt) })
	return out
}

// RemovePending forgets a kept box.
func (s *Store) RemovePending(relayID string) {
	if ValidID(relayID) {
		_ = os.Remove(s.pending(relayID + ".json"))
	}
}

// GC removes what can no longer matter: messages and sent records older than
// MaxMessageAge, kept boxes older than MaxRelayTTL, and temporary files a
// killed process left. A file it cannot remove now is left for the next run.
func (s *Store) GC() {
	now := s.now()
	for _, e := range s.all() {
		if now.Sub(e.Msg.SentAt) <= MaxMessageAge {
			continue
		}
		for _, suffix := range []string{".released", ".read", ".claim", ".dropped", ".json"} {
			_ = os.Remove(s.inbox(e.Key + suffix))
		}
	}
	for _, p := range s.Pending() {
		if now.Sub(p.ReceivedAt) > MaxRelayTTL {
			s.RemovePending(p.RelayID)
		}
	}
	names, _ := os.ReadDir(filepath.Join(s.dir, "sent"))
	for _, n := range names {
		info, err := n.Info()
		if err != nil {
			continue
		}
		old := now.Sub(info.ModTime()) > MaxMessageAge
		if strings.HasSuffix(n.Name(), ".to") {
			if data, err := os.ReadFile(s.sent(n.Name())); err == nil {
				if ns, err := strconv.ParseInt(string(data), 10, 64); err == nil {
					old = now.Sub(time.Unix(0, ns)) > MaxMessageAge
				}
			}
		}
		if old {
			_ = os.Remove(s.sent(n.Name()))
		}
	}
	for _, sub := range []string{"inbox", "sent", "pending"} {
		names, _ := os.ReadDir(filepath.Join(s.dir, sub))
		for _, n := range names {
			if !strings.HasPrefix(n.Name(), ".tmp-") {
				continue
			}
			if info, err := n.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
				_ = os.Remove(filepath.Join(s.dir, sub, n.Name()))
			}
		}
	}
}
