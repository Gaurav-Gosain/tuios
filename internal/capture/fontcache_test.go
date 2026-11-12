package capture

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

// cacheFixture is a font folder with one Go Mono and a cache file beside it.
type cacheFixture struct {
	t     *testing.T
	dir   string
	cache string
}

func newCacheFixture(t *testing.T) *cacheFixture {
	t.Helper()
	f := &cacheFixture{t: t, dir: t.TempDir(), cache: filepath.Join(t.TempDir(), "tuios", "fonts-index.gob")}
	f.write("mono.ttf", gomono.TTF, time.Unix(1_700_000_000, 0))
	return f
}

func (f *cacheFixture) write(name string, data []byte, mtime time.Time) {
	f.t.Helper()
	p := filepath.Join(f.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		f.t.Fatal(err)
	}
}

func (f *cacheFixture) index() *fontIndex { return indexFonts([]string{f.dir}, f.cache) }

func (f *cacheFixture) has(idx *fontIndex, family string) bool {
	_, ok := idx.byFamily[foldFontName(family)]
	return ok
}

// padded returns data grown with zero bytes to n bytes, which a font parser
// reads as the same font.
func padded(data []byte, n int) []byte {
	return append(bytes.Clone(data), make([]byte, n-len(data))...)
}

// TestFontCacheRebuildsWhenTheFontsChange holds the fingerprint to the changes
// a font folder sees. A font added, removed, replaced by a file of another
// size, replaced by a file of the same size at another time, put in a new
// folder, and a different list of folders must each give the index of the
// fonts as they are, not of the fonts the file was made from.
//
// How it could pass wrongly, written down first:
//   - A rebuilt index could equal the cached one by luck. Each case changes
//     which families are there, and the test reads the family.
//   - The first build could read no cache because it wrote none. It checks the
//     file is there before the change.
//
// Negative controls: a fingerprint with no sizes failed the replace case, one
// with no times failed the same-size case, and a load that took any file as
// current failed every case.
func TestFontCacheRebuildsWhenTheFontsChange(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name   string
		change func(f *cacheFixture)
		family string // what the index must now hold
		gone   string // and what it must no longer hold
	}{
		{"a font added", func(f *cacheFixture) { f.write("reg.ttf", goregular.TTF, t0) }, "Go", ""},
		{"a font removed", func(f *cacheFixture) {
			if err := os.Remove(filepath.Join(f.dir, "mono.ttf")); err != nil {
				f.t.Fatal(err)
			}
		}, "", "Go Mono"},
		{"a font replaced by one of another size", func(f *cacheFixture) { f.write("mono.ttf", goregular.TTF, t0) }, "Go", "Go Mono"},
		{"a font replaced by one of the same size at another time", func(f *cacheFixture) {
			n := max(len(gomono.TTF), len(goregular.TTF))
			// The first file was padded to this size too, so only the time differs.
			f.write("mono.ttf", padded(goregular.TTF, n), t0.Add(time.Hour))
		}, "Go", "Go Mono"},
		{"a font in a new folder", func(f *cacheFixture) { f.write("sub/more/bold.ttf", gobold.TTF, t0) }, "Go", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCacheFixture(t)
			if tc.name == "a font replaced by one of the same size at another time" {
				// Both files are the same size from the start.
				n := max(len(gomono.TTF), len(goregular.TTF))
				f.write("mono.ttf", padded(gomono.TTF, n), t0)
			}
			before := f.index()
			if !f.has(before, "Go Mono") {
				t.Fatal("the first index has no Go Mono")
			}
			if _, err := os.Stat(f.cache); err != nil {
				t.Fatalf("the first build wrote no cache: %v", err)
			}
			tc.change(f)
			after := f.index()
			if tc.family != "" && !f.has(after, tc.family) {
				t.Errorf("the index after the change has no %q: it was taken from the file", tc.family)
			}
			if tc.gone != "" && f.has(after, tc.gone) {
				t.Errorf("the index after the change still has %q: it was taken from the file", tc.gone)
			}
		})
	}
	t.Run("a different list of folders", func(t *testing.T) {
		f := newCacheFixture(t)
		_ = f.index()
		other := t.TempDir()
		if err := os.WriteFile(filepath.Join(other, "reg.ttf"), goregular.TTF, 0o644); err != nil {
			t.Fatal(err)
		}
		idx := indexFonts([]string{other}, f.cache)
		if f.has(idx, "Go Mono") || !f.has(idx, "Go") {
			t.Errorf("an index of another folder came from the file made for the first: %v", keys(idx.byFamily))
		}
	})
}

