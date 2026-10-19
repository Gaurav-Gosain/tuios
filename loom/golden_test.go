package loom

import (
	"flag"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"tuios.dev/tml"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// goldenCase is a document and the size to draw it at. A case file starts
// with a comment that gives the size, such as <!-- 48x10 -->, and may ask
// for a frame around the surface with <!-- 64x16 frame -->.
type goldenCase struct {
	name  string
	path  string
	w, h  int
	frame bool
	owner bool // the frame names an owner, as a tuios popup does
}

var sizeLine = regexp.MustCompile(`^<!--(.*)-->`)
var sizeItem = regexp.MustCompile(`^\s*(\d+)x(\d+)(\s+frame(-owner)?)?\s*$`)

// cellAspect is the cell of the frames: JetBrainsMono NFM through shot at
// scale 1 is 8 by 19 pixels.
const cellAspect = 19.0 / 8

// loadCases reads the cases of a folder. The first line of a case file
// gives the sizes, comma separated: <!-- 48x10 --> or
// <!-- 150x38 frame, 100x30 frame -->. A case with several sizes is named
// NAME-WxH for each.
func loadCases(t *testing.T, dir string) []goldenCase {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.tml"))
	if err != nil {
		t.Fatal(err)
	}
	var out []goldenCase
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		first, _, _ := strings.Cut(string(src), "\n")
		m := sizeLine.FindStringSubmatch(first)
		if m == nil {
			t.Fatalf("%s: the first line must give the size, such as <!-- 48x10 -->", f)
		}
		items := strings.Split(m[1], ",")
		for _, it := range items {
			sm := sizeItem.FindStringSubmatch(it)
			if sm == nil {
				t.Fatalf("%s: %q is not a size such as 48x10 or 64x16 frame", f, it)
			}
			w, _ := strconv.Atoi(sm[1])
			h, _ := strconv.Atoi(sm[2])
			name := strings.TrimSuffix(filepath.Base(f), ".tml")
			if len(items) > 1 {
				name += "-" + sm[1] + "x" + sm[2]
			}
			out = append(out, goldenCase{name: name, path: f, w: w, h: h, frame: sm[3] != "", owner: sm[4] != ""})
		}
	}
	return out
}

// render draws a case at one depth. A framed case draws the surface inside
// a host frame, as a popup shows it.
func (c goldenCase) render(t testing.TB, d Depth) *Buffer {
	t.Helper()
	src, err := os.ReadFile(c.path)
	if err != nil {
		t.Fatal(err)
	}
	doc, problems, err := tml.Check(src, nil)
	if err != nil {
		t.Fatalf("%s: %v %v", c.name, err, problems)
	}
	for _, p := range problems {
		if p.Severity == tml.SevError {
			t.Fatalf("%s: %v", c.name, p)
		}
	}
	pal := DefaultPalette(d)
	s, err := New(doc, &Options{Palette: pal, CellAspect: cellAspect})
	if err != nil {
		t.Fatal(err)
	}
	if !c.frame {
		return s.Render(c.w, c.h)
	}
	out := NewBuffer(c.w, c.h)
	owner := ""
	if c.owner {
		owner = "from claude in api-auth"
	}
	Frame(out, rect(0, 0, c.w, c.h), doc.AttrString("title"), owner, pal, true)
	inner := s.Render(c.w-4, c.h-2)
	for y := 0; y < inner.Height(); y++ {
		for x := 0; x < inner.Width(); x++ {
			if cell := inner.b.CellAt(x, y); cell != nil && cell.Width > 0 {
				cp := *cell
				out.b.SetCell(x+2, y+1, &cp)
			}
		}
	}
	return out
}

// TestGolden draws every component case and worked example at the four
// depths and compares the cells, text and style, with the golden file.
func TestGolden(t *testing.T) {
	for _, dir := range []string{"testdata/components", "testdata/examples-golden"} {
		for _, c := range loadCases(t, dir) {
			t.Run(c.name, func(t *testing.T) {
				var b strings.Builder
				for _, d := range Depths() {
					buf := c.render(t, d)
					checkFits(t, buf, c.w, c.h)
					fmt.Fprintf(&b, "=== %s %dx%d\n", d, c.w, c.h)
					b.WriteString(dumpBuffer(buf, DefaultPalette(d)))
				}
				compareGolden(t, filepath.Join("testdata", "golden", c.name+".golden"), b.String())
			})
		}
	}
}

