// Package swatch reads the colour theme of the desktop an application runs
// on, as a Palette with semantic roles: a background, a text colour, an
// accent, the sixteen ANSI colours and the rest.
//
// A Source reads one kind of theme. The omarchy sub-package reads the theme
// Omarchy applies. A Source also names the paths whose change means a new
// palette, so an application can follow the theme while it runs.
//
// Watch follows a Source: it reads the theme again when one of its paths
// changes, and delivers each new palette. The teaswatch sub-package turns that
// into Bubble Tea messages.
//
// swatch and its omarchy package use the standard library and fsnotify. They
// read theme files from strangers, so they parse colours only: nothing in a
// theme file is executed or expanded.
//
// The API is at v0 and can change between minor versions.
package swatch

import (
	"errors"
	"image/color"
	"maps"
)

// ErrNotFound is the error a Source's Load returns when the theme it reads
// is not installed on this machine.
var ErrNotFound = errors.New("swatch: theme source not found")

// Palette is a theme with semantic roles. A role the source does not define
// is derived from the others, so every field is set on a palette a Source
// returns, except the entries of Raw.
type Palette struct {
	// ID is the theme's short name as the source spells it, such as
	// "tokyo-night". Name is the name to show, such as "Tokyo Night".
	ID, Name string
	// Source names the Source the palette came from, such as "omarchy".
	Source string
	// Light is true for a theme meant for a light background.
	Light bool

	Background, Foreground, Cursor           color.Color
	SelectionBackground, SelectionForeground color.Color

	// Surface is a raised ground, for a panel or a dialog over the
	// background.
	Surface color.Color
	// Muted is the colour of secondary text.
	Muted color.Color
	// Accent is the theme's highlight colour.
	Accent color.Color
	// Border is the colour of the focused window's border. It is Accent
	// when the theme names no border of its own.
	Border color.Color

	Success, Warning, Error, Info color.Color

	// ANSI is the sixteen terminal colours, 0 to 15.
	ANSI [16]color.Color

	// Raw holds every colour the source defines or derives, by the source's
	// own key, for an application that wants more than the roles above.
	// Treat it as read-only: palettes copied from one read share it.
	Raw map[string]color.Color
}

// IsDark reports whether the palette is meant for a dark background.
func (p Palette) IsDark() bool { return !p.Light }

// Equal reports whether p and q hold the same names and colours. Two loads
// of an unchanged theme are Equal.
func (p Palette) Equal(q Palette) bool {
	if p.ID != q.ID || p.Name != q.Name || p.Source != q.Source || p.Light != q.Light {
		return false
	}
	pairs := [][2]color.Color{
		{p.Background, q.Background}, {p.Foreground, q.Foreground}, {p.Cursor, q.Cursor},
		{p.SelectionBackground, q.SelectionBackground}, {p.SelectionForeground, q.SelectionForeground},
		{p.Surface, q.Surface}, {p.Muted, q.Muted}, {p.Accent, q.Accent}, {p.Border, q.Border},
		{p.Success, q.Success}, {p.Warning, q.Warning}, {p.Error, q.Error}, {p.Info, q.Info},
	}
	for i := range p.ANSI {
		pairs = append(pairs, [2]color.Color{p.ANSI[i], q.ANSI[i]})
	}
	for _, pair := range pairs {
		if !sameColor(pair[0], pair[1]) {
			return false
		}
	}
	return maps.EqualFunc(p.Raw, q.Raw, sameColor)
}

// sameColor compares two colours by value. Two nil colours are the same.
func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// Source reads a palette from one kind of theme.
type Source interface {
	// Name names the source, such as "omarchy". It is the Source field of
	// every palette Load returns.
	Name() string
	// Load reads the theme now in force. It returns ErrNotFound, wrapped or
	// not, when the theme is not installed.
	Load() (Palette, error)
	// WatchPaths are the directories whose change can mean a new palette.
	// A directory that does not exist yet is still listed. Empty means the
	// source cannot be followed.
	WatchPaths() []string
	// Relevant reports whether a change to path, a file or a directory in
	// one of WatchPaths, can mean a new palette.
	Relevant(path string) bool
}

// Static returns a Source that always loads p and never changes.
func Static(p Palette) Source { return staticSource{p} }

type staticSource struct{ p Palette }

func (s staticSource) Name() string           { return s.p.Source }
func (s staticSource) Load() (Palette, error) { return s.p, nil }
func (staticSource) WatchPaths() []string     { return nil }
func (staticSource) Relevant(string) bool     { return false }
