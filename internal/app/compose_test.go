package app

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	uv "github.com/charmbracelet/ultraviolet"
)

// composeReference is the frame the lipgloss Compositor produces for the same
// layers: the path composeLayers replaced, kept here as the oracle it has to
// match byte for byte.
func composeReference(w, h int, layers []*lipgloss.Layer) string {
	canvas := lipgloss.NewCanvas(w, h)
	canvas.Compose(lipgloss.NewCompositor(layers...))
	return canvas.Render()
}

// composeUnderTest is the same frame through composeLayers, drawn twice on one
// OS so the second pass runs from the cached cells rather than the parse.
func composeUnderTest(t *testing.T, w, h int, layers []*lipgloss.Layer) (first, cached string) {
	t.Helper()
	m := &OS{Settings: config.Global}
	for range 2 {
		canvas := &frameCanvas{}
		canvas.Resize(w, h)
		canvas.Clear()
		m.composeLayers(canvas, layers)
		if first == "" {
			first = canvas.Render()
		} else {
			cached = canvas.Render()
		}
	}
	return first, cached
}

// composeCases are the layer sets that exercise what the compositor has to get
// right: wide glyphs on a layer's last column and on the canvas edge, marks that
// combine across an escape, hyperlinks, layers that overlap, share a z, or lie
// partly or wholly off the canvas, ragged lines, and an empty layer.
func composeCases() map[string][]*lipgloss.Layer {
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	bg := lipgloss.NewStyle().Background(lipgloss.Color("4"))
	return map[string][]*lipgloss.Layer{
		"plain": {
			lipgloss.NewLayer("hello\nworld").X(1).Y(1).Z(1).ID("a"),
		},
		"overlap-order": {
			lipgloss.NewLayer(red.Render("AAAAAA\nAAAAAA\nAAAAAA")).X(2).Y(1).Z(3).ID("a"),
			lipgloss.NewLayer(bg.Render("BBB\nBBB")).X(4).Y(2).Z(3).ID("b"),
			lipgloss.NewLayer("CCCCCCCC").X(0).Y(2).Z(1),
		},
		"wide-on-layer-edge": {
			lipgloss.NewLayer("界界界\n界界界").X(0).Y(0).Z(1).ID("w"),
			lipgloss.NewLayer("x\ny").X(6).Y(0).Z(2).ID("x"),
			lipgloss.NewLayer("界").X(5).Y(1).Z(3).ID("edge"),
		},
		"wide-on-canvas-edge": {
			lipgloss.NewLayer("abcdefghi界").X(0).Y(0).Z(1).ID("w"),
			lipgloss.NewLayer("界").X(9).Y(1).Z(2).ID("e"),
		},
		"wide-under-layer-corner": {
			lipgloss.NewLayer("界界界界界").X(0).Y(0).Z(1).ID("under"),
			lipgloss.NewLayer("Z\nZ").X(3).Y(0).Z(2).ID("over"),
		},
		"combining-across-escape": {
			lipgloss.NewLayer("a\x1b[31ḿb\x1b[m c").X(1).Y(1).Z(1).ID("c"),
		},
		"hyperlink": {
			lipgloss.NewLayer("\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\ tail").X(0).Y(0).Z(1).ID("l"),
		},
		"off-canvas": {
			lipgloss.NewLayer("gone").X(-10).Y(-10).Z(1).ID("gone"),
			lipgloss.NewLayer("half\nhalf").X(8).Y(3).Z(1).ID("half"),
			lipgloss.NewLayer("neg\nneg\nneg").X(-1).Y(-1).Z(2).ID("neg"),
		},
		"ragged-and-empty": {
			lipgloss.NewLayer("a\n\nabc\nab").X(2).Y(0).Z(2).ID("r"),
			lipgloss.NewLayer("").X(3).Y(3).Z(5).ID("empty"),
			lipgloss.NewLayer("filler filler\nfiller filler\nfiller filler\nfiller filler").X(0).Y(0).Z(1),
		},
		"wide-under-left-edge": {
			lipgloss.NewLayer("界界界界界").X(0).Y(0).Z(1).ID("under"),
			lipgloss.NewLayer("ab\nab").X(1).Y(0).Z(2).ID("over"),
		},
		"wide-under-right-edge": {
			lipgloss.NewLayer("界界界界界").X(0).Y(0).Z(1).ID("under"),
			lipgloss.NewLayer("ab\nab").X(2).Y(0).Z(2).ID("over"),
		},
		"wide-under-both-edges": {
			lipgloss.NewLayer(" 界界界界").X(0).Y(0).Z(1),
			lipgloss.NewLayer("abcd").X(2).Y(0).Z(2).ID("over"),
		},
		"layer-past-right-edge": {
			lipgloss.NewLayer("0123456789abc\n0123456789abc").X(2).Y(1).Z(1).ID("past"),
		},
		"layer-past-left-edge": {
			lipgloss.NewLayer("0123456789abc\n0123456789abc").X(-4).Y(1).Z(1).ID("past"),
		},
		"spill-onto-wide": {
			lipgloss.NewLayer("界界界界界").X(0).Y(0).Z(1).ID("under"),
			lipgloss.NewLayer("a界").X(2).Y(0).Z(2).ID("spill"),
		},
		"shared-id": {
			lipgloss.NewLayer("AAAA\nAAAA").X(0).Y(0).Z(2).ID("same"),
			lipgloss.NewLayer("BB").X(1).Y(1).Z(3).ID("same"),
			lipgloss.NewLayer("CCCCCC").X(2).Y(2).Z(1).ID("same"),
		},
		"crlf": {
			lipgloss.NewLayer("one\r\ntwo\r\n界").X(1).Y(1).Z(1).ID("crlf"),
		},
		"no-ids-mixed": {
			lipgloss.NewLayer(red.Render("styled\nrows")).X(0).Y(0).Z(2),
			lipgloss.NewLayer("plain").X(1).Y(1).Z(1),
		},
	}
}

