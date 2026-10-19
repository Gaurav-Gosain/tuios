package transcriptview

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestOpenCodeServiceRules holds the read to its limits after review: the
// password goes to no connection this user cannot be shown to hold, the export
// runs only when no service is there, one fetch serves the reads of a
// session for a moment, and an export that runs too long is killed with the
// processes it started.
func TestOpenCodeServiceRules(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("testdata", "opencode-messages.json"))
	if err != nil {
		t.Fatal(err)
	}
	export, err := filepath.Abs(filepath.Join("testdata", "opencode-export.json"))
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string) (Page, error) {
		return Read(path, Options{Format: FormatOpenCode, Session: ocSession})
	}
	// countingExport is an opencode whose export counts its runs in a file.
	countingExport := func(t *testing.T, extra string) string {
		t.Helper()
		dir := t.TempDir()
		runs := filepath.Join(dir, "runs")
		fakeExport(t, dir, "echo run >> "+runs+"; "+extra+"cat "+export)
		return runs
	}
	runCount := func(runs string) int {
		data, _ := os.ReadFile(runs)
		return strings.Count(string(data), "run")
	}

	t.Run("a port another user holds", func(t *testing.T) {
		f := newFakeOpenCode(t, page)
		asOtherUser(t)
		runs := countingExport(t, "")
		p, err := read(writeService(t, f.srv.URL, f.pid, 0o600))
		if err != nil {
			t.Fatal(err)
		}
		if n := f.hits.Load(); n != 0 {
			t.Fatalf("%d requests carried the password to a port another user holds", n)
		}
		if runCount(runs) != 1 || len(p.Entries) == 0 {
			t.Fatalf("the export ran %d times and gave %d entries, want once, with the session", runCount(runs), len(p.Entries))
		}
	})
	t.Run("a service that is there but fails", func(t *testing.T) {
		f := newFakeOpenCode(t, []byte("not json"))
		runs := countingExport(t, "")
		if _, err := read(writeService(t, f.srv.URL, f.pid, 0o600)); err != ErrService {
			t.Fatalf("err %v, want ErrService", err)
		}
		if runCount(runs) != 0 {
			t.Fatal("the export ran for a service that is there")
		}
	})
	t.Run("reads of one session share a fetch", func(t *testing.T) {
		runs := countingExport(t, "sleep 0.3; ")
		path := filepath.Join(t.TempDir(), "service.json")
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				if _, err := read(path); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if _, err := read(path); err != nil {
			t.Fatal(err)
		}
		if n := runCount(runs); n != 1 {
			t.Fatalf("five reads ran the export %d times, want once", n)
		}
	})
	t.Run("an export that runs too long", func(t *testing.T) {
		dir := t.TempDir()
		pids := filepath.Join(dir, "pids")
		// The export starts a child, as opencode starts its private server,
		// and both write their pids.
		fakeExport(t, dir, "echo $$ >> "+pids+"; sleep 30 & echo $! >> "+pids+"; wait")
		start := time.Now()
		if _, err := read(filepath.Join(t.TempDir(), "service.json")); err != ErrService {
			t.Fatalf("err %v, want ErrService", err)
		}
		if took := time.Since(start); took > openCodeReadBudget+time.Second {
			t.Fatalf("the read took %v, past its budget", took)
		}
		data, _ := os.ReadFile(pids)
		for _, pid := range strings.Fields(string(data)) {
			deadline := time.Now().Add(2 * time.Second)
			for {
				if _, err := os.Stat("/proc/" + pid); err != nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("process %s of the export outlived the read", pid)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	})
	t.Run("a read before a cursor", func(t *testing.T) {
		noOpenCode(t)
		f := newFakeOpenCode(t, page)
		p, err := Read(writeService(t, f.srv.URL, f.pid, 0o600), Options{Format: FormatOpenCode, Session: ocSession, Before: "oc1"})
		if err != nil || len(p.Entries) != 0 || p.More || f.hits.Load() != 0 {
			t.Fatalf("got %d entries, more %v, %d requests, err %v; want an empty page and no request", len(p.Entries), p.More, f.hits.Load(), err)
		}
	})
	t.Run("an address with a trailing slash", func(t *testing.T) {
		noOpenCode(t)
		f := newFakeOpenCode(t, page)
		p, err := read(writeService(t, f.srv.URL+"/", f.pid, 0o600))
		if err != nil {
			t.Fatal(err)
		}
		checkOpenCodePage(t, p)
	})
	t.Run("an export is used again while the database is unchanged", func(t *testing.T) {
		data := t.TempDir()
		t.Setenv("XDG_DATA_HOME", data)
		t.Setenv("OPENCODE_DB", "")
		db := filepath.Join(data, "opencode", "opencode.db")
		if err := os.MkdirAll(filepath.Dir(db), 0o700); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(db, []byte("db"), 0o600)
		runs := countingExport(t, "")
		path := filepath.Join(t.TempDir(), "service.json")
		// Each read past the shared fetch, as a read more than openCodeShare
		// after the last one.
		readAfterShare := func() {
			t.Helper()
			openCodeFlights.Lock()
			clear(openCodeFlights.m)
			openCodeFlights.Unlock()
			if _, err := read(path); err != nil {
				t.Fatal(err)
			}
		}
		readAfterShare()
		readAfterShare()
		if n := runCount(runs); n != 1 {
			t.Fatalf("two reads of a quiet session ran the export %d times, want once", n)
		}
		// opencode writes to its write-ahead log.
		_ = os.WriteFile(db+"-wal", []byte("frame"), 0o600)
		readAfterShare()
		if n := runCount(runs); n != 2 {
			t.Fatalf("a read after a write ran the export %d times in all, want twice", n)
		}
		// A database of another channel counts too.
		_ = os.WriteFile(filepath.Join(data, "opencode", "opencode-beta.db"), []byte("db"), 0o600)
		readAfterShare()
		if n := runCount(runs); n != 3 {
			t.Fatalf("a read after a write to another channel's database ran the export %d times in all, want 3", n)
		}
		// With no database to watch, every read exports.
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		readAfterShare()
		readAfterShare()
		if n := runCount(runs); n != 5 {
			t.Fatalf("reads with no database ran the export %d times in all, want 5", n)
		}
	})
}
