package main

import (
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io"

	"github.com/egoist/mygo/ui"
)

// The fonts guios draws with: Inter for the chrome, JetBrains Mono for the
// panes. Both are under the SIL Open Font License (fonts/*-LICENSE.txt). They
// are kept gzipped, as the other fonts in this repository are, and registered
// once at start.
//
//go:embed fonts/*.ttf.gz
var fontFiles embed.FS

const (
	uiFamily   = "Inter"
	monoFamily = "JetBrains Mono"
)

var fontSet = []struct{ file, family string }{
	{"Inter-Regular", uiFamily},
	{"Inter-Medium", uiFamily},
	{"Inter-SemiBold", uiFamily},
	{"JetBrainsMono-Regular", monoFamily},
	{"JetBrainsMono-Bold", monoFamily},
	{"JetBrainsMono-Italic", monoFamily},
	{"JetBrainsMono-BoldItalic", monoFamily},
}

// registerFonts adds the bundled fonts to MyGo's font set.
func registerFonts() error {
	for _, f := range fontSet {
		gz, err := fontFiles.ReadFile("fonts/" + f.file + ".ttf.gz")
		if err != nil {
			return err
		}
		r, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			return fmt.Errorf("font %s: %w", f.file, err)
		}
		data, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("font %s: %w", f.file, err)
		}
		if err := ui.RegisterFont(data, f.family); err != nil {
			return fmt.Errorf("font %s: %w", f.file, err)
		}
	}
	return nil
}
