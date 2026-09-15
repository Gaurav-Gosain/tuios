package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withoutFontconfig hides fc-match for the duration of a test, which is what
// an untouched macOS looks like. The font cache and the index are dropped on
// the way in and the way out so neither test sees the other's answers.
func withoutFontconfig(t *testing.T) {
	t.Helper()
	old := os.Getenv("PATH")
	t.Setenv("PATH", "")
	resetFontCache()
	t.Cleanup(func() {
		_ = os.Setenv("PATH", old)
		resetFontCache()
	})
}

// anyIndexedFamily picks a family this machine actually has, so the tests can
// ask for a real font without naming one and hoping.
func anyIndexedFamily(t *testing.T) indexedFace {
	t.Helper()
	for _, faces := range loadFontIndex().byFamily {
		for _, f := range faces {
			if f.Weight == weightRegular && !f.Italic && f.Family != "" {
				return f
			}
		}
	}
	t.Skip("no fonts found on this machine")
	return indexedFace{}
}

// TestTheIndexResolvesWithoutFontconfig is the bug this file exists for. A Mac
// has no fc-match, so every lookup used to miss and every capture was drawn in
// Go Mono, which has no icons in it.
//
// Negative control: dropping the indexFamily arm from FontByFamily made this
// fail with no font found, which is the old behaviour exactly.
func TestTheIndexResolvesWithoutFontconfig(t *testing.T) {
	want := anyIndexedFamily(t)
	withoutFontconfig(t)

	face, ok := FontByFamily(want.Family)
	if !ok {
		t.Fatalf("%q did not resolve with no fontconfig on the machine", want.Family)
	}
	if _, err := os.Stat(face.File); err != nil {
		t.Errorf("resolved %q to %s, which does not open: %v", want.Family, face.File, err)
	}
}

// TestThePostScriptNameResolvesWithoutFontconfig covers the other half of the
// same bug. A terminal answers a font query with a PostScript name, so this is
// the lookup that makes a default capture come out in the user's own font.
func TestThePostScriptNameResolvesWithoutFontconfig(t *testing.T) {
	var want indexedFace
	for _, f := range loadFontIndex().byPostScript {
		if f.PostScript != "" {
			want = f
			break
		}
	}
	if want.PostScript == "" {
		t.Skip("no fonts found on this machine")
	}
	withoutFontconfig(t)

	face, ok := FontByPostScriptName(want.PostScript)
	if !ok {
		t.Fatalf("PostScript name %q did not resolve with no fontconfig", want.PostScript)
	}
	if !strings.EqualFold(face.Name, want.PostScript) {
		t.Errorf("asked for %q and got %q", want.PostScript, face.Name)
	}
}

// TestTheIndexRefusesAFontNobodyHas is the substitution guard, held to the
// index rather than to fc-match. The index has no reason to substitute, and
// this is what keeps it that way.
//
// Negative control: making pickFace return faces[0] when nothing matched
// resolved a nonsense family and this failed.
func TestTheIndexRefusesAFontNobodyHas(t *testing.T) {
	withoutFontconfig(t)
	if face, ok := FontByFamily("No Such Font Family XYZ"); ok {
		t.Errorf("a family nobody has resolved to %s (%s)", face.File, face.Name)
	}
	if face, ok := FontByPostScriptName("NoSuchFontFamilyXYZ-Regular"); ok {
		t.Errorf("a PostScript name nobody has resolved to %s (%s)", face.File, face.Name)
	}
}

// TestAGenericNameIsStillSkippedWithoutFontconfig keeps the CSS-stack rule
// honest on the index path: "monospace" names a category, and answering it
// with whatever the index happens to hold is the substitution this package
// refuses everywhere else.
func TestAGenericNameIsStillSkippedWithoutFontconfig(t *testing.T) {
	withoutFontconfig(t)
	if face, ok := FontByFamily("No Such Font XYZ, monospace, sans-serif"); ok {
		t.Errorf("a stack of nothing but generics resolved to %s", face.File)
	}
}

