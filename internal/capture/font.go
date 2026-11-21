package capture

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
)

// FontFace is one resolved face: the file to rasterize with, and the name that
// found it.
type FontFace struct {
	File string
	// Index is the face to take from File when the file is a collection. Zero
	// is the first face, and the whole answer for a plain single font.
	Index int
	Name  string
}

const fontLookupTimeout = 2 * time.Second

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
	for name := range strings.SplitSeq(stack, ",") {
		name = strings.TrimSpace(strings.Trim(strings.TrimSpace(name), `"'`))
		if name == "" || genericFamilies[strings.ToLower(name)] {
			continue
		}
		face, ok := cachedLookup("family:"+name, func() (FontFace, bool) {
			if face, ok := indexFamily(name); ok {
				return face, true
			}
			return fcMatch(name+":", "%{file}|%{family}", func(echo string) bool {
				for alias := range strings.SplitSeq(echo, ",") {
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
	for name := range strings.SplitSeq(stack, ",") {
		name = strings.TrimSpace(strings.Trim(strings.TrimSpace(name), `"'`))
		if name == "" || genericFamilies[strings.ToLower(name)] {
			continue
		}
		face, ok := cachedLookup("bold:"+name, func() (FontFace, bool) {
			if face, ok := indexBoldFamily(name); ok {
				return face, true
			}
			return fcMatch(name+":bold", "%{file}|%{family}", func(echo string) bool {
				for alias := range strings.SplitSeq(echo, ",") {
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
		if n, err := strconv.Atoi(strings.TrimSpace(fields[2])); err == nil && n > 0 {
			face.Index = n
		}
	}
	return face, true
}

func runFcMatch(pattern, format string) (string, error) {
	if fcMatchStalled.Load() {
		return "", errFcMatchStalled
	}
	if _, err := exec.LookPath("fc-match"); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), fontLookupTimeout)
	defer cancel()
	out, err := childproc.CommandContext(ctx, "fc-match", pattern, "-f", format).Output()
	if err != nil {
		if ctx.Err() != nil {
			fcMatchStalled.Store(true)
		}
		return "", err
	}
	return string(out), nil
}

var (
	fcMatchStalled    atomic.Bool
	errFcMatchStalled = errors.New("fc-match timed out before")
)

func equalFontName(a, b string) bool {
	return strings.EqualFold(squeezeSpace(a), squeezeSpace(b))
}

func squeezeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func resetFontCache() {
	fontCacheMu.Lock()
	defer fontCacheMu.Unlock()
	fontCache = map[string]FontFace{}
	fcMatchStalled.Store(false)
	resetFontIndex()
}
