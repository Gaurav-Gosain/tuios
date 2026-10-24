//go:build ignore

// gentml.go writes the component list of skills/tuios/tml.md from the TML
// catalog. go generate ./skills runs it before gen.go packs the archive.
// TestTMLTopicMatchesCatalog fails when the two drift.
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/skills/internal/tmlref"
)

func main() {
	path := filepath.Join("tuios", "tml.md")
	doc, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	out, err := tmlref.Splice(doc)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		log.Fatal(err)
	}
}
