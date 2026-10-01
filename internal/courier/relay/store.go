package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/courier"
)

// The store holds every mailbox in memory, and with a data directory also on
// disk, one file per message:
//
//	DATA/<mailbox id>/<message id>.msg
//
// Both names are hex the relay generated or derived, never a string a client
// chose, so no request can name a path outside the directory.

type message struct {
	ID         string
	From       courier.Identity
	ReceivedAt time.Time
	ExpiresAt  time.Time
	Box        []byte
}

type diskMessage struct {
	From       string    `json:"from"`
	ReceivedAt time.Time `json:"received_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Box        []byte    `json:"box"`
}

type mailbox struct {
	msgs  []*message
	bytes int64
	// changed is closed and replaced when mail arrives, waking every fetch
	// waiting on this mailbox.
	changed chan struct{}
}

// errFull is a send refused for space. Its text says which limit.
type errFull string

func (e errFull) Error() string { return string(e) }

type store struct {
	opts Options

	mu    sync.Mutex
	boxes map[string]*mailbox
	total int64
}

func openStore(opts Options) (*store, error) {
	s := &store{opts: opts, boxes: map[string]*mailbox{}}
	if opts.DataDir == "" {
		return s, nil
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return nil, err
	}
	dirs, err := os.ReadDir(opts.DataDir)
	if err != nil {
		return nil, err
	}
	now := opts.Now()
	for _, d := range dirs {
		if !d.IsDir() || !courier.ValidID(d.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(opts.DataDir, d.Name()))
		if err != nil {
			opts.Logf("data: %v", err)
			continue
		}
		mb := s.box(d.Name())
		for _, f := range files {
			id, ok := strings.CutSuffix(f.Name(), ".msg")
			if !ok || !courier.ValidID(id) {
				continue
			}
			path := filepath.Join(opts.DataDir, d.Name(), f.Name())
			m, err := readDiskMessage(path, id)
			if err != nil {
				opts.Logf("data: skipping %s: %v", path, err)
				continue
			}
			if !now.Before(m.ExpiresAt) {
				_ = os.Remove(path)
				continue
			}
			mb.msgs = append(mb.msgs, m)
			mb.bytes += int64(len(m.Box))
			s.total += int64(len(m.Box))
		}
		slices.SortFunc(mb.msgs, func(a, b *message) int { return a.ReceivedAt.Compare(b.ReceivedAt) })
	}
	return s, nil
}

func readDiskMessage(path, id string) (*message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var dm diskMessage
	if err := json.Unmarshal(data, &dm); err != nil {
		return nil, err
	}
	from, err := courier.ParseIdentity(dm.From)
	if err != nil {
		return nil, err
	}
	if len(dm.Box) == 0 {
		return nil, errors.New("empty box")
	}
	return &message{ID: id, From: from, ReceivedAt: dm.ReceivedAt, ExpiresAt: dm.ExpiresAt, Box: dm.Box}, nil
}

// box returns the mailbox, creating it. The caller holds mu, or is openStore.
func (s *store) box(id string) *mailbox {
	mb := s.boxes[id]
	if mb == nil {
		mb = &mailbox{changed: make(chan struct{})}
		s.boxes[id] = mb
	}
	return mb
}

func (s *store) put(mailboxID string, from courier.Identity, box []byte, now time.Time) (*message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mb := s.box(mailboxID)
	size := int64(len(box))
	switch {
	case len(mb.msgs) >= s.opts.MaxPerMailbox:
		return nil, errFull(fmt.Sprintf("the mailbox holds %d messages, the most it may", len(mb.msgs)))
	case mb.bytes+size > s.opts.MaxMailboxBytes:
		return nil, errFull("the mailbox is out of space")
	case s.total+size > s.opts.MaxTotalBytes:
		return nil, errFull("the relay is out of space")
	}
	m := &message{ID: courier.NewID(), From: from, ReceivedAt: now, ExpiresAt: now.Add(s.opts.TTL), Box: box}
	if s.opts.DataDir != "" {
		if err := s.persist(mailboxID, m); err != nil {
			return nil, err
		}
	}
	mb.msgs = append(mb.msgs, m)
	mb.bytes += size
	s.total += size
	close(mb.changed)
	mb.changed = make(chan struct{})
	return m, nil
}

func (s *store) persist(mailboxID string, m *message) error {
	dir := filepath.Join(s.opts.DataDir, mailboxID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(diskMessage{From: m.From.String(), ReceivedAt: m.ReceivedAt, ExpiresAt: m.ExpiresAt, Box: m.Box})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp, filepath.Join(dir, m.ID+".msg"))
	}
	if werr != nil {
		_ = os.Remove(tmp)
	}
	return werr
}

// list returns up to limit unexpired messages, oldest first, and a channel
// that closes when more arrive.
func (s *store) list(mailboxID string, now time.Time, limit int) ([]*message, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mb := s.box(mailboxID)
	var out []*message
	for _, m := range mb.msgs {
		if len(out) == limit {
			break
		}
		if now.Before(m.ExpiresAt) {
			out = append(out, m)
		}
	}
	return out, mb.changed
}

// remove deletes the named messages from one mailbox and reports how many
// there were. An id in another mailbox is not found here.
func (s *store) remove(mailboxID string, ids []string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	mb := s.boxes[mailboxID]
	if mb == nil {
		return 0
	}
	n := 0
	mb.msgs = slices.DeleteFunc(mb.msgs, func(m *message) bool {
		if !slices.Contains(ids, m.ID) {
			return false
		}
		s.drop(mailboxID, mb, m)
		n++
		return true
	})
	return n
}

// expire deletes every message past its expiry and reports how many.
func (s *store) expire(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, mb := range s.boxes {
		mb.msgs = slices.DeleteFunc(mb.msgs, func(m *message) bool {
			if now.Before(m.ExpiresAt) {
				return false
			}
			s.drop(id, mb, m)
			n++
			return true
		})
	}
	return n
}

// drop accounts for and unlinks one message. The caller holds mu.
func (s *store) drop(mailboxID string, mb *mailbox, m *message) {
	mb.bytes -= int64(len(m.Box))
	s.total -= int64(len(m.Box))
	if s.opts.DataDir != "" {
		err := os.Remove(filepath.Join(s.opts.DataDir, mailboxID, m.ID+".msg"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.opts.Logf("data: %v", err)
		}
	}
}

func (s *store) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, mb := range s.boxes {
		n += len(mb.msgs)
	}
	return n
}
