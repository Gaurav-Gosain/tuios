package capture

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Finding the font a capture should be drawn in.
//
// The PNG backend rasterizes with a real font file, and its built-in fallback
// is Go Mono, which has no icons. Every private-use glyph in a modern prompt
// therefore came out as a tofu box unless the user found and set
// screenshot.font_file by hand. Nobody should have to: the terminal already
// knows which font it is drawing, and kitty will say so when asked.
//
// Turning that name into a path is what this file does, and it asks twice. The
// font index in fontindex.go reads the names out of the font files themselves,
// which works on every platform and needs nothing installed; fontconfig is
// asked second, for the fonts a machine keeps somewhere only fontconfig was
// told about. Asking fontconfig first, or only, is what this used to do, and it
// meant no capture on macOS ever found a font at all: fc-match is fontconfig's
// command line and macOS does not ship fontconfig.
//
// The one trap in the fontconfig half is that fc-match never fails. Asked for a
// font that does not exist it substitutes its best guess and reports success,
// so an unverified lookup silently draws the capture in Noto Sans. Every
// lookup here checks that the name fontconfig echoes back is the name that was
// asked for. The index cannot substitute: it only ever answers with a file
// that says it is the font that was asked for.

// FontFace is one resolved face: the file to rasterize with, and the name that
// found it.
type FontFace struct {
	File string
	// Index is the face to take from File when the file is a collection. Zero
	// is the first face, and the whole answer for a plain single font.
	Index int
	Name  string
}

// fontLookupTimeout bounds a fontconfig call. fc-match answers in about ten
// milliseconds; anything past this is a machine with a broken font cache, and a
// capture drawn in the fallback face beats a capture that never arrives.
const fontLookupTimeout = 2 * time.Second

// genericFamilies are the CSS fallbacks a font stack ends with. They name a
// category rather than a font, so fontconfig would answer them with whatever it
// pleases, which is exactly the substitution this package refuses elsewhere.
var genericFamilies = map[string]bool{
	"monospace": true, "sans-serif": true, "serif": true,
	"cursive": true, "fantasy": true, "system-ui": true, "ui-monospace": true,
}

var (
	fontCacheMu sync.Mutex
	fontCache   = map[string]FontFace{}
)

// FontByPostScriptName resolves an exact PostScript name, which is the shape of
// name a terminal answers a font query with.
func FontByPostScriptName(name string) (FontFace, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return FontFace{}, false
	}
	return cachedLookup("ps:"+name, func() (FontFace, bool) {
		if face, ok := indexPostScript(name); ok {
			return face, true
		}
		return fcMatch(":postscriptname="+name, "%{file}|%{postscriptname}", func(echo string) bool {
			return equalFontName(echo, name)
		})
	})
}

// FontByFamily resolves a family name, which is the shape of name a config file
// carries. A CSS stack is tried entry by entry, and the generic categories at
// the end of one are skipped rather than handed to fontconfig.
func FontByFamily(stack string) (FontFace, bool) {
	for _, name := range strings.Split(stack, ",") {
		name = strings.TrimSpace(strings.Trim(strings.TrimSpace(name), `"'`))
		if name == "" || genericFamilies[strings.ToLower(name)] {
			continue
		}
		face, ok := cachedLookup("family:"+name, func() (FontFace, bool) {
			if face, ok := indexFamily(name); ok {
				return face, true
			}
			return fcMatch(name+":", "%{file}|%{family}", func(echo string) bool {
				// fontconfig reports every family alias a face answers to, comma
				// separated, and a match on any of them is a match.
				for _, alias := range strings.Split(echo, ",") {
					if equalFontName(alias, name) {
						return true
					}
				}
				return false
			})
		})
		if ok {
			return face, true
		}
	}
	return FontFace{}, false
}

// BoldFontByFamily resolves the bold face of a family. It reports nothing when
// the answer is the face the regular weight already resolved to, because a
// family with no bold cut is better double-struck than drawn twice from one
// face and called bold.
//
// "The same face" is a file and an index, not a file. A collection keeps a
// family's weights in one file, so Menlo's bold is face 1 of the same .ttc the
// regular came from, and comparing paths alone threw it away.
func BoldFontByFamily(stack string, regular FontFace) (FontFace, bool) {
	for _, name := range strings.Split(stack, ",") {
		name = strings.TrimSpace(strings.Trim(strings.TrimSpace(name), `"'`))
		if name == "" || genericFamilies[strings.ToLower(name)] {
			continue
		}
		face, ok := cachedLookup("bold:"+name, func() (FontFace, bool) {
			if face, ok := indexBoldFamily(name); ok {
				return face, true
			}
			return fcMatch(name+":bold", "%{file}|%{family}", func(echo string) bool {
				for _, alias := range strings.Split(echo, ",") {
					if equalFontName(alias, name) {
						return true
					}
				}
				return false
			})
		})
		if ok && (face.File != regular.File || face.Index != regular.Index) {
			return face, true
		}
	}
	return FontFace{}, false
}

// cachedLookup memoizes one resolution for the life of the process, misses
// included: a font that is not installed will not become installed while tuios
// is running, and a repeated miss is a directory walk and a process spawn
// nobody asked for.
//
// The lock is held across find because the expensive miss is the first one,
// which builds the font index. Two captures racing on a cold cache should wait
// for one scan rather than run two.
func cachedLookup(key string, find func() (FontFace, bool)) (FontFace, bool) {
	fontCacheMu.Lock()
	defer fontCacheMu.Unlock()
	if hit, ok := fontCache[key]; ok {
		return hit, hit.File != ""
	}
	face, ok := find()
	if !ok {
		face = FontFace{}
	}
	fontCache[key] = face
	return face, face.File != ""
}

// fcMatch runs one fc-match and keeps the answer only when verify accepts the
// name fontconfig echoed.
//
// The format asks for the face index as well as the file, because fontconfig
// answers plenty of families with a collection: "Menlo" on macOS is a face of
// Menlo.ttc, not a file of its own.
func fcMatch(pattern, format string, verify func(string) bool) (FontFace, bool) {
	out, err := runFcMatch(pattern, format+"|%{index}")
	if err != nil {
		return FontFace{}, false
	}
	fields := strings.Split(strings.TrimSpace(out), "|")
	if len(fields) < 2 || fields[0] == "" || !verify(fields[1]) {
		return FontFace{}, false
	}
	face := FontFace{File: fields[0], Name: fields[1]}
	if len(fields) > 2 {
		// A fontconfig too old to know %{index} echoes the specifier back
		// verbatim. An answer that will not parse is face zero, which is what
		// asking for the file alone has always meant.
		if n, err := strconv.Atoi(strings.TrimSpace(fields[2])); err == nil && n > 0 {
			face.Index = n
		}
	}
	return face, true
}

// runFcMatch is the fontconfig call, isolated so a host without fontconfig
// costs one PATH lookup and no process.
func runFcMatch(pattern, format string) (string, error) {
	if _, err := exec.LookPath("fc-match"); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), fontLookupTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "fc-match", pattern, "-f", format).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// equalFontName compares two font names the way a person reads them: case and
// the spaces between words are not what tells two fonts apart, and fontconfig
// is inconsistent about both.
func equalFontName(a, b string) bool {
	return strings.EqualFold(squeezeSpace(a), squeezeSpace(b))
}

func squeezeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// resetFontCache empties the lookup cache. Tests own it; nothing else needs it,
// because the answer cannot change under a running process.
func resetFontCache() {
	fontCacheMu.Lock()
	defer fontCacheMu.Unlock()
	fontCache = map[string]FontFace{}
	resetFontIndex()
}
