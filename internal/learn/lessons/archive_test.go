package lessons

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/tarpack"
)

// TestArchiveMatchesFiles holds lessons.tar.gz to tracks.json and tracks/. The
// lint and the schema read the files, and the binary reads the archive, so a
// track edited without go generate would pass the lint and ship the old lesson.
func TestArchiveMatchesFiles(t *testing.T) {
	packed, err := tarpack.Unpack(archive)
	if err != nil {
		t.Fatal(err)
	}
	disk := map[string][]byte{}
	for _, root := range []string{"tracks.json", "tracks"} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(p)
			disk[filepath.ToSlash(p)] = data
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(packed) != len(disk) {
		t.Errorf("the archive holds %d files and the folder %d; run go generate ./internal/learn/lessons", len(packed), len(disk))
	}
	for name, want := range disk {
		if !bytes.Equal(packed[name], want) {
			t.Errorf("%s differs from its copy in the archive; run go generate ./internal/learn/lessons", name)
		}
	}
}
