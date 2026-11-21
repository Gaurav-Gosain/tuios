package capture

import (
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"tuios.dev/shot"
)

const maxFallbackLookups = 8

var fallbackQueries = []string{"monospace:", ":", "emoji:"}

const maxFallbackFaces = 6

// FillFallbacks is fillFallbacks for a frame built outside Frame, as the e2e
// suite builds its pictures, so they draw a glyph as a capture does.
func FillFallbacks(g *shot.Grid, f *shot.Frame) { fillFallbacks(g, f) }

func fillFallbacks(g *shot.Grid, f *shot.Frame) {
	if g == nil || f == nil {
		return
	}
	stage := map[rune]int{}
	lookups := 0
	var added []FontFace
	var last rune
	for {
		missing, err := shot.MissingGlyphs(g, f)
		if err != nil {
			return
		}
		if last != 0 && slices.Contains(missing, last) {
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

const maxOpenFontFiles = 32

var openFonts struct {
	mu    sync.Mutex
	files map[string]*os.File
}

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

const fontFileKeep = time.Minute

const fontFileBudget = 64 << 20

var fontFiles struct {
	mu    sync.Mutex
	data  map[string][]byte
	size  int
	timer *time.Timer
}

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

func dropFontFiles() {
	fontFiles.mu.Lock()
	defer fontFiles.mu.Unlock()
	fontFiles.data, fontFiles.size = nil, 0
}
