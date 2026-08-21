package applist

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tempFrecency(t *testing.T) *Frecency {
	t.Helper()
	return LoadFrecency(filepath.Join(t.TempDir(), "launcher.json"))
}

func TestBoostRisesWithUse(t *testing.T) {
	f := tempFrecency(t)
	if got := f.Boost("gcc"); got != 0 {
		t.Fatalf("Boost of an unused name = %d, want 0", got)
	}
	f.Note("gcc")
	first := f.Boost("gcc")
	if first <= 0 {
		t.Fatalf("Boost after one launch = %d, want a positive boost", first)
	}
	f.Note("gcc")
	if second := f.Boost("gcc"); second <= first {
		t.Fatalf("Boost after two launches = %d, want more than %d", second, first)
	}
}

func TestBoostDecaysWithAge(t *testing.T) {
	now := time.Now()
	brackets := []struct {
		name string
		age  time.Duration
	}{
		{"an hour", 30 * time.Minute},
		{"a day", 5 * time.Hour},
		{"a week", 3 * 24 * time.Hour},
		{"older", 30 * 24 * time.Hour},
	}
	prev := MaxBoost + 1
	for _, b := range brackets {
		r := record{Count: 1, Last: now.Add(-b.age).Unix()}
		got := boostFor(r, now.Unix())
		if got >= prev {
			t.Fatalf("boost within %s = %d, want less than the fresher bracket's %d", b.name, got, prev)
		}
		if got < 0 {
			t.Fatalf("boost within %s = %d, want a non-negative boost", b.name, got)
		}
		prev = got
	}
	if prev == 0 {
		t.Error("a program used once long ago should still outrank one never used")
	}
}

// TestBoostIsCapped is what keeps frecency a tiebreaker: no launch history may
// grow large enough to outrank a clearly better match.
func TestBoostIsCapped(t *testing.T) {
	f := tempFrecency(t)
	for range 200 {
		f.Note("htop")
	}
	if got := f.Boost("htop"); got != MaxBoost {
		t.Fatalf("Boost after 200 launches = %d, want it capped at %d", got, MaxBoost)
	}
}

func TestFrecencyRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher.json")
	f := LoadFrecency(path)
	f.Note("nvim")
	f.Note("nvim")
	want := f.Boost("nvim")

	reloaded := LoadFrecency(path)
	if got := reloaded.Boost("nvim"); got != want {
		t.Fatalf("Boost after reload = %d, want %d", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("history mode = %o, want 600", perm)
	}
}

func TestFrecencySurvivesCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := LoadFrecency(path)
	if got := f.Boost("anything"); got != 0 {
		t.Fatalf("Boost = %d from a corrupt history, want 0", got)
	}
	f.Note("gcc")
	if f.Boost("gcc") == 0 {
		t.Error("a corrupt history must not stop a new one being recorded")
	}
}

func TestFrecencyIgnoresNewerVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher.json")
	body := fmt.Sprintf(`{"version":%d,"apps":{"gcc":{"n":9,"t":%d}}}`, frecencyVersion+1, time.Now().Unix())
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadFrecency(path).Boost("gcc"); got != 0 {
		t.Fatalf("Boost = %d from a future format, want it left alone", got)
	}
}

// TestFrecencyPrunes keeps the file from growing without bound while holding on
// to the records a person would notice losing.
func TestFrecencyPrunes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher.json")
	f := LoadFrecency(path)

	f.mu.Lock()
	old := time.Now().Add(-90 * 24 * time.Hour).Unix()
	for i := range maxRecords + 100 {
		f.recs[fmt.Sprintf("stale-%d", i)] = record{Count: 1, Last: old}
	}
	f.mu.Unlock()

	f.Note("favourite")

	reloaded := LoadFrecency(path)
	reloaded.mu.Lock()
	got := len(reloaded.recs)
	reloaded.mu.Unlock()
	if got > maxRecords {
		t.Fatalf("history kept %d records, want at most %d", got, maxRecords)
	}
	if reloaded.Boost("favourite") == 0 {
		t.Error("pruning dropped the most recently used entry")
	}
}

func TestDefaultPathIsUnderState(t *testing.T) {
	got := DefaultPath()
	if !strings.HasSuffix(got, filepath.Join("tuios", "launcher.json")) {
		t.Fatalf("DefaultPath = %q, want it under the tuios state directory", got)
	}
}