func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs. Got:\n%s", path, got)
	}
}

// checkFits asserts that every row of the buffer is exactly w cells wide by
// the width of its graphemes, as ansi.StringWidth counts them.
func checkFits(t testing.TB, buf *Buffer, w, h int) {
	t.Helper()
	if h == 0 || w == 0 {
		// An empty buffer has no rows to measure its width by.
		if buf.Height() != h {
			t.Fatalf("buffer is %d rows, want %d", buf.Height(), h)
		}
		return
	}
	if buf.Width() != w || buf.Height() != h {
		t.Fatalf("buffer is %dx%d, want %dx%d", buf.Width(), buf.Height(), w, h)
	}
	for y := 0; y < h; y++ {
		total := 0
		for x := 0; x < w; x++ {
			c := buf.b.CellAt(x, y)
			if c.Width == 0 {
				continue
			}
			if got := width(c.Content); c.Content != " " && got != c.Width {
				t.Fatalf("row %d col %d: %q is %d cells, the cell says %d", y, x, c.Content, got, c.Width)
			}
			for _, r := range c.Content {
				if r < 0x20 || r >= 0x7f && r < 0xa0 {
					t.Fatalf("row %d col %d: control character %U", y, x, r)
				}
			}
			total += c.Width
		}
		if total != w {
			t.Fatalf("row %d is %d cells wide, want %d", y, total, w)
		}
	}
}

// dumpBuffer writes the text of a buffer between bars, then a letter per
// cell for its style, with a legend that names each style by its roles.
func dumpBuffer(buf *Buffer, p *Palette) string {
	roleOf := map[color.Color]string{}
	for _, r := range roleNames {
		if c := p.Color(r); c != nil {
			if _, ok := roleOf[c]; !ok {
				roleOf[c] = r
			}
		}
	}
	name := func(c color.Color) string {
		if c == nil {
			return ""
		}
		if r, ok := roleOf[c]; ok {
			return r
		}
		return fmt.Sprint(c)
	}
	var text, styles strings.Builder
	keys := map[string]byte{}
	next := byte('a')
	for y := 0; y < buf.Height(); y++ {
		text.WriteByte('|')
		styles.WriteByte('|')
		for x := 0; x < buf.Width(); x++ {
			c := buf.b.CellAt(x, y)
			if c.Width == 0 {
				continue
			}
			text.WriteString(c.Content)
			var k []string
			if f := name(c.Style.Fg); f != "" && c.Content != " " {
				k = append(k, "fg="+f)
			}
			if b := name(c.Style.Bg); b != "" {
				k = append(k, "bg="+b)
			}
			for _, a := range []struct {
				bit  uint8
				name string
			}{{attrBold, "bold"}, {attrItalic, "italic"}, {attrReverse, "reverse"}, {attrStrike, "strike"}, {attrFaint, "faint"}} {
				if c.Style.Attrs&a.bit != 0 {
					k = append(k, a.name)
				}
			}
			if c.Style.Underline != 0 {
				k = append(k, "underline")
			}
			if c.Link.URL != "" {
				k = append(k, "link")
			}
			key := strings.Join(k, " ")
			if key == "" {
				styles.WriteString(strings.Repeat(" ", c.Width))
				continue
			}
			l, ok := keys[key]
			if !ok {
				l = next
				keys[key] = l
				next = nextLetter(next)
			}
			styles.WriteString(strings.Repeat(string(l), c.Width))
		}
		text.WriteString("|\n")
		styles.WriteString("|\n")
	}
	var legend []string
	for k, l := range keys {
		legend = append(legend, fmt.Sprintf("%c: %s", l, k))
	}
	sort.Strings(legend)
	return text.String() + "--- styles\n" + styles.String() + strings.Join(legend, "\n") + "\n"
}

func nextLetter(b byte) byte {
	switch b {
	case 'z':
		return 'A'
	case 'Z':
		return '0'
	case '9':
		return '#'
	}
	return b + 1
}