// TestBoldIsADifferentFaceFromRegular checks the weight split. A family whose
// bold cut resolves to the regular face is a family with no bold cut, and
// BoldFontByFamily is specified to report nothing rather than hand back the
// regular face under a bold name.
//
// "A different face" is a file and an index. A collection keeps every weight of
// a family in one file, so requiring different paths here would call Menlo
// Bold the same font as Menlo.
//
// Negative control: comparing only the paths failed on the first macOS family
// that ships as a .ttc, which is most of them.
func TestBoldIsADifferentFaceFromRegular(t *testing.T) {
	idx := loadFontIndex()
	var family string
	for key, faces := range idx.byFamily {
		var hasRegular, hasBold bool
		for _, f := range faces {
			hasRegular = hasRegular || (f.Weight == weightRegular && !f.Italic)
			hasBold = hasBold || (f.Weight == weightBold && !f.Italic)
		}
		if hasRegular && hasBold {
			family = key
			break
		}
	}
	if family == "" {
		t.Skip("no family with both a regular and a bold cut on this machine")
	}
	withoutFontconfig(t)

	reg, ok := FontByFamily(family)
	if !ok {
		t.Fatalf("%q has a regular cut in the index but did not resolve", family)
	}
	bold, ok := BoldFontByFamily(family, reg)
	if !ok {
		t.Fatalf("%q has a bold cut in the index but did not resolve", family)
	}
	if bold.File == reg.File && bold.Index == reg.Index {
		t.Errorf("bold and regular both resolved to face %d of %s", reg.Index, reg.File)
	}
}

// TestStyleTraitsSeparatesTheWeights pins the trap in reading a weight off a
// name. A weight is not a word: "Extra Bold", "ExtraBold" and "extrabold" are
// one weight spelled three ways, and a style that merely contains "bold" is as
// likely to be SemiBold as Bold.
//
// Both mistakes have the same consequence, because a face that is neither bold
// nor italic used to pass for regular: reading ExtraBold as "not bold" made it
// the regular, and reading it as "bold" would make it the bold cut. It is
// neither.
//
// Negative control: classifying by whole words instead of the folded name made
// "Extra Bold" come back weightBold and this failed; treating every non-bold
// face as regular made "ExtraBold" come back weightRegular and this failed.
func TestStyleTraitsSeparatesTheWeights(t *testing.T) {
	for _, tc := range []struct {
		style, postScript string
		want              weightClass
		italic            bool
	}{
		{"Regular", "JetBrainsMonoNF-Regular", weightRegular, false},
		{"", "", weightRegular, false},
		{"Book", "Whatever-Book", weightRegular, false},
		{"Bold", "JetBrainsMonoNF-Bold", weightBold, false},
		{"Italic", "JetBrainsMonoNF-Italic", weightRegular, true},
		{"Bold Italic", "JetBrainsMonoNF-BoldItalic", weightBold, true},
		{"BoldItalic", "JetBrainsMonoNF-BoldItalic", weightBold, true},
		{"Oblique", "Whatever-Oblique", weightRegular, true},
		{"SemiBold", "JetBrainsMonoNF-SemiBold", weightOther, false},
		{"ExtraBold", "JetBrainsMonoNF-ExtraBold", weightOther, false},
		{"Extra Bold", "JetBrainsMonoNF-ExtraBold", weightOther, false},
		{"Thin", "JetBrainsMonoNF-Thin", weightOther, false},
		{"Light Italic", "JetBrainsMonoNF-LightItalic", weightOther, true},
		{"Black", "Whatever-Black", weightOther, false},
		{"", "SomeFont-Bold", weightBold, false},
		{"", "SomeFont", weightRegular, false},
	} {
		got, italic := styleTraits(tc.style, tc.postScript)
		if got != tc.want || italic != tc.italic {
			t.Errorf("styleTraits(%q, %q) = weight:%v italic:%v, want weight:%v italic:%v",
				tc.style, tc.postScript, got, italic, tc.want, tc.italic)
		}
	}
}

// TestTheIndexReadsACollection checks TTC support, which is not optional on
// macOS: the system ships most of its own faces, Menlo included, as
// collections, so a scan that skipped them would skip the fonts most likely to
// be asked for.
func TestTheIndexReadsACollection(t *testing.T) {
	var ttc string
	for _, faces := range loadFontIndex().byFamily {
		for _, f := range faces {
			if strings.EqualFold(filepath.Ext(f.File), ".ttc") {
				ttc = f.File
				break
			}
		}
	}
	if ttc == "" {
		t.Skip("no font collections on this machine")
	}
	if got := readFontNames(ttc); len(got) == 0 {
		t.Errorf("%s parsed to no faces at all", ttc)
	}
}

// TestAnUnreadableDirectoryIsNotFatal keeps the walk forgiving. A font
// directory that cannot be read is one place with no fonts in it.
func TestAnUnreadableDirectoryIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "truncated.ttf")
	if err := os.WriteFile(junk, []byte("not a font"), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := buildFontIndex([]string{
		filepath.Join(dir, "does-not-exist"),
		dir,
		"/proc/nonexistent/fonts",
	})
	if idx == nil {
		t.Fatal("the scan gave up entirely")
	}
	if len(idx.byFamily) != 0 {
		t.Errorf("a directory of junk produced %d families", len(idx.byFamily))
	}
}
