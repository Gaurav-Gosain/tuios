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
	"time"

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
	// A framed case is a popup, so view.placement says so.
	s.SetPlacement("popup")
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
	for y := range h {
		total := 0
		for x := range w {
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

// motionStep is one frame of a motion case: the clock, and a state path to
// set just before the frame is drawn.
type motionStep struct {
	at    time.Duration
	path  string
	value any
}

// motionCases are the documents of testdata/motion and the frames each is
// drawn at, before and during its effect.
var motionCases = []struct {
	name  string
	w, h  int
	steps []motionStep
}{
	{"fade", 44, 8, []motionStep{{at: 0}, {at: 60 * time.Millisecond}, {at: 120 * time.Millisecond}, {at: 400 * time.Millisecond}}},
	{"stagger", 40, 14, []motionStep{{at: 0}, {at: 100 * time.Millisecond}, {at: 200 * time.Millisecond}, {at: 600 * time.Millisecond}}},
	{"iris", 44, 9, []motionStep{{at: 0}, {at: 90 * time.Millisecond}, {at: 180 * time.Millisecond}, {at: 500 * time.Millisecond}}},
	{"decode", 48, 6, []motionStep{{at: 0}, {at: 150 * time.Millisecond}, {at: 300 * time.Millisecond}, {at: 700 * time.Millisecond}}},
	{"exit", 40, 8, []motionStep{{at: 0}, {at: 300 * time.Millisecond}, {at: 330 * time.Millisecond, path: "state.show", value: false},
		{at: 390 * time.Millisecond}, {at: 450 * time.Millisecond}, {at: 600 * time.Millisecond}}},
	{"skeleton", 40, 9, []motionStep{{at: 0}, {at: 500 * time.Millisecond}, {at: 1000 * time.Millisecond},
		{at: 1100 * time.Millisecond, path: "state.loading", value: false}, {at: 1160 * time.Millisecond}, {at: 1500 * time.Millisecond}}},
	{"toast", 40, 6, []motionStep{{at: 0}, {at: 500 * time.Millisecond}, {at: 1080 * time.Millisecond}, {at: 1300 * time.Millisecond},
		{at: 1400 * time.Millisecond, path: "state.msg", value: "Saved theme.toml"}, {at: 1480 * time.Millisecond}, {at: 1700 * time.Millisecond}}},
}

// motionSurface opens a motion case with the effects on and a clock the
// test moves.
func motionSurface(t testing.TB, name string, d Depth, now *time.Time, motion bool) *Surface {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "motion", name+".tml"))
	if err != nil {
		t.Fatal(err)
	}
	doc, problems, err := tml.Check(src, nil)
	if err != nil {
		t.Fatalf("%s: %v %v", name, err, problems)
	}
	for _, p := range problems {
		if p.Severity == tml.SevError {
			t.Fatalf("%s: %v", name, p)
		}
	}
	s, err := New(doc, &Options{Palette: DefaultPalette(d), CellAspect: cellAspect, Motion: motion, Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestMotionGolden draws each motion case at its steps, at the four depths,
// and compares the cells with the golden file. It also holds the rules of
// the clock: a surface that has finished moving asks for no frame, and with
// motion off the first frame is the last.
func TestMotionGolden(t *testing.T) {
	t0 := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	for _, c := range motionCases {
		t.Run(c.name, func(t *testing.T) {
			var b strings.Builder
			for _, d := range Depths() {
				now := t0
				s := motionSurface(t, c.name, d, &now, true)
				for _, st := range c.steps {
					now = t0.Add(st.at)
					if st.path != "" {
						if err := s.Set(st.path, st.value); err != nil {
							t.Fatal(err)
						}
					}
					buf := s.Render(c.w, c.h)
					checkFits(t, buf, c.w, c.h)
					fmt.Fprintf(&b, "=== %s %dx%d at %s\n", d, c.w, c.h, st.at)
					b.WriteString(dumpBuffer(buf, DefaultPalette(d)))
				}
				// Run the clock out: past every effect and ttl, the surface
				// draws once more and then asks for nothing.
				for range 3 {
					now = now.Add(2 * time.Second)
					s.Render(c.w, c.h)
				}
				if c.name != "skeleton" {
					if wait, ok := s.NextFrame(); ok {
						t.Errorf("%s: idle surface asks for a frame in %s", d, wait)
					}
				}
			}
			compareGolden(t, filepath.Join("testdata", "golden", "motion-"+c.name+".golden"), b.String())
		})
	}
	// Motion off: the first frame is the finished one, and nothing waits.
	for _, name := range []string{"fade", "stagger", "iris", "decode"} {
		now := t0
		off := motionSurface(t, name, TrueColor, &now, false)
		first := dumpBuffer(off.Render(44, 14), DefaultPalette(TrueColor))
		if _, ok := off.NextFrame(); ok {
			t.Errorf("%s: motion off still asks for a frame", name)
		}
		on := motionSurface(t, name, TrueColor, &now, true)
		on.Render(44, 14)
		now = now.Add(5 * time.Second)
		on.Render(44, 14)
		on.Render(44, 14)
		if last := dumpBuffer(on.Render(44, 14), DefaultPalette(TrueColor)); last != first {
			t.Errorf("%s: with motion off the first frame differs from the last frame with motion on:\n%s\nwant\n%s", name, first, last)
		}
	}
}
