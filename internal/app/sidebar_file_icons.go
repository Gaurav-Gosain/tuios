package app

import (
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The files section's icons.
//
// Two layers, and which one draws is the whole of "it degrades".
//
// Underneath are three roles of the glyph set: folder, parent and file. They
// are settable like every other mark on the rail, they have ASCII spellings, and
// they are what a terminal running --ascii-only or a set that is 7-bit
// throughout draws. A listing is legible with nothing but those three.
//
// On top is this table: one nerd font codepoint per file type, from the private
// use area. It is off in ASCII mode and off when appearance.sidebar.file_icons
// is false, because a terminal without a patched font draws every one of them
// as a tofu box. printableTitle strips exactly these codepoints out of window
// titles for that reason; the difference is that a title's icon is whatever a
// foreign program put there, while these are ours and the user has said their
// font has them.
//
// The table is data rather than a switch on purpose. It is read once per drawn
// row, it will be edited far more often than the code around it, and a
// hundred-arm switch is a hundred places for an extension to be spelled twice.

// fileIconByExt is the icon for a file's extension, lowercased and without the
// dot. Grouped by what the icon means rather than alphabetically, so an
// extension is added next to the ones that already share its glyph.
var fileIconByExt = map[string]string{
	// Languages.
	"go":    "",
	"rs":    "",
	"py":    "",
	"js":    "",
	"mjs":   "",
	"cjs":   "",
	"jsx":   "",
	"ts":    "",
	"tsx":   "",
	"c":     "",
	"h":     "",
	"cpp":   "",
	"cc":    "",
	"hpp":   "",
	"lua":   "",
	"rb":    "",
	"java":  "",
	"php":   "",
	"swift": "",
	"zig":   "",
	"nix":   "",
	"vim":   "",
	"sql":   "",
	"db":    "",

	// Markup and styling.
	"html": "",
	"htm":  "",
	"css":  "",
	"scss": "",
	"md":   "",
	"mdx":  "",

	// Data and configuration.
	"json": "",
	"toml": "",
	"yaml": "",
	"yml":  "",
	"ini":  "",
	"conf": "",
	"env":  "",
	"lock": "",

	// Shells.
	"sh":   "",
	"bash": "",
	"zsh":  "",
	"fish": "",

	// Archives, media and documents.
	"zip":   "",
	"tar":   "",
	"gz":    "",
	"xz":    "",
	"zst":   "",
	"7z":    "",
	"rar":   "",
	"png":   "",
	"jpg":   "",
	"jpeg":  "",
	"gif":   "",
	"svg":   "",
	"webp":  "",
	"ico":   "",
	"mp3":   "",
	"wav":   "",
	"flac":  "",
	"ogg":   "",
	"mp4":   "",
	"mkv":   "",
	"mov":   "",
	"webm":  "",
	"pdf":   "",
	"txt":   "",
	"log":   "",
	"so":    "",
	"dll":   "",
	"dylib": "",
	"o":     "",
	"a":     "",
}

// fileIconByName is the icon for a whole file name, for the files that carry
// their meaning in the name rather than in an extension. Matched before the
// extension, and case-insensitively, because a Makefile is a makefile.
var fileIconByName = map[string]string{
	"makefile":       "",
	"gnumakefile":    "",
	"justfile":       "",
	"dockerfile":     "",
	"containerfile":  "",
	".gitignore":     "",
	".gitattributes": "",
	".gitmodules":    "",
	"license":        "",
	"licence":        "",
	"copying":        "",
}

// fileIconFolder and fileIconParent are the two directory icons, and
// fileIconFile is what a name neither table knows falls back to.
const (
	fileIconFolder = ""
	fileIconParent = ""
	fileIconFile   = ""
)

// init drops any icon the layout could not place. Every rail row budgets
// exactly one column for its glyph, so a two-cell codepoint would move the name
// beside it one column right and put the row's own click target under a
// different column than the one the pointer is tested against. It is the same
// rule sanitizeGlyphSet applies to a set the user wrote, applied here to the
// table we ship.
func init() {
	for _, table := range []map[string]string{fileIconByExt, fileIconByName} {
		for key, icon := range table {
			if lipgloss.Width(icon) != 1 {
				delete(table, key)
			}
		}
	}
}

// fileIconsOn reports whether the nerd font layer draws at all.
func fileIconsOn() bool {
	return config.SidebarFileIcons && !overlay.UseASCII()
}

// fileRowGlyph is the one cell in front of a name in the files section.
//
// parent is the ".." row, which is a directory but not one of the names in the
// listing: it moves the view out rather than in, so it wears a mark of its own
// and the user has something to aim at without reading.
func fileRowGlyph(name string, dir, parent bool) string {
	if !config.SidebarShowGlyphs {
		return " "
	}
	if !fileIconsOn() {
		switch {
		case parent:
			return config.GetRailParentGlyph()
		case dir:
			return config.GetRailFolderGlyph()
		default:
			return config.GetRailFileGlyph()
		}
	}
	switch {
	case parent:
		return fileIconParent
	case dir:
		return fileIconFolder
	}
	if icon, ok := fileIconByName[strings.ToLower(name)]; ok {
		return icon
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if icon, ok := fileIconByExt[ext]; ok {
		return icon
	}
	return fileIconFile
}
