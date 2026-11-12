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

// TestRexLuaMatchesItsSource: the tuios.lua install writes into Rex's config
// folder is the one in integrations/rex, where its stub tests run. A change
// there that is not copied here would install a file the tests never saw.
func TestRexLuaMatchesItsSource(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "integrations", "rex", "tuios.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src, RexLua()) {
		t.Errorf("integrations/rex/tuios.lua differs from %s; copy it there and run go generate ./internal/integration", rexLuaAsset)
	}
}