func keys(m map[string][]indexedFace) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestFontCacheOpensNoFontOnAHit holds the point of it: when the fingerprint
// matches, no font file is read. A file rewritten in place with the same size
// and the same modification time is not noticed, which is the gap the
// fingerprint accepts (see fontcache.go); the test uses it as the proof that
// nothing was read, and so states the gap.
//
// Negative control: with no file kept the second index read the rewritten font
// and had no Go Mono.
func TestFontCacheOpensNoFontOnAHit(t *testing.T) {
	f := newCacheFixture(t)
	first := f.index()
	p := filepath.Join(f.dir, "mono.ttf")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// The same size, the same time, other bytes: not a font.
	if err := os.WriteFile(p, bytes.Repeat([]byte{0xAB}, int(info.Size())), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second := f.index()
	if !f.has(first, "Go Mono") || !f.has(second, "Go Mono") {
		t.Fatalf("the second index has lost Go Mono: a font file was read on a hit (%v)", keys(second.byFamily))
	}
}

// TestFontCacheIsOnlyAShortcut holds the failure modes of the file: missing,
// empty, damaged, cut short, from another format number, made from other
// fonts. Each is a miss that gives the right index and leaves a good file; none
// is an error, and a cache folder that cannot be written gives the index too.
//
// Negative controls: a load that ignored the format number failed the
// other-format case, and a save error that was returned instead of dropped
// failed the unwritable case.
func TestFontCacheIsOnlyAShortcut(t *testing.T) {
	valid := func(f *cacheFixture) []byte {
		_ = f.index()
		b, err := os.ReadFile(f.cache)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	otherFormat := func(f *cacheFixture) []byte {
		w := fontWalk{seenDirs: map[string]bool{}, seenFiles: map[string]bool{}}
		w.dir(f.dir)
		var buf bytes.Buffer
		_ = gob.NewEncoder(&buf).Encode(fontIndexFile{Version: fontCacheVersion + 1, Fingerprint: fontFingerprint([]string{f.dir}, w.stamps), Faces: nil})
		return buf.Bytes()
	}
	for _, tc := range []struct {
		name string
		make func(f *cacheFixture) []byte
	}{
		{"empty", func(*cacheFixture) []byte { return nil }},
		{"not a cache", func(*cacheFixture) []byte { return []byte("this is not a gob") }},
		{"cut short", func(f *cacheFixture) []byte { b := valid(f); return b[:len(b)/2] }},
		{"another format number", otherFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCacheFixture(t)
			content := tc.make(f)
			if err := os.MkdirAll(filepath.Dir(f.cache), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.cache, content, 0o600); err != nil {
				t.Fatal(err)
			}
			idx := f.index()
			if !f.has(idx, "Go Mono") {
				t.Fatalf("the index has no Go Mono after a %s file", tc.name)
			}
			w := fontWalk{seenDirs: map[string]bool{}, seenFiles: map[string]bool{}}
			w.dir(f.dir)
			if _, ok := loadFontCache(f.cache, fontFingerprint([]string{f.dir}, w.stamps)); !ok {
				t.Errorf("the file was not written again after a %s file", tc.name)
			}
		})
	}
	t.Run("a cache folder that cannot be written", func(t *testing.T) {
		f := newCacheFixture(t)
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		idx := indexFonts([]string{f.dir}, filepath.Join(blocker, "tuios", "fonts-index.gob"))
		if !f.has(idx, "Go Mono") {
			t.Fatal("the index has no Go Mono when the cache folder cannot be written")
		}
	})
	t.Run("no cache folder", func(t *testing.T) {
		f := newCacheFixture(t)
		if idx := indexFonts([]string{f.dir}, ""); !f.has(idx, "Go Mono") {
			t.Fatal("the index has no Go Mono with no cache path")
		}
	})
}

// TestFontCacheSurvivesTwoWriters holds the atomic write: several processes
// that all scan at once, and so all write, leave one whole file that the next
// one reads as current.
func TestFontCacheSurvivesTwoWriters(t *testing.T) {
	f := newCacheFixture(t)
	f.write("reg.ttf", goregular.TTF, time.Unix(1_700_000_000, 0))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _ = f.index() })
	}
	wg.Wait()
	w := fontWalk{seenDirs: map[string]bool{}, seenFiles: map[string]bool{}}
	w.dir(f.dir)
	faces, ok := loadFontCache(f.cache, fontFingerprint([]string{f.dir}, w.stamps))
	if !ok || len(faces) != 2 {
		t.Fatalf("after eight writers the file reads as ok=%v with %d faces, want a whole file of 2", ok, len(faces))
	}
}
