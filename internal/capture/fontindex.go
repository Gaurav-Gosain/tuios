package capture

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/image/font/sfnt"
)

// The font files on this machine, keyed by the names they carry inside
// themselves.
//
// fc-match is fontconfig's command line, and macOS does not ship fontconfig.
// Terminals do not have this problem because they link the platform's font API
// instead: CoreText on macOS, DirectWrite on Windows, libfontconfig on Linux.
// All three want cgo, and tuios is built without it, which is what left the
// screenshot font lookup reaching for the one font database a Mac reliably does
// not have. Every capture on an untouched Mac therefore fell back to Go Mono,
// which has no icons in it.
//
// The way out is that a font database is more than this package needs. It needs
// a path for a name, and a font file already states its own names in its
// OpenType `name` table. Reading them directly needs no cgo, no installed
// tool, and no process: it is the same answer on every platform, and it is
// available where fontconfig is not installed at all.
//
// fontconfig is still asked when it is there and the index came up empty. It
// knows about directories configured by hand and about fonts installed
// somewhere no convention would look for them, and giving that up to gain the
// Mac would be a trade, not a fix.

// indexedFace is one face as its own name table describes it.
type indexedFace struct {
	File string
	// Index is which face inside File this is. A collection holds several and
	// the file path alone does not say which one was wanted.
	Index int
	// Family is the name a config file uses. A face whose typographic family
	// differs from its family name is recorded under both: a four-weight
	// family names every weight in NameIDFamily ("Iosevka Term SemiBold")
	// while NameIDTypographicFamily stays the name a person would type.
	Family           string
	TypographicFamly string
	PostScript       string
	Subfamily        string
	Weight           weightClass
	Italic           bool
}

// weightClass is as much of a face's weight as choosing between the faces of
// one family needs: the plain one, the bold one, and all the others.
type weightClass int

const (
	// weightOther is any named weight that is neither of the two a terminal
	// asks for: Thin, Light, Medium, SemiBold, ExtraBold, Black.
	weightOther weightClass = iota
	weightRegular
	weightBold
)

// fontIndex is every face found under the system font directories.
type fontIndex struct {
	byFamily     map[string][]indexedFace
	byPostScript map[string]indexedFace
}

var (
	fontIndexOnce sync.Once
	fontIndexVal  *fontIndex
)

// loadFontIndex builds the index once. A machine does not grow fonts while
// tuios is running, and the scan is the expensive half of a lookup.
func loadFontIndex() *fontIndex {
	fontIndexOnce.Do(func() { fontIndexVal = buildFontIndex(fontDirs()) })
	return fontIndexVal
}

// resetFontIndex drops the scan so the next lookup rebuilds it. Tests own this;
// nothing else needs it.
func resetFontIndex() {
	fontIndexOnce = sync.Once{}
	fontIndexVal = nil
}

// maxFontFiles bounds the walk. A large machine has a few thousand faces; a
// number far past that means the walk wandered somewhere it should not have,
// and a capture drawn in the fallback face beats one that never arrives.
const maxFontFiles = 8000

// fontDirs is where each platform keeps fonts, most specific first so a user's
// own copy of a family wins over the system's.
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

// fontFileExts are the container formats x/image/font/sfnt can read. A
// collection holds several faces in one file and is how macOS ships most of
// its own monospace faces, so skipping it would skip Menlo.
var fontFileExts = map[string]bool{
	".ttf": true, ".otf": true, ".ttc": true, ".otc": true, ".dfont": true,
}

func buildFontIndex(dirs []string) *fontIndex {
	idx := &fontIndex{
		byFamily:     map[string][]indexedFace{},
		byPostScript: map[string]indexedFace{},
	}
	seen := map[string]bool{}
	count := 0
	for _, dir := range dirs {
		if count >= maxFontFiles {
			break
		}
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// An unreadable directory is one place with no fonts in it, not
				// a reason to abandon the places that are readable.
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if count >= maxFontFiles {
				return fs.SkipAll
			}
			if d.IsDir() {
				if strings.HasPrefix(d.Name(), ".") && path != dir {
					return fs.SkipDir
				}
				return nil
			}
			if !fontFileExts[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			if seen[path] {
				return nil
			}
			seen[path] = true
			count++
			for _, face := range readFontNames(path) {
				idx.add(face)
			}
			return nil
		})
	}
	return idx
}

