package app

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// depDiffOut is the directory a dependency-bump differential run writes its
// dumps into. Empty means the harness is skipped, so an ordinary test run
// never pays for it.
func depDiffOut(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("TUIOS_DEPDIFF_OUT")
	if dir == "" {
		t.Skip("TUIOS_DEPDIFF_OUT unset")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

func writeDump(t *testing.T, name, body string) {
	t.Helper()
	path := filepath.Join(depDiffOut(t), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("wrote %s (%d bytes)", path, len(body))
}

// depDiffStrings is the measurement corpus. It leans on the codepoint ranges
// that moved between go-runewidth v0.0.24 and v0.0.28 (Hebrew niqqud, Arabic
// harakat, Indic and south-east Asian marks) plus the emoji, CJK and
// ambiguous-width cases the UI already has open bugs around.
var depDiffStrings = []struct{ name, s string }{
	{"ascii", "hello world"},
	{"ascii-sgr", "\x1b[31mhello\x1b[0m world"},
	{"cjk", "日本語"},
	{"cjk-mixed", "abc日本語def"},
	{"hangul", "한국어"},
	{"fullwidth", "ＡＢＣ"},
	{"halfwidth-kana", "ｱｲｳ"},
	{"emoji-basic", "hi 👋 there"},
	{"emoji-zwj-family", "👨‍👩‍👧‍👦"},
	{"emoji-zwj-pair", "👩‍💻"},
	{"emoji-skin-tone", "👋🏽"},
	{"emoji-flag", "🇯🇵"},
	{"emoji-keycap", "1️⃣"},
	{"emoji-text-vs", "❤︎"},
	{"emoji-emoji-vs", "❤️"},
	{"combining-acute", "éclair"},
	{"combining-stack", "à́̂̃"},
	{"hebrew-niqqud", "שָׁלוֹם"},
	{"hebrew-point-alone", "ְ"},
	{"arabic-harakat", "مَرْحَبًا"},
	{"arabic-mark-alone", "ً"},
	{"devanagari", "हिन्दी"},
	{"devanagari-mark-alone", "ु"},
	{"bengali", "বাংলা"},
	{"bengali-mark-alone", "ু"},
	{"tamil", "தமிழ்"},
	{"kannada-mark-alone", "ಿ"},
	{"thai", "กันยายน"},
	{"thai-mark-alone", "ั"},
	{"lao-mark-alone", "ັ"},
	{"tibetan", "བོད་"},
	{"tibetan-mark-alone", "ཱ"},
	{"myanmar-mark-alone", "ိ"},
	{"tagalog-mark-alone", "ᜒ"},
	{"khmer", "ខ្មែរ"},
	{"mongolian-fvs", "ᠭ᠋"},
	{"ambiguous-greek", "αβγ"},
	{"ambiguous-cyrillic", "абв"},
	{"ambiguous-box", "─│┌"},
	{"powerline", ""},
	{"nerdfont-icon", " "},
	{"zero-width-space", "a​b"},
	{"soft-hyphen", "a­b"},
	{"bidi-controls", "a‮b‬c"},
	{"regional-pair", "🇦🇧"},
	{"tag-sequence", "🏴󠁧󠁢󠁳󠁣󠁴󠁿"},
	{"surrogate-plane-mark", "\U0001e000"},
	{"cjk-ext-b", "\U00020000"},
	{"window-name-wide", "世 pane"},
	{"window-name-emoji", "🚀 pane"},
}

// TestDepDiffWidth dumps every width measurement the UI depends on, over the
// corpus and over the whole codepoint space, so a dependency bump can be
// diffed rather than trusted.
func TestDepDiffWidth(t *testing.T) {
	depDiffOut(t)
	var b strings.Builder

	b.WriteString("## corpus\n")
	b.WriteString("name\tansi.StringWidth\tansi.StringWidthWc\tlipgloss.Width\tlineWidth\tbytes\n")
	for _, c := range depDiffStrings {
		fmt.Fprintf(&b, "%s\t%d\t%d\t%d\t%d\t%q\n",
			c.name,
			ansi.StringWidth(c.s),
			ansi.StringWidthWc(c.s),
			lipgloss.Width(c.s),
			lineWidth(c.s),
			c.s,
		)
	}

	b.WriteString("\n## per-codepoint sweep (only runes whose width is not 1)\n")
	b.WriteString("rune\tgrapheme\twc\n")
	for r := rune(0); r <= 0x2FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue // surrogates are not valid runes
		}
		s := string(r)
		g := ansi.StringWidth(s)
		w := ansi.StringWidthWc(s)
		if g == 1 && w == 1 {
			continue
		}
		fmt.Fprintf(&b, "U+%04X\t%d\t%d\n", r, g, w)
	}

	writeDump(t, "width.txt", b.String())
}

// TestDepDiffFrame dumps composed frames for the layouts most exposed to a
// width change: shared borders, wide runes in pane content, and wide runes in
// a window name.
func TestDepDiffFrame(t *testing.T) {
	depDiffOut(t)
	origShared := config.SharedBorders
	t.Cleanup(func() { config.SharedBorders = origShared })

	guests := []struct{ name, text string }{
		{"ascii", "PANEEDGE"},
		{"wide", "世世世世世"},
		{"emoji", "👋👋👋👋"},
		{"zwj", "👨‍👩‍👧‍👦x"},
		{"combining", "ééé"},
		{"arabic", "مَرْحَبًا"},
		{"thai", "กันยายน"},
		{"mixed", "ab世cd👋ef"},
	}

	var b strings.Builder
	for _, shared := range []bool{false, true} {
		config.SharedBorders = shared
		for _, panes := range []int{2, 3, 5} {
			for _, g := range guests {
				name := fmt.Sprintf("shared=%v/panes=%d/%s", shared, panes, g.name)
				frame := depDiffFrame(t, panes, g.text)
				fmt.Fprintf(&b, "### %s\n", name)
				b.WriteString(frame)
				b.WriteString("\n")
			}
		}
	}
	writeDump(t, "frame.txt", b.String())
}

// depDiffFrame paints text into every pane and returns the composed frame,
// stripped of styling, with each row prefixed by its measured width.
func depDiffFrame(t *testing.T, panes int, text string) string {
	t.Helper()
	m := gapTestOS(t, panes)
	for i, w := range m.Windows {
		w.SetTitle(fmt.Sprintf("%s-%d", text, i))
		w.LockIO()
		_, _ = w.Terminal.Write([]byte(text))
		w.UnlockIO()
		w.MarkContentDirty()
	}
	m.TileAllWindows()
	out := ansi.Strip(lipgloss.Sprint(m.GetCanvas(true).Render()))
	var b strings.Builder
	for i, row := range strings.Split(out, "\n") {
		fmt.Fprintf(&b, "%3d w=%3d |%s|\n", i, ansi.StringWidth(row), row)
	}
	return b.String()
}

// TestDepDiffWrap dumps the wrapping and truncation helpers, which decide
// where text is cut and are measured with the same tables.
func TestDepDiffWrap(t *testing.T) {
	depDiffOut(t)
	var b strings.Builder
	widths := []int{1, 2, 3, 5, 8, 13}
	names := make([]string, 0, len(depDiffStrings))
	byName := map[string]string{}
	for _, c := range depDiffStrings {
		names = append(names, c.name)
		byName[c.name] = c.s
	}
	sort.Strings(names)
	for _, n := range names {
		s := byName[n]
		for _, w := range widths {
			fmt.Fprintf(&b, "%s\tw=%d\ttrunc=%q\thard=%q\twrap=%q\tcut=%q\n",
				n, w,
				ansi.Truncate(s, w, "…"),
				ansi.Hardwrap(s, w, false),
				ansi.Wrap(s, w, ""),
				ansi.Cut(s, 0, w),
			)
		}
	}
	writeDump(t, "wrap.txt", b.String())
}

// TestDepDiffStyle dumps composed frames with their styling intact. frame.txt
// strips escape sequences, which is what width evidence wants and exactly
// wrong for a bump that moves a colour: the palette modules and the terminfo
// lookup decide the bytes that carry it, and a stripped frame cannot see them.
func TestDepDiffStyle(t *testing.T) {
	depDiffOut(t)
	origShared := config.SharedBorders
	t.Cleanup(func() { config.SharedBorders = origShared })

	var b strings.Builder
	for _, shared := range []bool{false, true} {
		config.SharedBorders = shared
		for _, panes := range []int{2, 4} {
			for _, text := range []string{"PANEEDGE", "世世世", "👋👋"} {
				m := gapTestOS(t, panes)
				for i, w := range m.Windows {
					w.SetTitle(fmt.Sprintf("%s-%d", text, i))
					w.LockIO()
					_, _ = w.Terminal.Write([]byte(text))
					w.UnlockIO()
					w.MarkContentDirty()
				}
				m.TileAllWindows()
				out := lipgloss.Sprint(m.GetCanvas(true).Render())
				fmt.Fprintf(&b, "### shared=%v panes=%d text=%s\n", shared, panes, text)
				for i, row := range strings.Split(out, "\n") {
					fmt.Fprintf(&b, "%3d %q\n", i, row)
				}
				b.WriteString("\n")
			}
		}
	}
	writeDump(t, "style.txt", b.String())
}

// hexOf renders a colour the way a diff can read it. A theme can hand back a
// typed nil pointer in a non-nil interface, which only shows up when RGBA is
// called, so this recovers rather than letting one unset colour end the dump.
func hexOf(c color.Color) (s string) {
	if c == nil {
		return "nil"
	}
	defer func() {
		if recover() != nil {
			s = "unset"
		}
	}()
	r, g, bl, a := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x/%04x", r>>8, g>>8, bl>>8, a)
}

// TestDepDiffPalette dumps every colour the theme layer resolves, for every
// registered theme, so a palette bump shows up as a diff rather than as a
// surprise on screen.
func TestDepDiffPalette(t *testing.T) {
	depDiffOut(t)
	// Initialize below mutates process-wide theme state, so restore whatever
	// the suite was using; a skipped dump must leave no trace either way.
	orig := theme.CurrentThemeID()
	t.Cleanup(func() { _ = theme.Initialize(orig) })
	theme.EnsureRegistry()
	names := theme.AvailableThemes()
	sort.Strings(names)

	accessors := []struct {
		name string
		fn   func() color.Color
	}{
		{"TerminalFg", theme.TerminalFg},
		{"TerminalBg", theme.TerminalBg},
		{"TerminalCursor", theme.TerminalCursor},
		{"BorderUnfocused", theme.BorderUnfocused},
		{"BorderFocusedWindow", theme.BorderFocusedWindow},
		{"BorderFocusedTerminal", theme.BorderFocusedTerminal},
		{"DockColorWindow", theme.DockColorWindow},
		{"DockColorTerminal", theme.DockColorTerminal},
		{"DockColorCopy", theme.DockColorCopy},
		{"NotificationError", theme.NotificationError},
		{"NotificationWarning", theme.NotificationWarning},
		{"NotificationSuccess", theme.NotificationSuccess},
	}

	var b strings.Builder
	for _, n := range names {
		if err := theme.Initialize(n); err != nil {
			fmt.Fprintf(&b, "### %s -> init error %v\n", n, err)
			continue
		}
		fmt.Fprintf(&b, "### %s\n", n)
		for i, c := range theme.GetANSIPalette() {
			fmt.Fprintf(&b, "  ansi[%02d]\t%s\n", i, hexOf(c))
		}
		for _, a := range accessors {
			fmt.Fprintf(&b, "  %s\t%s\n", a.name, hexOf(a.fn()))
		}
		p := theme.UI()
		fmt.Fprintf(&b, "  ui.Canvas\t%s\n", hexOf(p.Canvas))
		fmt.Fprintf(&b, "  ui.Panel\t%s\n", hexOf(p.Panel))
		fmt.Fprintf(&b, "  ui.Surface\t%s\n", hexOf(p.Surface))
		fmt.Fprintf(&b, "  ui.RowSel\t%s\n", hexOf(p.RowSel))
		fmt.Fprintf(&b, "  ui.Card\t%s\n", hexOf(p.Card))
		fmt.Fprintf(&b, "  ui.Selected\t%s\n", hexOf(p.Selected))
		fmt.Fprintf(&b, "  ui.Fg\t%s\n", hexOf(p.Fg))
		fmt.Fprintf(&b, "  ui.FgDim\t%s\n", hexOf(p.FgDim))
		fmt.Fprintf(&b, "  ui.FgMute\t%s\n", hexOf(p.FgMute))
		fmt.Fprintf(&b, "  ui.Accent\t%s\n", hexOf(p.Accent))
		fmt.Fprintf(&b, "  ui.AccentBright\t%s\n", hexOf(p.AccentBright))
		fmt.Fprintf(&b, "  ui.PillFg\t%s\n", hexOf(p.PillFg))
		fmt.Fprintf(&b, "  ui.Warn\t%s\n", hexOf(p.Warn))
		fmt.Fprintf(&b, "  ui.Success\t%s\n", hexOf(p.Success))
		fmt.Fprintf(&b, "  ui.Info\t%s\n", hexOf(p.Info))
		fmt.Fprintf(&b, "  ui.Warning\t%s\n", hexOf(p.Warning))
		for _, sw := range theme.ThemeSwatch(n) {
			fmt.Fprintf(&b, "  swatch\t%s\n", hexOf(sw))
		}
	}
	writeDump(t, "palette.txt", b.String())
}

// TestDepDiffContrast sweeps the contrast maths over a colour grid. These are
// the functions that reach go-colorful, and they pick the foreground drawn on
// every panel, so a change in the colour space shows up here first.
func TestDepDiffContrast(t *testing.T) {
	depDiffOut(t)
	step := 0x33
	var grid []color.Color
	for r := 0; r <= 0xff; r += step {
		for g := 0; g <= 0xff; g += step {
			for bl := 0; bl <= 0xff; bl += step {
				grid = append(grid, color.RGBA{R: uint8(r), G: uint8(g), B: uint8(bl), A: 0xff})
			}
		}
	}

	var b strings.Builder
	for _, bg := range grid {
		fmt.Fprintf(&b, "%s\tcontrastText=%s\n", hexOf(bg), hexOf(theme.ContrastText(bg)))
	}
	for i, fg := range grid {
		bg := grid[len(grid)-1-i]
		fmt.Fprintf(&b, "%s on %s\tratio=%.6f\treadable=%s\n",
			hexOf(fg), hexOf(bg), theme.ContrastRatio(fg, bg), hexOf(theme.Readable(fg, bg)))
	}
	writeDump(t, "contrast.txt", b.String())
}
