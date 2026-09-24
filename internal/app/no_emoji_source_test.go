package app

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// emojiDataPath is the pinned copy of the Unicode emoji property file. See
// testdata/unicode/README.md for where it came from.
const emojiDataPath = "testdata/unicode/emoji-data.txt"

// emojiSourceAllowed lists the non-test files whose string literals may hold
// emoji code points, each with the reason. Every entry is data that describes
// another program's output or input, not text tuios shows as its own. A new
// entry is a decision somebody makes in review, which is the point.
var emojiSourceAllowed = map[string]string{
	// Detection code that matches the option cursors other agents draw.
	"internal/harness/answers.go": "matches option cursors agent CLIs draw",
	"internal/harness/prompt.go":  "matches prompt cursors agent CLIs draw",
	// The fuzzers feed the emulator emoji on purpose, to test widths.
	"internal/fuzz/vtgen/vtgen.go": "emulator width input",
	"internal/fuzz/pools.go":       "emulator width input",
	// The prompt parser skips the icons eza and tree print before a name.
	"internal/scrollback/parser.go": "matches icons other programs print",
	// The browser tour's fake Claude Code draws Claude Code's own spinner.
	"internal/webshell/agent.go": "imitates another program's screen",
	// macOS Option-key output is what the keyboard sends, not what we show.
	"internal/config/keynormalizer.go": "macOS Option-key character map",
}

// emojiSourceRoots are the trees the lint reads.
var emojiSourceRoots = []string{"internal", "cmd"}

// loadEmojiRunes reads the code points emoji-data.txt gives any emoji
// property. ASCII is left out: '#', '*' and the digits are Emoji=Yes only as
// keycap bases, and they are plain text everywhere tuios uses them.
func loadEmojiRunes(t *testing.T) map[rune]string {
	t.Helper()
	f, err := os.Open(emojiDataPath)
	if err != nil {
		t.Fatalf("open %s: %v", emojiDataPath, err)
	}
	defer f.Close()
	out := map[rune]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Split(line, ";")
		if len(fields) != 2 {
			continue
		}
		prop := strings.TrimSpace(fields[1])
		lo, hi, found := strings.Cut(strings.TrimSpace(fields[0]), "..")
		if !found {
			hi = lo
		}
		first, err1 := strconv.ParseUint(lo, 16, 32)
		last, err2 := strconv.ParseUint(hi, 16, 32)
		if err1 != nil || err2 != nil {
			t.Fatalf("bad range %q in %s", fields[0], emojiDataPath)
		}
		for r := rune(first); r <= rune(last); r++ {
			if r < 0x80 {
				continue
			}
			if _, ok := out[r]; !ok {
				out[r] = prop
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	// U+FE0F asks for emoji presentation of whatever precedes it, which is the
	// thing to keep out even when the base alone would be text.
	out[0xFE0F] = "Emoji_Presentation selector"
	// Not emoji, but held to the same rule: the return key is drawn one way,
	// overlay.EnterGlyph in a hint (EnterKey where it is spelled out), and
	// this second glyph for it is missing from many fonts.
	out[0x23CE] = "second return glyph; use overlay.EnterGlyph"
	if len(out) < 1000 {
		t.Fatalf("read %d emoji code points; the data file is not what this test expects", len(out))
	}
	return out
}

// TestNoEmojiInSourceStrings fails on an emoji code point, U+FE0F or any other
// character with an emoji property in a string or rune literal of a non-test
// Go file under internal/ and cmd/.
//
// Terminals draw these as colour pictures when they fall back to an emoji
// font, often two cells wide in a one-cell slot, and the maintainer's rule is
// no emoji anywhere in tuios's UI or code. Harness manifests are TOML and are
// not read here; Go files that hold other programs' glyphs as data are listed
// in emojiSourceAllowed.
func TestNoEmojiInSourceStrings(t *testing.T) {
	emoji := loadEmojiRunes(t)
	root := moduleRoot(t)
	fset := token.NewFileSet()
	var bad []string
	files := 0
	for _, sub := range emojiSourceRoots {
		err := filepath.WalkDir(filepath.Join(root, sub), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if _, ok := emojiSourceAllowed[rel]; ok {
				return nil
			}
			hits, err := emojiLiteralHits(fset, path, rel, emoji)
			if err != nil {
				return err
			}
			files++
			bad = append(bad, hits...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sub, err)
		}
	}
	if files < 100 {
		t.Fatalf("read %d files; this lint is no longer looking at the source", files)
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Error(b)
	}
}

// emojiLiteralHits parses one Go file and reports every emoji code point in
// its string and rune literals, after unquoting, so an escape such as
// "\U0001F44D" or "\xf0\x9f\x91\x8d" is caught as surely as the literal
// character.
func emojiLiteralHits(fset *token.FileSet, path, rel string, emoji map[rune]string) ([]string, error) {
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || (lit.Kind != token.STRING && lit.Kind != token.CHAR) {
			return true
		}
		val, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		for _, r := range val {
			if prop, ok := emoji[r]; ok {
				pos := fset.Position(lit.Pos())
				hits = append(hits, fmt.Sprintf("%s:%d: U+%04X %q (%s)", rel, pos.Line, r, string(r), prop))
			}
		}
		return true
	})
	return hits, nil
}

// TestEmojiSourceAllowListIsLive keeps the allow list from outliving its
// reasons: a listed file that no longer exists, or no longer holds an emoji
// code point, is taken off.
func TestEmojiSourceAllowListIsLive(t *testing.T) {
	emoji := loadEmojiRunes(t)
	root := moduleRoot(t)
	fset := token.NewFileSet()
	for rel := range emojiSourceAllowed {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s is allowed emoji but cannot be read: %v", rel, err)
			continue
		}
		hits, err := emojiLiteralHits(fset, path, rel, emoji)
		if err != nil {
			t.Errorf("parse %s: %v", rel, err)
			continue
		}
		if len(hits) == 0 {
			t.Errorf("%s is allowed emoji but holds none; take it off emojiSourceAllowed", rel)
		}
	}
}