// add files one face under every name it answers to.
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
		// A collection lists the same family once per face; the slice is the
		// weights, and picking between them is the lookup's job.
		idx.byFamily[key] = append(idx.byFamily[key], f)
	}
}

// readFontNames opens one font file and reads what each face inside calls
// itself. A file that will not parse contributes nothing and is not an error:
// a font directory holds bitmap fonts, Type 1 leftovers and the occasional
// truncated download, and none of them should stop the scan.
func readFontNames(path string) []indexedFace {
	f, err := os.Open(path) // #nosec G304 - a file found by walking the system font directories
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	// ParseCollectionReaderAt answers a plain TTF or OTF with a collection of
	// one, so this is the single path for every container.
	coll, err := sfnt.ParseCollectionReaderAt(f)
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

// styleTraits reads weight and slant off the style name.
//
// The subfamily is the honest source for this: "Regular", "Bold", "Bold
// Italic", "ExtraBold". The PostScript name is the backstop for a face that
// leaves its subfamily blank, where the style rides after the hyphen
// ("JetBrainsMonoNF-Bold").
//
// The trap is that a weight is not a word. "Extra Bold", "ExtraBold" and
// "extrabold" are one weight spelled three ways, and a face that merely
// contains "bold" is as likely to be SemiBold as Bold. Both halves are folded
// to bare letters and then read whole: only the name that is exactly a bold
// with nothing else attached is this family's bold cut, and everything from
// Thin to Black is a weight nobody asked for. Reading "bold" out of
// "ExtraBold" is what made a plain request for JetBrainsMono Nerd Font
// rasterize in ExtraBold, because ExtraBold was neither bold nor italic and so
// passed for regular.
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

// foldStyleName reduces a style name to bare lowercase letters, so the several
// ways of writing one weight land on one answer.
func foldStyleName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// afterHyphen returns the style half of a PostScript name, which is everything
// past the last hyphen. A name with no hyphen has no style half to read.
func afterHyphen(s string) string {
	if i := strings.LastIndex(s, "-"); i >= 0 {
		return s[i+1:]
	}
	return ""
}

// foldFontName is the key two font names are compared under: case and the
// spaces between words do not tell two fonts apart, and neither do the naming
// habits of whoever built the file.
func foldFontName(s string) string {
	return strings.ToLower(squeezeSpace(s))
}

// indexPostScript finds an exact PostScript name.
func indexPostScript(name string) (FontFace, bool) {
	f, ok := loadFontIndex().byPostScript[foldFontName(name)]
	if !ok {
		return FontFace{}, false
	}
	return FontFace{File: f.File, Index: f.Index, Name: f.PostScript}, true
}

// indexFamily finds the face a family name on its own asks for: the upright,
// regular-weight one.
//
// A family that ships no regular at all still resolves, to its lightest upright
// face. Some families are only ever a Light or a Medium, and drawing one of
// those beats falling back to Go Mono and losing every icon.
func indexFamily(name string) (FontFace, bool) {
	if face, ok := pickFace(name, func(f indexedFace) bool {
		return f.Weight == weightRegular && !f.Italic
	}); ok {
		return face, true
	}
	return pickFace(name, func(f indexedFace) bool { return !f.Italic })
}

// indexBoldFamily finds the upright bold cut of a family, and nothing else. A
// family with no bold is better double-struck than drawn in its ExtraBold.
func indexBoldFamily(name string) (FontFace, bool) {
	return pickFace(name, func(f indexedFace) bool {
		return f.Weight == weightBold && !f.Italic
	})
}

// pickFace takes the first face of a family that want accepts. Nothing
// acceptable is a miss, not an excuse to substitute: answering with a face
// nobody asked for is the habit this package exists to avoid.
func pickFace(name string, want func(indexedFace) bool) (FontFace, bool) {
	for _, f := range loadFontIndex().byFamily[foldFontName(name)] {
		if want(f) {
			return FontFace{File: f.File, Index: f.Index, Name: familyLabel(f)}, true
		}
	}
	return FontFace{}, false
}

// familyLabel is the name a resolved face reports back, preferring the
// typographic family because that is the name that was typed to find it.
func familyLabel(f indexedFace) string {
	if f.TypographicFamly != "" {
		return f.TypographicFamly
	}
	return f.Family
}
