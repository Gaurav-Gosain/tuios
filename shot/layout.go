package shot

type run struct {
	row, col, cells int
	cell            Cell
	text            string
	procedural      rune
	wide            bool
}

func textRuns(g *Grid) []run {
	var runs []run
	for y := 0; y < g.Rows; y++ {
		row := g.Cells[y]
		var cur *run
		flush := func() {
			if cur != nil {
				runs = append(runs, *cur)
				cur = nil
			}
		}
		for x := 0; x < g.Cols; x++ {
			c := row[x]
			if c.Width == 0 {
				continue
			}
			r := firstRune(c.Cluster)
			single := len(c.Cluster) == len(string(r))
			procedural := c.Width == 1 && single && isProcedural(r)
			multi := c.Cluster != "" && !single
			if procedural || c.Width == 2 || multi {
				flush()
				runs = append(runs, run{
					row: y, col: x, cells: int(c.Width), cell: c,
					text: c.Cluster, wide: c.Width == 2,
					procedural: pickProcedural(procedural, r),
				})
				continue
			}
			if cur != nil && cur.cell.SameStyle(c) {
				cur.cells++
				cur.text += displayCluster(c)
				continue
			}
			flush()
			cur = &run{row: y, col: x, cells: 1, cell: c, text: displayCluster(c)}
		}
		flush()
	}
	return runs
}

func pickProcedural(is bool, r rune) rune {
	if is {
		return r
	}
	return 0
}

