package capture

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/image/font/sfnt"
)

type indexedFace struct {
	File             string
	Index            int
	Family           string
	TypographicFamly string
	PostScript       string
	Subfamily        string
	Weight           weightClass
	Italic           bool
}

type weightClass int

const (
	weightOther weightClass = iota
	weightRegular
	weightBold
)

type fontIndex struct {
	byFamily     map[string][]indexedFace
	byPostScript map[string]indexedFace
}

var (
	fontIndexOnce sync.Once
	fontIndexVal  *fontIndex
)

func loadFontIndex() *fontIndex {
	fontIndexOnce.Do(func() { fontIndexVal = buildFontIndex(fontDirs()) })
	return fontIndexVal
}

func resetFontIndex() {
	fontIndexOnce = sync.Once{}
	fontIndexVal = nil
}

const maxFontFiles = 8000

func fontDirs() []string {
	home, _ := os.UserHomeDir()
	var dirs []string
	add := func(parts ...string) {
		for _, p := range parts {
			if p != "" {
				dirs = append(dirs, p)
			}
		}
	}
	switch runtime.GOOS {
	case "darwin":
		if home != "" {
			add(filepath.Join(home, "Library", "Fonts"))
		}
		add("/Library/Fonts", "/System/Library/Fonts", "/System/Library/Fonts/Supplemental")
	case "windows":
		add(os.Getenv("LOCALAPPDATA") + `\Microsoft\Windows\Fonts`)
		if win := os.Getenv("WINDIR"); win != "" {
			add(filepath.Join(win, "Fonts"))
		}
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			add(filepath.Join(xdg, "fonts"))
		} else if home != "" {
			add(filepath.Join(home, ".local", "share", "fonts"))
		}
		if home != "" {
			add(filepath.Join(home, ".fonts"))
		}
		add("/usr/local/share/fonts", "/usr/share/fonts", "/run/host/fonts")
	}
	return dirs
}

var fontFileExts = map[string]bool{
	".ttf": true, ".otf": true, ".ttc": true, ".otc": true, ".dfont": true,
}

func buildFontIndex(dirs []string) *fontIndex {
	return indexFonts(dirs, fontCachePath())
}

func scanFontIndex(dirs []string) *fontIndex {
	return indexFonts(dirs, "")
}

func indexFonts(dirs []string, cachePath string) *fontIndex {
	idx := &fontIndex{
		byFamily:     map[string][]indexedFace{},
		byPostScript: map[string]indexedFace{},
	}
	w := fontWalk{seenDirs: map[string]bool{}, seenFiles: map[string]bool{}}
	for _, dir := range dirs {
		if w.count >= maxFontFiles {
			break
		}
		w.dir(dir)
	}
	fingerprint := fontFingerprint(dirs, w.stamps)
	faces, ok := loadFontCache(cachePath, fingerprint)
	if !ok {
		for _, fileFaces := range readFontFaces(w.files) {
			faces = append(faces, fileFaces...)
		}
		saveFontCache(cachePath, fingerprint, faces)
	}
	for _, face := range faces {
		idx.add(face)
	}
	return idx
}

const fontReaders = 16

func readFontFaces(files []string) [][]indexedFace {
	out := make([][]indexedFace, len(files))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(fontReaders, len(files)) {
		wg.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= len(files) {
					return
				}
				out[i] = readFontNames(files[i])
			}
		})
	}
	wg.Wait()
	return out
}

type fontWalk struct {
	seenDirs  map[string]bool
	seenFiles map[string]bool
	count     int
	files     []string
	stamps    []fontFileStamp
}

func (w *fontWalk) dir(dir string) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil || w.seenDirs[real] {
		return
	}
	w.seenDirs[real] = true
	entries, err := os.ReadDir(real)
	if err != nil {
		return
	}
	for _, e := range entries {
		if w.count >= maxFontFiles {
			return
		}
		path := filepath.Join(real, e.Name())
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			isDir = info.IsDir()
		}
		if isDir {
			if !strings.HasPrefix(e.Name(), ".") {
				w.dir(path)
			}
			continue
		}
		if !fontFileExts[strings.ToLower(filepath.Ext(path))] {
			continue
		}
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		}
		if w.seenFiles[path] {
			continue
		}
		w.seenFiles[path] = true
		w.count++
		w.files = append(w.files, path)
		stamp := fontFileStamp{path: path}
		if info, err := os.Stat(path); err == nil {
			stamp.size, stamp.mtime = info.Size(), info.ModTime().UnixNano()
		}
		w.stamps = append(w.stamps, stamp)
	}
}

