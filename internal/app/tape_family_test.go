package app

import (
	"image/color"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/tape/trust"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// tapeTestOS is a session with two tapes recorded and a project tape to review.
func tapeTestOS(t *testing.T) *OS {
	t.Helper()
	m := &OS{Width: 120, Height: 40, WorkspaceFocus: map[int]int{}, NumWorkspaces: 9, CurrentWorkspace: 1}
	m.InitTapeManager()
	stamp := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	m.TapeManager.Files = []TapeFile{
		{Name: "build-release", Size: 2048, Modified: stamp},
		{Name: "smoke-test", Size: 512, Modified: stamp},
	}
	m.TapeReview = &TapeReviewState{
		Path:    "/home/u/proj/.tuios.tape",
		Dir:     "/home/u/proj",
		Status:  trust.StatusUntrusted,
		Content: []byte("Type \"echo hi\"\nEnter\n"),
	}
	return m
}

// TestTapeSurfacesSpeakTheOverlayGrammar is the family pass: both tape surfaces
// drew a cyan rounded border on a black fill, with Title-Case "Enter Play"
// hints and magenta keys, which was one of the five palettes the app was
// carrying alongside its own.
func TestTapeSurfacesSpeakTheOverlayGrammar(t *testing.T) {
	m := tapeTestOS(t)

	for name, out := range map[string]string{
		"tape manager": m.RenderTapeManager(),
		"tape review":  m.RenderTapeReview(),
	} {
		plain := ansi.Strip(out)
		// A lowercase chip title, the shape every panel's title has.
		first := strings.TrimSpace(strings.Split(plain, "\n")[1])
		if first != strings.ToLower(first) {
			t.Errorf("%s: the title chip reads %q, the family's titles are lowercase", name, first)
		}
		// Chip hints, not the Title-Case pairs these two used.
		for _, banned := range []string{"Enter Play", "Esc Close", "Enter Confirm", "q:close", "Run once   "} {
			if strings.Contains(plain, banned) {
				t.Errorf("%s: the footer still reads %q", name, banned)
			}
		}
		// The panel's own ground, on every row it draws.
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(ansi.Strip(line)) == "" {
				continue
			}
			if !strings.Contains(line, "\x1b[") {
				t.Errorf("%s: a row is drawn with no styling, so it has no ground: %q", name, line)
			}
		}
	}
}

// TestTapeListSelectionFillsTheRow pins the change from ragged per-run slabs to
// a row: the selected tape's fill spans the panel from edge to edge, so the
// selection reads as a row rather than as a background painted around text.
func TestTapeListSelectionFillsTheRow(t *testing.T) {
	m := tapeTestOS(t)
	pal := theme.UI()
	out := m.RenderTapeManager()

	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(ansi.Strip(l), "build-release") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("the manager drew no row for the selected tape")
	}

	grounds := cellBackgrounds(line)
	width := lipgloss.Width(ansi.Strip(line))
	if len(grounds) != width {
		t.Fatalf("read %d cell grounds from a %d-cell row", len(grounds), width)
	}
	// The panel pads two cells either side of its content; the row is the rest.
	want := rgbaOf(pal.RowSel)
	filled := 0
	for _, g := range grounds {
		if g == want {
			filled++
		}
	}
	if inner := width - 4; filled < inner {
		t.Errorf("the selection fill covers %d of the row's %d cells: %q", filled, inner, ansi.Strip(line))
	}
}

// TestTapeSurfacesHoldUpInASCII checks the two surfaces carry no glyph a
// terminal without a capable font cannot draw.
func TestTapeSurfacesHoldUpInASCII(t *testing.T) {
	prev := config.UseASCIIOnly
	config.UseASCIIOnly = true
	overlay.SetASCII(true)
	t.Cleanup(func() {
		config.UseASCIIOnly = prev
		overlay.SetASCII(prev)
	})

	m := tapeTestOS(t)
	m.TapeManager.Mode = TapeManagerNaming
	m.TapeManager.NameBuffer = "my-tape"
	for name, out := range map[string]string{
		"tape manager": m.RenderTapeManager(),
		"tape review":  m.RenderTapeReview(),
	} {
		for _, r := range ansi.Strip(out) {
			if r > 127 {
				t.Errorf("%s drew %q in ASCII mode", name, r)
				break
			}
		}
	}
}

// TestTapeRenderPathsHaveNoHardcodedColours is the grep the audit asked for: the
// two files carry no colour of their own any more, so a theme change reaches
// them like it reaches every other surface.
func TestTapeRenderPathsHaveNoHardcodedColours(t *testing.T) {
	hexOrANSI := regexp.MustCompile(`lipgloss\.Color\(`)
	for _, path := range []string{"tapemanager.go", "tape_review.go"} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if loc := hexOrANSI.FindIndex(src); loc != nil {
			t.Errorf("%s names a colour of its own at byte %d; colour comes from the theme", path, loc[0])
		}
	}
}

// cellBackgrounds returns the background colour in force at each drawn cell of
// a styled line. A cell with no background reads as nil, which is what a fill
// that stops short of the row's end looks like.
func cellBackgrounds(line string) []color.RGBA {
	var (
		out     []color.RGBA
		cur     color.RGBA
		pattern = regexp.MustCompile(`\x1b\[([0-9;]*)m`)
		idx     int
	)
	for _, loc := range pattern.FindAllStringSubmatchIndex(line, -1) {
		for _, r := range line[idx:loc[0]] {
			_ = r
			out = append(out, cur)
		}
		params := strings.Split(line[loc[2]:loc[3]], ";")
		for i, p := range params {
			switch p {
			case "0":
				cur = color.RGBA{}
			case "48":
				if len(params) >= i+5 && params[i+1] == "2" {
					v := [3]uint8{}
					ok := true
					for k := range v {
						n, err := strconv.Atoi(params[i+2+k])
						if err != nil {
							ok = false
							break
						}
						v[k] = uint8(n)
					}
					if ok {
						cur = color.RGBA{R: v[0], G: v[1], B: v[2], A: 0xFF}
					}
				}
			}
		}
		idx = loc[1]
	}
	for range line[idx:] {
		out = append(out, cur)
	}
	return out
}

// rgbaOf converts a palette colour to the form cellBackgrounds reports.
func rgbaOf(c color.Color) color.RGBA {
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xFF}
}