func displayCluster(c Cell) string {
	if c.Cluster == "" {
		return " "
	}
	return c.Cluster
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func (r run) isBlank() bool {
	if r.procedural != 0 {
		return false
	}
	if r.cell.Underline != UnderlineNone || r.cell.Strike {
		return false
	}
	for _, ch := range r.text {
		if ch != ' ' {
			return false
		}
	}
	return true
}

type bgRun struct {
	row, col, cells int
	color           Color
}

func bgRuns(g *Grid) []bgRun {
	var runs []bgRun
	for y := 0; y < g.Rows; y++ {
		row := g.Cells[y]
		var cur *bgRun
		flush := func() {
			if cur != nil {
				runs = append(runs, *cur)
				cur = nil
			}
		}
		for x := 0; x < g.Cols; x++ {
			c := row[x]
			if c.Width == 0 {
				continue
			}
			span := int(c.Width)
			if c.BGDefault {
				flush()
				continue
			}
			if cur != nil && cur.color == c.BG {
				cur.cells += span
				continue
			}
			flush()
			cur = &bgRun{row: y, col: x, cells: span, color: c.BG}
		}
		flush()
	}
	return runs
}

// FrameMode says how much dressing goes around the grid.
type FrameMode int

const (
	// FrameNone renders the bare grid on its own background.
	FrameNone FrameMode = iota
	// FramePlain renders the padded card without a title bar.
	FramePlain
	// FrameWindow adds the title bar and window controls.
	FrameWindow
)

// ControlsStyle picks the window control marks in the title bar.
type ControlsStyle int

const (
	// ControlsMacOS draws the traffic lights, and is what "auto" resolves to.
	//
	// Accent-tinted dots at the traffic lights' size, spacing and position do
	// not read as a quieter variant of the arrangement everyone knows; they
	// read as that arrangement with the colours broken. Red, amber and green are what a window control
	// is, everywhere, so they are not the theme's to tint. See the rule in the
	// corrective spec's section 4: chrome that carries a learned meaning keeps
	// its learned colours.
	ControlsMacOS ControlsStyle = iota
	// ControlsGlyphs draws the user's own glyph-set window marks.
	ControlsGlyphs
	// ControlsNone leaves the bar empty of controls.
	ControlsNone
)

// Frame is the fully resolved dressing around a grid. Every value is
// concrete by the time it lands here: config parsing, theme derivation, and
// the auto keywords are the caller's job, so the renderer has no defaults of
// its own to disagree with the settings panel about.
type Frame struct {
	Mode    FrameMode
	Padding int // px around the card, at scale 1
	Radius  int // card corner radius, px at scale 1
	Shadow  bool

	// Transparent drops the wash entirely: PNG alpha, SVG nothing.
	Transparent bool
	// WashStart and WashEnd are the diagonal gradient stops. Equal values
	// give a solid fill.
	WashStart, WashEnd Color

	Controls ControlsStyle
	// Title is the window display name for the title bar.
	Title string
	// Accent tints the dots and the title, derived from the theme.
	Accent Color
	// CloseGlyph, MinimizeGlyph, MaximizeGlyph are the glyph-set marks for
	// ControlsGlyphs.
	CloseGlyph, MinimizeGlyph, MaximizeGlyph string

	// FontFamily is the CSS font stack for SVG and HTML.
	FontFamily string
	// Font is the font PNG rasterizes with, and the one SVG and HTML inline
	// as an @font-face when EmbedFont is set. No Data means reference-only
	// SVG and HTML and the embedded Go Mono for PNG.
	Font Font
	// BoldFont is the bold cut of Font. No Data double-strikes bold cells.
	BoldFont Font
	// Fallback lists the faces PNG looks in, in order, for a glyph Font does
	// not have. Go Mono comes after them. MissingGlyphs says which runes
	// still need a face.
	Fallback []Font
	// EmbedFont allows SVG and HTML to inline Font as an @font-face. Only
	// a font the user named themselves earns that: embedding turns a kilobyte
	// of SVG into megabytes, and a font tuios found by asking the terminal was
	// found for the raster, not for the export.
	EmbedFont bool
	// Scale multiplies the PNG raster size, 1 to 4.
	Scale int

	// CellAspect is one cell of the terminal being pictured, width over
	// height. Zero leaves the raster on the font's own cell, which is what a
	// render with no terminal to ask has to do.
	CellAspect float64
}

type layout struct {
	cw, ch                     float64
	pad                        float64
	inset                      float64
	titleH                     float64
	radius                     float64
	cardX, cardY, cardW, cardH float64
	gridX, gridY               float64
	w, h                       float64
}

func computeLayout(g *Grid, f *Frame, cw, ch, scale float64) layout {
	l := layout{cw: cw, ch: ch}
	if f == nil || f.Mode == FrameNone {
		l.cardW = float64(g.Cols) * cw
		l.cardH = float64(g.Rows) * ch
		l.w, l.h = l.cardW, l.cardH
		return l
	}
	l.pad = float64(f.Padding) * scale
	l.radius = float64(f.Radius) * scale
	l.inset = ch * 0.75
	if f.Mode == FrameWindow {
		l.titleH = ch * 1.7
	}
	l.cardX, l.cardY = l.pad, l.pad
	l.cardW = float64(g.Cols)*cw + 2*l.inset
	l.cardH = float64(g.Rows)*ch + 2*l.inset + l.titleH
	l.gridX = l.cardX + l.inset
	l.gridY = l.cardY + l.titleH + l.inset
	l.w = l.cardW + 2*l.pad
	l.h = l.cardH + 2*l.pad
	return l
}

func contrastRatio(a, b Color) float64 {
	la, lb := luma(a), luma(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func deriveWash(bg Color, accents []Color) (Color, Color) {
	a1, a2 := bg, bg
	if len(accents) > 0 {
		a1 = accents[0]
		a2 = accents[len(accents)-1]
	}
	if len(accents) > 1 {
		a2 = accents[1]
	}
	start := quietStop(mix(a1, bg, 0.45), bg)
	end := quietStop(mix(a2, bg, 0.6), bg)
	return start, end
}

func quietStop(c, bg Color) Color {
	for i := 0; i < 16 && contrastRatio(c, bg) > 2.2; i++ {
		c = mix(c, bg, 0.2)
	}
	return c
}