func TestComposeLayersMatchesCompositor(t *testing.T) {
	const w, h = 10, 4
	for name, layers := range composeCases() {
		t.Run(name, func(t *testing.T) {
			want := composeReference(w, h, layers)
			first, cached := composeUnderTest(t, w, h, layers)
			if first != want {
				t.Fatalf("first compose differs from the compositor\nwant %q\ngot  %q", want, first)
			}
			if cached != want {
				t.Fatalf("cached compose differs from the compositor\nwant %q\ngot  %q", want, cached)
			}
		})
	}
}

// TestComposeLayersManyEqualZ pins the order of layers that share a z once
// there are more of them than the sort's insertion-sort threshold. The
// Compositor's sort is not stable, and the frame depends on the order it
// happens to settle on, so the replacement must sort the same list the same
// way.
func TestComposeLayersManyEqualZ(t *testing.T) {
	const w, h = 40, 6
	rng := rand.New(rand.NewSource(7))
	for round := range 20 {
		var layers []*lipgloss.Layer
		n := 13 + rng.Intn(20)
		for i := range n {
			body := strings.Repeat(string(rune('A'+i%26)), 3+rng.Intn(6))
			if rng.Intn(3) == 0 {
				body += "\n" + body
			}
			l := lipgloss.NewLayer(body).X(rng.Intn(w)).Y(rng.Intn(h)).Z(rng.Intn(3))
			if rng.Intn(2) == 0 {
				l = l.ID(fmt.Sprintf("l%d", i))
			}
			layers = append(layers, l)
		}
		want := composeReference(w, h, layers)
		first, cached := composeUnderTest(t, w, h, layers)
		if first != want || cached != want {
			t.Fatalf("round %d: equal-z order differs from the compositor", round)
		}
	}
}

// TestComposeLayersRandomMatchesCompositor draws random stacks of layers
// through both paths. Wide glyphs, styles, links, negative offsets, layers
// past every edge, shared z values, ids and no ids: whatever the generator
// produces, the two frames must be the same bytes, and so must the second
// frame drawn from the cached cells.
func TestComposeLayersRandomMatchesCompositor(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	glyphs := []string{"a", "b", " ", "界", "é", "█", "x\u0301", "👍"}
	styles := []lipgloss.Style{
		lipgloss.NewStyle(),
		lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		lipgloss.NewStyle().Background(lipgloss.Color("#101010")).Bold(true),
		lipgloss.NewStyle().Underline(true),
	}
	for round := range 300 {
		w, h := 4+rng.Intn(20), 1+rng.Intn(6)
		var layers []*lipgloss.Layer
		for i := range 1 + rng.Intn(16) {
			var sb strings.Builder
			rows := 1 + rng.Intn(3)
			for r := range rows {
				if r > 0 {
					sb.WriteByte('\n')
				}
				for range rng.Intn(8) {
					sb.WriteString(styles[rng.Intn(len(styles))].Render(glyphs[rng.Intn(len(glyphs))]))
				}
				if rng.Intn(5) == 0 {
					sb.WriteString("\x1b]8;;https://a.test\x1b\\L\x1b]8;;\x1b\\")
				}
			}
			l := lipgloss.NewLayer(sb.String()).X(rng.Intn(w+6) - 3).Y(rng.Intn(h+2) - 1).Z(rng.Intn(3))
			if rng.Intn(2) == 0 {
				l = l.ID(fmt.Sprintf("r%d", i))
			}
			layers = append(layers, l)
		}
		want := composeReference(w, h, layers)
		first, cached := composeUnderTest(t, w, h, layers)
		if first != want {
			t.Fatalf("round %d (%dx%d): first compose differs\nwant %q\ngot  %q", round, w, h, want, first)
		}
		if cached != want {
			t.Fatalf("round %d (%dx%d): cached compose differs\nwant %q\ngot  %q", round, w, h, want, cached)
		}
	}
}

