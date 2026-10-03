package learnssh

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// store keeps what outlives a session, in small JSON files in the state
// directory: personal bests by key, the leaderboard, and the counters.
//
// Nothing here can tell who a person is. A best is filed under a salted hash
// of the SSH key's fingerprint, with a salt that never leaves the server. A
// name is on the board only when its owner chose to show it.
type store struct {
	dir  string
	salt []byte

	mu    sync.Mutex
	bests map[string]*bestRow
	board map[string][]boardRow
	stats Stats
	dirty bool
	block []string
}

type bestRow struct {
	Ms   map[string]int64 `json:"ms"`
	Seen int64            `json:"seen"` // unix day, to drop the oldest first
}

type boardRow struct {
	Name   string `json:"name"`
	Ms     int64  `json:"ms"`
	Owner  string `json:"owner"`
	Public bool   `json:"public"`
	When   int64  `json:"when"`
}

// Stats are the aggregate counters. They carry no address and no name.
type Stats struct {
	Sessions      int64            `json:"sessions"`
	Keyed         int64            `json:"keyed"`
	PeakActive    int64            `json:"peakActive"`
	TracksDone    map[string]int64 `json:"tracksDone"`
	Challenges    map[string]int64 `json:"challenges"`
	Refused       map[string]int64 `json:"refused"`
	Ended         map[string]int64 `json:"ended"`
	SessionSecs   int64            `json:"sessionSecs"`
	Since         string           `json:"since"`
	LastWrittenAt string           `json:"lastWrittenAt,omitempty"`
}

const (
	maxBests   = 20000
	boardSize  = 10
	fileBests  = "bests.json"
	fileBoard  = "board.json"
	fileStats  = "stats.json"
	fileSalt   = "salt"
	fileBlock  = "blocklist.txt"
	minBoardMs = 1000 // faster than this is a script, not a person
)

func openStore(dir string) (*store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &store{dir: dir, bests: map[string]*bestRow{}, board: map[string][]boardRow{}}
	salt, err := os.ReadFile(filepath.Join(dir, fileSalt))
	if err != nil || len(salt) < 16 {
		salt = make([]byte, 32)
		_, _ = rand.Read(salt)
		if err := writeAtomic(filepath.Join(dir, fileSalt), salt); err != nil {
			return nil, err
		}
	}
	s.salt = salt
	readJSON(filepath.Join(dir, fileBests), &s.bests)
	readJSON(filepath.Join(dir, fileBoard), &s.board)
	readJSON(filepath.Join(dir, fileStats), &s.stats)
	if s.stats.Since == "" {
		s.stats.Since = time.Now().UTC().Format(time.RFC3339)
	}
	for _, m := range []*map[string]int64{&s.stats.TracksDone, &s.stats.Challenges, &s.stats.Refused, &s.stats.Ended} {
		if *m == nil {
			*m = map[string]int64{}
		}
	}
	s.block = defaultBlocklist
	if b, err := os.ReadFile(filepath.Join(dir, fileBlock)); err == nil {
		for _, w := range strings.Fields(strings.ToLower(string(b))) {
			s.block = append(s.block, w)
		}
	}
	return s, nil
}

func readJSON(path string, v any) {
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, v)
	}
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// owner is the salted hash a key's bests are filed under.
func (s *store) owner(fingerprint string) string {
	h := sha256.Sum256(append(append([]byte(nil), s.salt...), fingerprint...))
	return hex.EncodeToString(h[:12])
}

func today() int64 { return time.Now().Unix() / 86400 }

// bestsFor is the owner's personal bests.
func (s *store) bestsFor(owner string) map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int64{}
	if r := s.bests[owner]; r != nil {
		for k, v := range r.Ms {
			out[k] = v
		}
	}
	return out
}