func (idx *fontIndex) add(f indexedFace) {
	if ps := foldFontName(f.PostScript); ps != "" {
		if _, taken := idx.byPostScript[ps]; !taken {
			idx.byPostScript[ps] = f
		}
	}
	for _, name := range []string{f.Family, f.TypographicFamly} {
		key := foldFontName(name)
		if key == "" {
			continue
		}
		idx.byFamily[key] = append(idx.byFamily[key], f)
	}
}

func readFontNames(path string) []indexedFace {
	f, err := os.Open(path) // #nosec G304 - a file found by walking the system font directories
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	coll, err := sfnt.ParseCollectionReaderAt(newBlockReaderAt(f))
	if err != nil {
		return nil
	}
	var out []indexedFace
	var buf sfnt.Buffer
	for i := range coll.NumFonts() {
		font, err := coll.Font(i)
		if err != nil {
			continue
		}
		name := func(id sfnt.NameID) string {
			s, err := font.Name(&buf, id)
			if err != nil {
				return ""
			}
			return strings.TrimSpace(s)
		}
		face := indexedFace{
			File:             path,
			Index:            i,
			Family:           name(sfnt.NameIDFamily),
			TypographicFamly: name(sfnt.NameIDTypographicFamily),
			PostScript:       name(sfnt.NameIDPostScript),
			Subfamily:        name(sfnt.NameIDSubfamily),
		}
		if face.Family == "" && face.TypographicFamly == "" && face.PostScript == "" {
			continue
		}
		style := face.Subfamily
		if typo := name(sfnt.NameIDTypographicSubfamily); typo != "" {
			style = typo
		}
		face.Weight, face.Italic = styleTraits(style, face.PostScript)
		out = append(out, face)
	}
	return out
}

func styleTraits(style, postScript string) (weightClass, bool) {
	name := foldStyleName(style)
	if name == "" {
		name = foldStyleName(afterHyphen(postScript))
	}
	italic := false
	for _, slant := range []string{"italic", "oblique"} {
		if strings.Contains(name, slant) {
			italic = true
			name = strings.ReplaceAll(name, slant, "")
		}
	}
	switch name {
	case "", "regular", "normal", "book", "roman", "text":
		return weightRegular, italic
	case "bold":
		return weightBold, italic
	default:
		return weightOther, italic
	}
}

func foldStyleName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func afterHyphen(s string) string {
	if i := strings.LastIndex(s, "-"); i >= 0 {
		return s[i+1:]
	}
	return ""
}

func foldFontName(s string) string {
	return strings.ToLower(squeezeSpace(s))
}

func indexPostScript(name string) (FontFace, bool) {
	f, ok := loadFontIndex().byPostScript[foldFontName(name)]
	if !ok {
		return FontFace{}, false
	}
	return FontFace{File: f.File, Index: f.Index, Name: f.PostScript}, true
}

func indexFamily(name string) (FontFace, bool) {
	if face, ok := pickFace(name, func(f indexedFace) bool {
		return f.Weight == weightRegular && !f.Italic
	}); ok {
		return face, true
	}
	return pickFace(name, func(f indexedFace) bool { return !f.Italic })
}

func indexBoldFamily(name string) (FontFace, bool) {
	return pickFace(name, func(f indexedFace) bool {
		return f.Weight == weightBold && !f.Italic
	})
}

func pickFace(name string, want func(indexedFace) bool) (FontFace, bool) {
	for _, f := range loadFontIndex().byFamily[foldFontName(name)] {
		if want(f) {
			return FontFace{File: f.File, Index: f.Index, Name: familyLabel(f)}, true
		}
	}
	return FontFace{}, false
}

func familyLabel(f indexedFace) string {
	if f.TypographicFamly != "" {
		return f.TypographicFamly
	}
	return f.Family
}
