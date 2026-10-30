package integration

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestAssetArchiveMatchesFiles holds assets.tar.gz to assets/. It fails when a
// plugin file is edited without go generate, which would install the old one.
func TestAssetArchiveMatchesFiles(t *testing.T) {
	packed := assets()
	n := 0
	err := filepath.WalkDir("assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		want, err := os.ReadFile(p)
		if !bytes.Equal(packed[filepath.ToSlash(p)], want) {
			t.Errorf("%s differs from its copy in the archive; run go generate ./internal/integration", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(packed) != n {
		t.Errorf("the archive holds %d files and assets/ %d; run go generate ./internal/integration", len(packed), n)
	}
}
