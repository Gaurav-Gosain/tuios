package app

import (
	"os"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	devicons "github.com/epilande/go-devicons"
)

// The files section's icons.
//
// Three layers, and which of them draws is the whole of "it degrades".
//
// Underneath are three roles of the glyph set: folder, parent and file. They
// are settable like every other mark on the rail, they have ASCII spellings, and
// they are what a terminal running --ascii-only or a set that is 7-bit
// throughout draws. A listing is legible with nothing but those three.
//
// On top of that is one nerd font codepoint per file type, from the private use
// area. It is off in ASCII mode and off when appearance.sidebar.file_icons is
// false, because a terminal without a patched font draws every one of them as a
// tofu box. printableTitle strips exactly these codepoints out of window titles
// for that reason; the difference is that a title's icon is whatever a foreign
// program put there, while these are ours and the user has said their font has
// them.
//
// On top of that again is the colour, under appearance.sidebar.file_icon_colors.
// It needs the codepoint underneath it, so it is off whenever the icons are:
// an ASCII slash in ruby red is a colour with no shape to carry it. Off, the
// mark is drawn in the row's own ink like every other glyph on the rail.
//
// # Why the colour is worth arguing about
//
// The rail's design note spends emphasis on two things and no more: "this is
// where you are" and "this wants a human". A column of per-type colour is a
// third claim on the eye, and it stands in the files section directly under the
// agent-state glyphs, which are the alarm. That is the cost, it is real, and it
// is why the colour is an option rather than the only way the section draws.
// The default is on because that is what was asked for; the frame with both
// sections showing is what the choice should be made on.
//
// # Where the table went
//
// The glyphs and the colours both come from go-devicons, which is the generated
// Go form of nvim-web-devicons and the same table yeetui draws from, so the two
// programs name a file type the same way. It replaced a hand-written table of
// seventy extensions and eleven whole names. The trade is a dependency (MIT,
// 59 KB, no dependencies of its own) and a table nobody here can edit, against
// six hundred and fifty entries instead of eighty-one and a glyph that can
// never disagree with its colour.

// fileIcon is one file type's mark: the codepoint and the "#RRGGBB" the icon
// table gives it. A zero fileIcon means the type layer has nothing to say and
// the glyph set's own roles draw instead.
type fileIcon struct {
	Glyph string
	Hex   string
}

// fileIconParentGlyph is the ".." row's codepoint, and the one mark in the
// section the icon table has no opinion about: the parent is not a file type,
// it is the way out of the folder, so it wears a mark of its own and the user
// has something to aim at without reading. It takes a directory's colour,
// because a directory is what it is.
const fileIconParentGlyph = ""

// fileIconDir and fileIconAny are the table's own two generic answers: what a
// folder wears and what a name it has never heard of wears. Resolved once, by
// asking for a nameless entry of each kind, so the fallback is the library's
// rather than a codepoint copied out of it and left to drift.
var (
	fileIconDir    = fileIconFor("", true)
	fileIconAny    = fileIconFor("", false)
	fileIconParent = fileIcon{Glyph: fileIconParentGlyph, Hex: fileIconDir.Hex}
)

// dirEntryInfo is a zero-syscall os.FileInfo built from what a directory read
// already returned.
//
// It exists because the only lookup go-devicons exports that does not touch the
// disk takes an os.FileInfo, and the one that takes a path lstats it. A listing
// of a large tree is read for the rail on every cd, and a stat per name in it is
// the cost this section was built to avoid: fileEntry keeps DirEntry.Type() for
// exactly that reason, and pays for it by reading a symlink to a directory as a
// file.
type dirEntryInfo struct {
	name string
	mode os.FileMode
}

func (d dirEntryInfo) Name() string       { return d.name }
func (d dirEntryInfo) Size() int64        { return 0 }
func (d dirEntryInfo) Mode() os.FileMode  { return d.mode }
func (d dirEntryInfo) ModTime() time.Time { return time.Time{} }
func (d dirEntryInfo) IsDir() bool        { return d.mode&os.ModeDir != 0 }
func (d dirEntryInfo) Sys() any           { return nil }

// fileIconFor is the icon table's answer for one name.
//
// It is called when a listing is read rather than when a row is drawn, because
// the answer depends only on the name and the name does not change between
// frames. That keeps the map lookups, the ToLower and the width check off the
// path that redraws the rail, and it is the same place yeetui resolves them.
func fileIconFor(name string, dir bool) fileIcon {
	var mode os.FileMode
	if dir {
		mode = os.ModeDir
	}
	s := devicons.IconForInfo(dirEntryInfo{name: name, mode: mode})
	return fileIconFit(fileIcon{Glyph: s.Icon, Hex: s.Color})
}

// fileIconFit drops an icon the layout cannot place.
//
// Every rail row budgets exactly one column for its glyph, so a two-cell
// codepoint would move the name beside it one column right and put the row's own
// click target under a different column than the one the pointer is tested
// against. The shipped table is all one-cell today; the guard is here because
// the table is generated upstream and a bump can add anything to it, and a rail
// whose hit rectangles have quietly slipped a column is not a failure anyone
// reads as an icon problem.
func fileIconFit(icon fileIcon) fileIcon {
	if lipgloss.Width(icon.Glyph) != 1 {
		return fileIcon{}
	}
	return icon
}

// fileIconsOn reports whether the nerd font layer draws at all.
func fileIconsOn() bool {
	return config.SidebarFileIcons && !overlay.UseASCII()
}

// fileIconColorsOn reports whether the colour layer draws. It needs the icons
// under it, so switching the icons off switches the colour off with them.
func fileIconColorsOn() bool {
	return fileIconsOn() && config.SidebarFileIconColors
}

// fileRowMark is the one cell in front of a name in the files section, and the
// colour it burns. An empty Hex means the row's own ink.
//
// parent is the ".." row, which is a directory but not one of the names in the
// listing: it moves the view out rather than in, so it wears its own mark.
func fileRowMark(icon fileIcon, dir, parent bool) fileIcon {
	if !config.SidebarShowGlyphs {
		return fileIcon{Glyph: " "}
	}
	if !fileIconsOn() {
		switch {
		case parent:
			return fileIcon{Glyph: config.GetRailParentGlyph()}
		case dir:
			return fileIcon{Glyph: config.GetRailFolderGlyph()}
		default:
			return fileIcon{Glyph: config.GetRailFileGlyph()}
		}
	}
	switch {
	case parent:
		icon = fileIconParent
	case icon.Glyph == "":
		// A row whose icon the table had nothing to say about, or one the
		// layout could not place.
		if dir {
			icon = fileIconDir
		} else {
			icon = fileIconAny
		}
	}
	if !fileIconColorsOn() {
		icon.Hex = ""
	}
	return icon
}