// record files a challenge time. keyed says the owner is a key hash and the
// best persists; otherwise owner is the session and only the board keeps it.
// It returns the owner's best and the time's place on the board, 0 if off it.
func (s *store) record(owner string, keyed bool, name, id string, ms int64) (best int64, rank int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true
	s.stats.Challenges[id]++
	best = ms
	if keyed {
		r := s.bests[owner]
		if r == nil {
			if len(s.bests) >= maxBests {
				s.dropOldestLocked()
			}
			r = &bestRow{Ms: map[string]int64{}}
			s.bests[owner] = r
		}
		r.Seen = today()
		if old := r.Ms[id]; old == 0 || ms < old {
			r.Ms[id] = ms
		}
		best = r.Ms[id]
	}
	if ms < minBoardMs {
		return best, 0
	}
	rows := s.board[id]
	// One row per owner: their best.
	if i := slices.IndexFunc(rows, func(r boardRow) bool { return r.Owner == owner }); i >= 0 {
		if rows[i].Ms <= ms {
			return best, i + 1
		}
		rows = slices.Delete(rows, i, i+1)
	}
	row := boardRow{Name: name, Ms: ms, Owner: owner, When: time.Now().Unix()}
	at, _ := slices.BinarySearchFunc(rows, ms, func(r boardRow, t int64) int {
		if r.Ms <= t {
			return -1
		}
		return 1
	})
	if at >= boardSize {
		s.board[id] = rows
		return best, 0
	}
	rows = slices.Insert(rows, at, row)
	if len(rows) > boardSize {
		rows = rows[:boardSize]
	}
	s.board[id] = rows
	return best, at + 1
}

// publish shows the owner's name on their rows.
func (s *store) publish(owner, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.board[id] {
		if s.board[id][i].Owner == owner {
			s.board[id][i].Public = true
			s.dirty = true
		}
	}
}

func (s *store) dropOldestLocked() {
	var oldest string
	var day int64 = 1 << 62
	for k, r := range s.bests {
		if r.Seen < day {
			oldest, day = k, r.Seen
		}
	}
	delete(s.bests, oldest)
}

// publicBoard is the board as a session sees it: a name only where its
// owner chose to show it.
func (s *store) publicBoard() Board {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Board{}
	for id, rows := range s.board {
		for _, r := range rows {
			name := "anonymous"
			if r.Public {
				name = r.Name
			}
			out[id] = append(out[id], Entry{Name: name, Ms: r.Ms})
		}
	}
	return out
}

// count changes the counters under the lock.
func (s *store) count(fn func(*Stats)) {
	s.mu.Lock()
	fn(&s.stats)
	s.dirty = true
	s.mu.Unlock()
}

func (s *store) snapshot() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stats
	st.TracksDone = clone(s.stats.TracksDone)
	st.Challenges = clone(s.stats.Challenges)
	st.Refused = clone(s.stats.Refused)
	st.Ended = clone(s.stats.Ended)
	return st
}

func clone(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// flush writes the files if anything changed.
func (s *store) flush() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	s.dirty = false
	s.stats.LastWrittenAt = time.Now().UTC().Format(time.RFC3339)
	bests, _ := json.Marshal(s.bests)
	board, _ := json.MarshalIndent(s.board, "", " ")
	stats, _ := json.MarshalIndent(s.stats, "", " ")
	s.mu.Unlock()
	for name, b := range map[string][]byte{fileBests: bests, fileBoard: board, fileStats: stats} {
		if err := writeAtomic(filepath.Join(s.dir, name), b); err != nil {
			return err
		}
	}
	return nil
}

// boardName is an SSH user name as the board may show it: lower case,
// [a-z0-9_-], at most 16 characters, and "anonymous" when nothing is left or
// it holds a word from the block list.
func (s *store) boardName(user string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(user) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
		if b.Len() == 16 {
			break
		}
	}
	name := b.String()
	if name == "" {
		return "anonymous"
	}
	squashed := strings.NewReplacer("_", "", "-", "", "0", "o", "1", "i", "3", "e", "4", "a", "5", "s", "7", "t").Replace(name)
	for _, w := range s.block {
		if w != "" && (strings.Contains(name, w) || strings.Contains(squashed, w)) {
			return "anonymous"
		}
	}
	return name
}

// defaultBlocklist keeps the worst words off the board. Names are hidden
// unless their owner shows them, so this is the second wall. The operator
// adds words in blocklist.txt in the state directory.
var defaultBlocklist = []string{
	"fuck", "shit", "cunt", "nigg", "fag", "rape", "nazi", "hitler", "porn",
	"penis", "pussy", "bitch", "whore", "slut", "cock", "dick", "kike", "retard",
	"admin", "root", "tuios", "official", "support",
}
