package config

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// GlyphEnv is what the terminal tuios draws on can show, as far as its
// environment says. It picks the glyphs when nobody chose them: a wrong glyph
// is worse than a plain one, and a terminal that cannot draw box drawing or a
// Nerd Font icon shows a box or a question mark in its place.
type GlyphEnv uint8

const (
	// GlyphEnvFull is a terminal that says nothing against drawing
	// everything: the default glyph set, Nerd Font icons included.
	GlyphEnvFull GlyphEnv = iota
	// GlyphEnvUnicode is the Linux console (TERM=linux). Its font has box
	// drawing and a few geometric shapes and no Nerd Font icons, so the chrome
	// takes the unicode glyph set and the dock and notification icons their
	// ASCII forms.
	GlyphEnvUnicode
	// GlyphEnvASCII is a locale that is not UTF-8. The terminal decodes bytes
	// in some other encoding, so anything past 7-bit ASCII comes out as
	// garbage: tuios runs as if --ascii-only were given.
	GlyphEnvASCII
)

// String names the environment the way list-glyphs prints it.
func (e GlyphEnv) String() string {
	switch e {
	case GlyphEnvUnicode:
		return "unicode"
	case GlyphEnvASCII:
		return "ascii"
	}
	return "full"
}

// DetectGlyphEnv reads the locale and TERM through getenv.
//
// The locale is the first of LC_ALL, LC_CTYPE and LANG that is set, which is
// the order the C library resolves the character type in. One that names
// UTF-8 in either spelling is fine, and any other value is not: C, POSIX and
// en_US.ISO-8859-1 all mean an 8-bit or 7-bit terminal. None of the three set
// at all is read as nothing known, not as the C locale. That is how a macOS
// terminal with "set locale environment variables" off and a fresh container
// both start, and both draw UTF-8 perfectly well; treating them as C would put
// the ASCII chrome on a great many screens that can draw the real one.
//
// The locale is checked first because it is the stronger statement: on a
// console with a non-UTF-8 locale, even box drawing is wrong.
func DetectGlyphEnv(getenv func(string) string) (GlyphEnv, string) {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := getenv(key)
		if v == "" {
			continue
		}
		if !localeIsUTF8(v) {
			return GlyphEnvASCII, key + "=" + v + " is not a UTF-8 locale"
		}
		break
	}
	if getenv("TERM") == "linux" {
		return GlyphEnvUnicode, "TERM=linux is the Linux console, which has no Nerd Font glyphs"
	}
	return GlyphEnvFull, ""
}

// localeIsUTF8 reports whether a locale name selects UTF-8: en_US.UTF-8,
// C.utf8, en_US.utf-8@euro and the like.
func localeIsUTF8(v string) bool {
	v = strings.ToLower(v)
	return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
}

// glyphsChosen reports whether the config names a glyph set of its own. The
// default id is not a choice: it is what an untouched config carries, so it
// is the one the environment may replace.
func glyphsChosen(id string) bool {
	return id != "" && id != theme.GlyphSetNone
}

// applyGlyphEnv folds the environment into the glyphs after the config has
// named its set. An explicit choice always wins: --ascii-only, or any set
// other than the default.
//
// It returns the set to activate. s.GlyphSet keeps the configured id, so
// get-config reports what the user wrote rather than what the terminal
// forced.
func (s *Settings) applyGlyphEnv() string {
	chosen := glyphsChosen(s.GlyphSet)
	s.NoNerdFont = !chosen && s.GlyphEnv == GlyphEnvUnicode
	if s.GlyphEnv == GlyphEnvASCII {
		s.UseASCIIOnly = s.ASCIIRequested || !chosen
	}
	if s.NoNerdFont {
		return "unicode"
	}
	return s.GlyphSet
}

// NerdFontsOff reports whether Nerd Font icons must not be drawn: under ASCII
// mode, and on a terminal whose font has none (GlyphEnvUnicode).
func (s *Settings) NerdFontsOff() bool {
	return s.UseASCIIOnly || s.NoNerdFont
}
