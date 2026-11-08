package capture

import (
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"tuios.dev/shot"
)

// Fallback faces for the glyphs the capture's font does not have.
//
// A terminal does not draw every glyph from its configured font. Geist Mono
// has no ◆, ▸ or ▤, and kitty draws them anyway, from whichever installed font
// fontconfig names for the codepoint. shot's PNG backend only knew the
// configured font and its built-in Go Mono, so the same screen came out with
// dotted boxes where the terminal had shapes.
//
// fillFallbacks closes that gap the way the terminal does: it asks fontconfig
// for a monospace font that covers each glyph the frame's fonts miss, and
// appends the answers to Frame.Fallback. It runs fc-match as a process, so
// tuios stays a cgo-free build. A host with no fc-match (macOS without
// Homebrew's fontconfig, Windows) keeps the old picture: Go Mono, then the box.
//
// The cost is bounded three ways. shot.MissingGlyphs is asked again after
// each face is added, so a glyph a face already covers asks nothing more.
// Every answer is cached for the life of the process, misses included,
// because the installed fonts do not change under a running tuios. And one
// capture asks about at most maxFallbackLookups glyphs, cached or not, so a
// warm cache draws the same picture as a cold one.

// maxFallbackLookups bounds the queries one capture asks fontconfig.
// fc-match answers in about ten milliseconds, and a screen in three scripts
// needs three fonts, so a bound well past that only stops a screen of random
// codepoints from spending seconds on lookups.
const maxFallbackLookups = 8

// fallbackQueries are the fontconfig patterns asked for a glyph, in turn,
// each one only when the face the one before named does not have it: a
// monospace face first, as a terminal prefers; then any family, since on
// macOS fontconfig answers a monospace query for Hangul with LastResort, which
// draws only a box, where Arial Unicode has the glyph; then an emoji face,
// since only that query names Apple Color Emoji or Noto Color Emoji. shot
// draws their sbix and CBDT bitmaps, and a glyph a face has in no form it
// draws counts as missing there, so the next query follows.
var fallbackQueries = []string{"monospace:", ":", "emoji:"}

// maxFallbackFaces bounds the faces one capture adds. Each one is a font file
// read into memory for the render.
const maxFallbackFaces = 6

// FillFallbacks is fillFallbacks for a frame built outside Frame, as the e2e
// suite builds its pictures, so they draw a glyph as a capture does.
func FillFallbacks(g *shot.Grid, f *shot.Frame) { fillFallbacks(g, f) }

// fillFallbacks appends to f.Fallback a face for each glyph of g that no face
// of f has, when fontconfig knows one.
func fillFallbacks(g *shot.Grid, f *shot.Frame) {
	if g == nil || f == nil {
		return
	}
	stage := map[rune]int{} // the queries asked for each glyph so far
	lookups := 0
	var added []FontFace // the faces in f.Fallback, in order
	var last rune        // the glyph the face added last was found for
	for {
		missing, err := shot.MissingGlyphs(g, f)
		if err != nil {
			return
		}
		if last != 0 && slices.Contains(missing, last) {
			// fontconfig named a face that does not have the glyph it was
			// asked for. It draws nothing, so it goes, and the glyphs it
			// seemed to cover are missing again. The glyph's next query
			// comes next.
			f.Fallback = f.Fallback[:len(f.Fallback)-1]
			added = added[:len(added)-1]
			last = 0
			continue
		}
		last = 0
		var r rune
		for _, m := range missing {
			if stage[m] < len(fallbackQueries) {
				r = m
				break
			}
		}
		if r == 0 || len(f.Fallback) >= maxFallbackFaces || lookups >= maxFallbackLookups {
			return
		}
		query := fallbackQueries[stage[r]]
		stage[r]++
		lookups++
		hex := strconv.FormatInt(int64(r), 16)
		face, ok := cachedLookup("glyph:"+query+hex, func() (FontFace, bool) {
			return fcMatch(query+"charset="+hex, "%{file}|%{family}", func(string) bool { return true })
		})
		// A face in the list already does not cover r: fontconfig answers
		// with its best guess when no font has the glyph.
		if !ok || slices.Contains(added, face) {
			continue
		}
		src, ok := openFontFile(face.File)
		if !ok {
			continue
		}
		f.Fallback = append(f.Fallback, src)
		f.Fallback[len(f.Fallback)-1].Index = face.Index
		added = append(added, face)
		last = r
	}
}

// maxOpenFontFiles bounds the fallback font files kept open. Past it, a
// file is read whole for its capture, as before.
const maxOpenFontFiles = 32

var openFonts struct {
	mu    sync.Mutex
	files map[string]*os.File
}

// openFontFile is a fallback font for shot to read in place: an open file it
// reads only the tables and glyphs of. A colour emoji font is far too big to
// read whole for each capture (Apple Color Emoji is about 190 MB). The files
// stay open for the life of the process: fontconfig names a handful.
func openFontFile(path string) (shot.Font, bool) {
	openFonts.mu.Lock()
	defer openFonts.mu.Unlock()
	if f, ok := openFonts.files[path]; ok {
		return shot.Font{File: f}, true
	}
	if len(openFonts.files) >= maxOpenFontFiles {
		data, err := readFontFile(path)
		return shot.Font{Data: data}, err == nil
	}
	f, err := os.Open(path) // #nosec G304 - a font file fontconfig named
	if err != nil {
		return shot.Font{}, false
	}
	if openFonts.files == nil {
		openFonts.files = map[string]*os.File{}
	}
	openFonts.files[path] = f
	return shot.Font{File: f}, true
}

// fontFileKeep is how long the font files stay in memory after the last
// capture that read them. A run of captures, such as an agent that takes one
// after each step, reads each file once. An idle tuios keeps none: one CJK
// fallback is about 19 MB.
const fontFileKeep = time.Minute

// fontFileBudget bounds the bytes the cache keeps. A file past it is read for
// its capture and not kept.
const fontFileBudget = 64 << 20

var fontFiles struct {
	mu    sync.Mutex
	data  map[string][]byte
	size  int
	timer *time.Timer
}

// readFontFile reads a font file through the cache. The renderer only reads
// font data, so captures share one copy.
func readFontFile(path string) ([]byte, error) {
	fontFiles.mu.Lock()
	defer fontFiles.mu.Unlock()
	if fontFiles.timer == nil {
		fontFiles.timer = time.AfterFunc(fontFileKeep, dropFontFiles)
	} else {
		fontFiles.timer.Reset(fontFileKeep)
	}
	if data, ok := fontFiles.data[path]; ok {
		return data, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 - the operator's config, or a font file the resolver found
	if err != nil {
		return nil, err
	}
	if fontFiles.size+len(data) <= fontFileBudget {
		if fontFiles.data == nil {
			fontFiles.data = map[string][]byte{}
		}
		fontFiles.data[path] = data
		fontFiles.size += len(data)
	}
	return data, nil
}

// dropFontFiles empties the cache. The timer runs it once, fontFileKeep after
// the last read, so an idle process has no timer.
func dropFontFiles() {
	fontFiles.mu.Lock()
	defer fontFiles.mu.Unlock()
	fontFiles.data, fontFiles.size = nil, 0
}
