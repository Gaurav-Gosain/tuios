package applist

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/adrg/xdg"
)

// MaxBoost caps what a launch history can add to a match score.
//
// The cap is what keeps frecency a tiebreaker rather than a second ranking.
// pkg/fuzzy pays 16 for a matched character and 32 for an exact prefix, so a
// boost of at most 32 can lift a near-tie and cannot drag a program the query
// barely matches above one it matches exactly.
const MaxBoost = 32

// frecencyVersion is stamped on the file so a later format can recognise this
// one rather than guess at it.
const frecencyVersion = 1

// maxRecords bounds the file. A launch history is a convenience, and every
// record past the few hundred a person actually uses is dead weight that still
// has to be parsed on open.
const maxRecords = 512

type record struct {
	Count int   `json:"n"`
	Last  int64 `json:"t"` // unix seconds
}

type frecencyFile struct {
	Version int               `json:"version"`
	Apps    map[string]record `json:"apps"`
}

// Frecency ranks names by a blend of how often and how recently they were
// chosen, so the second time someone runs a program it is already at the top.
//
// It is safe for concurrent use. Every method is best effort: a launcher whose
// history cannot be read still launches things.
type Frecency struct {
	mu   sync.Mutex
	path string
	recs map[string]record
	// now is the clock, indirected so tests can age a record without sleeping.
	now func() time.Time
}

// DefaultPath is where tuios keeps the launch history. It is read at call time
// rather than at init so a test that redirects XDG_STATE_HOME is obeyed.
func DefaultPath() string {
	return filepath.Join(xdg.StateHome, "tuios", "launcher.json")
}

// LoadFrecency reads the history at path. A missing file is the ordinary
// first-run case and yields an empty history, not an error.
func LoadFrecency(path string) *Frecency {
	f := &Frecency{path: path, recs: make(map[string]record), now: time.Now}
	data, err := os.ReadFile(path)
	if err != nil {
		return f
	}
	var parsed frecencyFile
	if json.Unmarshal(data, &parsed) != nil || parsed.Version > frecencyVersion {
		return f
	}
	for name, r := range parsed.Apps {
		if r.Count > 0 {
			f.recs[name] = r
		}
	}
	return f
}

// Note records that name was chosen and persists the history.
func (f *Frecency) Note(name string) {
	if name == "" {
		return
	}
	f.mu.Lock()
	r := f.recs[name]
	r.Count++
	r.Last = f.now().Unix()
	f.recs[name] = r
	data, err := f.encodeLocked()
	f.mu.Unlock()
	if err == nil {
		writeAtomic(f.path, data)
	}
}

// Boost returns what name's history adds to a match score, between 0 and
// MaxBoost.
//
// The decay is by age bracket rather than by a continuous curve: brackets are
// integer arithmetic that cannot drift, and the difference between an hour ago
// and ninety minutes ago is not information a person has about their own habits.
func (f *Frecency) Boost(name string) int {
	f.mu.Lock()
	r, ok := f.recs[name]
	now := f.now().Unix()
	f.mu.Unlock()
	if !ok {
		return 0
	}
	return boostFor(r, now)
}

func boostFor(r record, now int64) int {
	const (
		hour = 3600
		day  = 24 * hour
		week = 7 * day
	)
	age := now - r.Last
	var s int
	switch {
	case age < hour:
		s = r.Count * 8
	case age < day:
		s = r.Count * 4
	case age < week:
		s = r.Count * 2
	default:
		s = r.Count
	}
	return min(s, MaxBoost)
}

// Save writes the history. Note already saves, so this is only for a caller
// batching several updates.
func (f *Frecency) Save() error {
	f.mu.Lock()
	data, err := f.encodeLocked()
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return writeAtomic(f.path, data)
}

// encodeLocked marshals the history, pruning to the records worth keeping.
func (f *Frecency) encodeLocked() ([]byte, error) {
	apps := f.recs
	if len(apps) > maxRecords {
		apps = f.pruneLocked()
		f.recs = apps
	}
	return json.Marshal(frecencyFile{Version: frecencyVersion, Apps: apps})
}

// pruneLocked keeps the strongest maxRecords entries, which is the set a person
// would notice losing.
func (f *Frecency) pruneLocked() map[string]record {
	now := f.now().Unix()
	type ranked struct {
		name  string
		boost int
	}
	all := make([]ranked, 0, len(f.recs))
	for name, r := range f.recs {
		all = append(all, ranked{name, boostFor(r, now)})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].boost != all[j].boost {
			return all[i].boost > all[j].boost
		}
		if f.recs[all[i].name].Last != f.recs[all[j].name].Last {
			return f.recs[all[i].name].Last > f.recs[all[j].name].Last
		}
		return all[i].name < all[j].name
	})
	kept := make(map[string]record, maxRecords)
	for _, r := range all[:maxRecords] {
		kept[r.name] = f.recs[r.name]
	}
	return kept
}

// writeAtomic replaces path in one step, following the pattern the session
// resurrection state uses. Two processes can share a launch history, and half a
// JSON file is a history that never loads again.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".launcher-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename succeeded
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
