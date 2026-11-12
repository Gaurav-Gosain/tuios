package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

// TestAnUnreadableDirectoryIsNotFatal keeps the walk forgiving. A font
// directory that cannot be read is one place with no fonts in it.
func TestAnUnreadableDirectoryIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "truncated.ttf")
	if err := os.WriteFile(junk, []byte("not a font"), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := scanFontIndex([]string{
		filepath.Join(dir, "does-not-exist"),
		dir,
		"/proc/nonexistent/fonts",
	})
	if idx == nil {
		t.Fatal("the scan gave up entirely")
	}
	if len(idx.byFamily) != 0 {
		t.Errorf("a directory of junk produced %d families", len(idx.byFamily))
	}
}

// TestTheScanFilesFacesInTheOrderOfTheWalk holds the parallel scan to the
// index a serial scan makes. Font files are read side by side, and a family's
// faces are listed in the order the walk met their files, which is what picks
// between two files that carry the same family: the first one wins. A scan
// that filed faces as the reads finished would pick differently from run to
// run.
//
// How it could pass wrongly, written down first:
//   - The files could be read in the order they are filed by luck. Forty files
//     of three families are read by sixteen readers, a spread that does not
//     keep one order by luck.
//   - It could compare the index with itself. The reference is made here, one
//     file after another, from the walk's own list.
//
// Negative control: filing the faces as the readers finish put the files of a
// family out of walk order and failed.
func TestTheScanFilesFacesInTheOrderOfTheWalk(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	fonts := [][]byte{gomono.TTF, goregular.TTF, gobold.TTF}
	for i := range 40 {
		at := dir
		if i%2 == 1 {
			at = sub
		}
		name := filepath.Join(at, fmt.Sprintf("f%02d.ttf", i))
		if err := os.WriteFile(name, fonts[i%len(fonts)], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.ttf"), []byte("not a font"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := fontWalk{seenDirs: map[string]bool{}, seenFiles: map[string]bool{}}
	w.dir(dir)
	if len(w.files) != 41 {
		t.Fatalf("the walk met %d files, want 41", len(w.files))
	}
	want := &fontIndex{byFamily: map[string][]indexedFace{}, byPostScript: map[string]indexedFace{}}
	for _, file := range w.files {
		for _, face := range readFontNames(file) {
			want.add(face)
		}
	}
	if len(want.byFamily["go mono"]) < 10 {
		t.Fatalf("the reference holds %d Go Mono faces, want one per file", len(want.byFamily["go mono"]))
	}
	for range 20 {
		got := scanFontIndex([]string{dir})
		if !reflect.DeepEqual(got.byFamily, want.byFamily) || !reflect.DeepEqual(got.byPostScript, want.byPostScript) {
			t.Fatalf("the parallel scan made another index than a serial one\n got  %v\n want %v", got.byFamily["go mono"], want.byFamily["go mono"])
		}
	}
}
