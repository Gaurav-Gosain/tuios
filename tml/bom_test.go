package tml

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoLiteralBOM fails on a literal U+FEFF in a Go file of this module
// after its first bytes. Go refuses such a file ("illegal byte order mark"),
// and some editors and tools turn a \uFEFF escape into the literal
// character. Write the escape "\xef\xbb\xbf" instead.
func TestNoLiteralBOM(t *testing.T) {
	bom := []byte("\xef\xbb\xbf")
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if i := bytes.Index(bytes.TrimPrefix(src, bom), bom); i >= 0 {
			t.Errorf("%s holds a literal byte order mark. Write \\xef\\xbb\\xbf instead.", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
