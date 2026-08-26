package capture

import (
	"context"
	"os/exec"
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
// knows which font it is drawing, kitty will say so when asked, and fontconfig
// turns the name it gives into a path in about ten milliseconds.
//
// The one trap is that fc-match never fails. Asked for a font that does not
// exist it substitutes its best guess and reports success, so an unverified
// lookup silently draws the capture in Noto Sans. Every lookup here checks that
// the name fontconfig echoes back is the name that was asked for.

// FontFace is one resolved face: the file to rasterize with, and the name that
// found it.
type FontFace struct {
	File string
	Name string
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
	return matchFont("ps:"+name, ":postscriptname="+name, "%{file}|%{postscriptname}", func(echo string) bool {
		return equalFontName(echo, name)
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
		face, ok := matchFont("family:"+name, name+":", "%{file}|%{family}", func(echo string) bool {
			// fontconfig reports every family alias a face answers to, comma
			// separated, and a match on any of them is a match.
			for _, alias := range strings.Split(echo, ",") {
				if equalFontName(alias, name) {
					return true
				}
			}
			return false
		})
		if ok {
			return face, true
		}
	}
	return FontFace{}, false
}

// BoldFontByFamily resolves the bold face of a family. It reports nothing when
// fontconfig hands back the same file as the regular weight, because a family
// with no bold cut is better double-struck than drawn twice from one face and
// called bold.
func BoldFontByFamily(stack, regularFile string) (FontFace, bool) {
	for _, name := range strings.Split(stack, ",") {
		name = strings.TrimSpace(strings.Trim(strings.TrimSpace(name), `"'`))
		if name == "" || genericFamilies[strings.ToLower(name)] {
			continue
		}
		face, ok := matchFont("bold:"+name, name+":bold", "%{file}|%{family}", func(echo string) bool {
			for _, alias := range strings.Split(echo, ",") {
				if equalFontName(alias, name) {
					return true
				}
			}
			return false
		})
		if ok && face.File != regularFile {
			return face, true
		}
	}
	return FontFace{}, false
}

// matchFont runs one fc-match and keeps the answer only when verify accepts the
// name fontconfig echoed. Results are cached for the life of the process,
// misses included: a font that is not installed will not become installed while
// tuios is running, and a failed lookup per capture is a process spawn nobody
// asked for.
func matchFont(key, pattern, format string, verify func(string) bool) (FontFace, bool) {
	fontCacheMu.Lock()
	defer fontCacheMu.Unlock()
	if hit, ok := fontCache[key]; ok {
		return hit, hit.File != ""
	}
	face := FontFace{}
	if out, err := runFcMatch(pattern, format); err == nil {
		file, echo, found := strings.Cut(strings.TrimSpace(out), "|")
		if found && file != "" && verify(echo) {
			face = FontFace{File: file, Name: echo}
		}
	}
	fontCache[key] = face
	return face, face.File != ""
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
}
