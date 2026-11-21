package app

import (
	"os"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	devicons "github.com/epilande/go-devicons"
)

type fileIcon struct {
	Glyph string
	Hex   string
}

const fileIconParentGlyph = ""

var (
	fileIconDir    = fileIconFor("", true)
	fileIconAny    = fileIconFor("", false)
	fileIconParent = fileIcon{Glyph: fileIconParentGlyph, Hex: fileIconDir.Hex}
)

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

func fileIconFor(name string, dir bool) fileIcon {
	var mode os.FileMode
	if dir {
		mode = os.ModeDir
	}
	s := devicons.IconForInfo(dirEntryInfo{name: name, mode: mode})
	return fileIconFit(fileIcon{Glyph: s.Icon, Hex: s.Color})
}

func fileIconFit(icon fileIcon) fileIcon {
	if lipgloss.Width(icon.Glyph) != 1 {
		return fileIcon{}
	}
	return icon
}

func fileIconsOn(s *config.Settings) bool {
	return s.SidebarFileIcons && !overlay.UseASCII() && !s.NoNerdFont
}

func fileIconColorsOn(s *config.Settings) bool {
	return fileIconsOn(s) && s.SidebarFileIconColors
}

func fileRowMark(icon fileIcon, dir, parent bool, s *config.Settings) fileIcon {
	if !s.SidebarShowGlyphs {
		return fileIcon{Glyph: " "}
	}
	if !fileIconsOn(s) {
		switch {
		case parent:
			return fileIcon{Glyph: s.GetRailParentGlyph()}
		case dir:
			return fileIcon{Glyph: s.GetRailFolderGlyph()}
		default:
			return fileIcon{Glyph: s.GetRailFileGlyph()}
		}
	}
	switch {
	case parent:
		icon = fileIconParent
	case icon.Glyph == "":
		if dir {
			icon = fileIconDir
		} else {
			icon = fileIconAny
		}
	}
	if !fileIconColorsOn(s) {
		icon.Hex = ""
	}
	return icon
}
