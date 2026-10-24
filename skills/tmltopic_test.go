package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/skills/internal/tmlref"
)

// TestTMLTopicMatchesCatalog holds the component list of the tml topic to the
// TML catalog. An agent writes surfaces from that list, so a component or an
// attribute the catalog gains, loses or renames must reach the topic. It
// fails when tml/catalog.go changes without go generate ./skills.
func TestTMLTopicMatchesCatalog(t *testing.T) {
	path := filepath.Join("tuios", "tml.md")
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := tmlref.Splice(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(doc, want) {
		t.Fatalf("%s: the component list differs from tml/catalog.go; run go generate ./skills\n--- want ---\n%s", path, tmlref.Section())
	}
}