// TestComposeLayersReparsesChangedContent is the cache's own guard: a layer
// that keeps its id and changes its string must draw the new string, not the
// cells parsed for the old one.
func TestComposeLayersReparsesChangedContent(t *testing.T) {
	const w, h = 10, 2
	m := &OS{Settings: config.Global}
	draw := func(content string) string {
		canvas := &frameCanvas{}
		canvas.Resize(w, h)
		canvas.Clear()
		m.composeLayers(canvas, []*lipgloss.Layer{lipgloss.NewLayer(content).X(0).Y(0).Z(1).ID("same")})
		return canvas.Render()
	}
	if got := draw("first"); !strings.Contains(got, "first") {
		t.Fatalf("first draw: %q", got)
	}
	if got := draw("second"); !strings.Contains(got, "second") || strings.Contains(got, "first") {
		t.Fatalf("a changed string under the same id drew the old cells: %q", got)
	}
	if got := draw("界"); !strings.Contains(got, "界") || strings.Contains(got, "second") {
		t.Fatalf("a narrower string under the same id kept the old width: %q", got)
	}
}

// TestComposeLayersSpillRowGoesCellByCell checks the guard for a row whose
// last head cell is wider than the columns left to it. No string the parser
// and the width measure agree on produces one, so the layer is built by hand:
// the copied row must still be what a cell-by-cell draw gives, with the wide
// cell's second half landing past the layer's edge.
func TestComposeLayersSpillRowGoesCellByCell(t *testing.T) {
	const w, h = 8, 1
	build := func() *cellLayer {
		cl := &cellLayer{w: 2, h: 1}
		cl.buf.Resize(2+wideMargin, 1)
		cl.blank = clearLines(cl.buf.Lines, cl.blank)
		cl.buf.Lines[0].Set(0, &uv.Cell{Content: "a", Width: 1})
		cl.buf.Lines[0].Set(1, &uv.Cell{Content: "界", Width: 2})
		cl.spill = []bool{true}
		return cl
	}
	draw := func(cl *cellLayer) string {
		canvas := &frameCanvas{}
		canvas.Resize(w, h)
		canvas.Clear()
		uv.NewStyledString("01234567").Draw(canvas, canvas.Bounds())
		cl.blit(canvas, 2, 0)
		return canvas.Render()
	}
	// The reference is the same layer drawn the slow way, which is what a
	// StyledString.Draw of the cells would do: clear the two columns, then set
	// the heads, the wide one spilling onto column 4.
	ref := &frameCanvas{}
	ref.Resize(w, h)
	ref.Clear()
	uv.NewStyledString("01234567").Draw(ref, ref.Bounds())
	ref.Lines[0].Set(2, nil)
	ref.Lines[0].Set(3, nil)
	ref.Lines[0].Set(2, &uv.Cell{Content: "a", Width: 1})
	ref.Lines[0].Set(3, &uv.Cell{Content: "界", Width: 2})
	want := ref.Render()
	if got := draw(build()); got != want {
		t.Fatalf("spill row drawn by copy\nwant %q\ngot  %q", want, got)
	}
}

// TestComposeLayersDropsAbsentLayers: a layer's cells are held only while it
// is on screen.
func TestComposeLayersDropsAbsentLayers(t *testing.T) {
	m := &OS{Settings: config.Global}
	canvas := &frameCanvas{}
	canvas.Resize(4, 1)
	m.composeLayers(canvas, []*lipgloss.Layer{lipgloss.NewLayer("a").ID("a"), lipgloss.NewLayer("b").ID("b")})
	if len(m.layerCells) != 2 {
		t.Fatalf("cached %d layers, want 2", len(m.layerCells))
	}
	m.composeLayers(canvas, []*lipgloss.Layer{lipgloss.NewLayer("b").ID("b")})
	if _, ok := m.layerCells["a"]; ok || len(m.layerCells) != 1 {
		t.Fatalf("an absent layer's cells were kept: %v", m.layerCells)
	}
}
