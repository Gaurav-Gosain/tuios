package main

import (
	"fmt"
	"image/color"
	"os"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/mygo/ui/editor"

	"github.com/Gaurav-Gosain/tuios/internal/guios/preview"
	"github.com/Gaurav-Gosain/tuios/internal/memtrim"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// Tables, JSON trees, archives, hex dumps, fonts and the details of a file.
// Each is parsed off the UI thread, and each list builds only the rows in
// view, so a table of half a million rows scrolls as one of ten.

// --- tables ---

// lookTable is a CSV or TSV file's state.
type lookTable struct {
	data *preview.TableData
	err  string
	list ui.ListState
	cols []ui.TableColumn
}

func (a *App) startTable(l *fileLook) {
	if l.table != nil {
		return
	}
	t := &lookTable{}
	l.table = t
	ctx, p := l.ctx, l.path
	go func() {
		d, err := preview.ReadTable(ctx, p)
		a.update(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				t.err = lookErrText(err)
				return
			}
			t.data = d
			l.mark("parsed " + itoa(len(d.Rows)) + " rows")
			memtrim.Request()
			t.cols = append(t.cols, ui.TableColumn{Title: "#", ID: "#", Width: max(float32(len(itoa(len(d.Rows))))*8+16, 36), Align: ui.End, Fixed: true, RowHeader: true})
			for i := range d.Width {
				title := ""
				if i < len(d.Header) {
					title = d.Header[i]
				}
				align := ui.Start
				if d.Numeric[i] {
					align = ui.End
				}
				// The header is bolder than the cells: its runes take more.
				w := min(max(float32(d.Width[i])*7.4+24, float32(len([]rune(title)))*8.2+30, 56), 360)
				t.cols = append(t.cols, ui.TableColumn{Title: title, ID: itoa(i), Width: w, MinWidth: 40, Align: align})
			}
		})
	}()
}

func (a *App) tableView(c *ui.Context, pal *palette, l *fileLook) {
	t := l.table
	switch {
	case t == nil || t.data == nil && t.err == "":
		lookWaiting(c, pal, l)
		return
	case t.err != "":
		lookFailed(c, pal, t.err)
		return
	}
	d := t.data
	ui.Column(c.Key("table")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Table(c.Key("rows"), &t.list, t.cols, len(d.Rows), func(row, col int) {
			if col == 0 {
				ui.Text(c, itoa(row+1)).FontSize(fsSmall).FontFeatures("tnum").TextColor(pal.textFaint).SingleLine()
				return
			}
			r := d.Rows[row]
			cell := ""
			if col-1 < len(r) {
				cell = r[col-1]
			}
			tx := ui.Text(c, oneLineText(cell)).FontSize(fsBody).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0)
			if d.Numeric[col-1] {
				tx.FontFeatures("tnum")
			}
		}).Grow(1).MinHeight(0)
		if d.Capped {
			ui.Row(c).Height(28).Shrink(0).Padding(0, 14).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.LookTableCapped(groupDigits(len(d.Rows)))).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine()
			})
		}
	})
}

// oneLineText joins a cell's lines with spaces.
func oneLineText(s string) string {
	if strings.ContainsAny(s, "\n\r\t") {
		return strings.Join(strings.Fields(s), " ")
	}
	return s
}

// --- JSON ---

// lookJSON is a JSON file's tree.
type lookJSON struct {
	tree  *preview.JSONTree
	err   string
	state ui.OutlineState[int32]
	kids  map[int32][]int32
}

func (a *App) startJSON(l *fileLook) {
	if l.tree != nil {
		return
	}
	j := &lookJSON{kids: map[int32][]int32{}}
	l.tree = j
	ctx, p := l.ctx, l.path
	go func() {
		t, err := preview.ReadJSON(ctx, p)
		a.update(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				j.err = lookErrText(err)
				return
			}
			j.tree = t
			l.mark("parsed " + itoa(len(t.Nodes)) + " nodes")
			// The root and its children open, as a reader looks first.
			j.state.Open.Add(0)
			for _, k := range j.children(0) {
				if len(j.children(k)) <= 50 {
					j.state.Open.Add(k)
				}
			}
		})
	}()
}

// children are a node's children, kept once asked.
func (j *lookJSON) children(i int32) []int32 {
	if k, ok := j.kids[i]; ok {
		return k
	}
	k := j.tree.Children(int(i))
	j.kids[i] = k
	return k
}

func (a *App) jsonView(c *ui.Context, pal *palette, l *fileLook) {
	j := l.tree
	switch {
	case j == nil || j.tree == nil && j.err == "":
		lookWaiting(c, pal, l)
		return
	case j.err != "":
		lookFailed(c, pal, j.err)
		return
	}
	mono := a.termFamily()
	ct := a.codeTheme(c, pal)
	tok := func(k editor.TokenKind) ui.Color {
		if col := ct.Tokens[k].Color; col.A != 0 {
			return col
		}
		return pal.text
	}
	keyC, strC, numC, litC := tok(editor.TokenProperty), tok(editor.TokenString), tok(editor.TokenNumber), tok(editor.TokenConstant)
	ui.Column(c.Key("json")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Outline(c.Key("tree"), &j.state, []int32{0}, func(i int32) []int32 {
			return j.children(i)
		}, func(i int32) {
			n := &j.tree.Nodes[i]
			spans := make([]ui.Span, 0, 3)
			if n.Key != "" {
				spans = append(spans, ui.Span{Text: n.Key, Color: keyC, Font: mono, Size: 12.5}, ui.Span{Text: "  ", Size: 12.5})
			}
			switch n.Kind {
			case preview.JSONObject:
				spans = append(spans, ui.Span{Text: "{ " + locales.LookKeys(int(n.Count)) + " }", Color: pal.textFaint, Size: 12})
			case preview.JSONArray:
				spans = append(spans, ui.Span{Text: "[ " + locales.LookItems(int(n.Count)) + " ]", Color: pal.textFaint, Size: 12})
			case preview.JSONString:
				spans = append(spans, ui.Span{Text: `"` + oneLineText(n.Value) + `"`, Color: strC, Font: mono, Size: 12.5})
			case preview.JSONNumber:
				spans = append(spans, ui.Span{Text: n.Value, Color: numC, Font: mono, Size: 12.5})
			default:
				spans = append(spans, ui.Span{Text: n.Value, Color: litC, Font: mono, Size: 12.5})
			}
			ui.RichText(c, spans...).SingleLine().Ellipsis("…").MinWidth(0).Padding(3, 0)
		}).Grow(1).MinHeight(0).Padding(6, 10)
		if j.tree.Capped {
			ui.Row(c).Height(28).Shrink(0).Padding(0, 14).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.LookJSONCapped()).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine()
			})
		}
	})
}

// --- archives ---

// lookArchive is an archive's listing, as a tree of its folders.
type lookArchive struct {
	entries  []preview.ArchiveEntry
	capped   bool
	err      string
	state    ui.OutlineState[string]
	kids     map[string][]string
	sizes    map[string]int64
	dirs     map[string]bool
	unpacked int64
}

func (a *App) startArchive(l *fileLook) {
	if l.arc != nil {
		return
	}
	ar := &lookArchive{}
	l.arc = ar
	ctx, p := l.ctx, l.path
	go func() {
		entries, capped, err := preview.ListArchive(ctx, p)
		kids, sizes, dirs := map[string][]string{}, map[string]int64{}, map[string]bool{}
		var unpacked int64
		if err == nil {
			// An archive need not name its folders: a folder is added the
			// first time something is in it.
			seen := map[string]bool{"": true}
			var addDir func(d string)
			addDir = func(d string) {
				if seen[d] {
					return
				}
				seen[d] = true
				dirs[d] = true
				parent := path.Dir(d)
				if parent == "." {
					parent = ""
				}
				addDir(parent)
				kids[parent] = append(kids[parent], d)
			}
			for _, e := range entries {
				name := strings.TrimSuffix(strings.TrimPrefix(e.Name, "./"), "/")
				if name == "" || name == "." {
					continue
				}
				if e.Dir {
					addDir(name)
					continue
				}
				parent := path.Dir(name)
				if parent == "." {
					parent = ""
				}
				addDir(parent)
				if !seen[name] {
					seen[name] = true
					kids[parent] = append(kids[parent], name)
				}
				sizes[name] = e.Size
				unpacked += e.Size
			}
			for k, list := range kids {
				sort.Slice(list, func(x, y int) bool {
					if dirs[list[x]] != dirs[list[y]] {
						return dirs[list[x]]
					}
					return strings.ToLower(list[x]) < strings.ToLower(list[y])
				})
				kids[k] = list
			}
		}
		a.update(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				ar.err = lookErrText(err)
				if nt, ok := err.(preview.ErrNoTool); ok {
					ar.err = locales.LookNoBsdtar(string(nt))
				}
				return
			}
			l.mark("listed")
			ar.entries, ar.capped, ar.kids, ar.sizes, ar.dirs, ar.unpacked = entries, capped, kids, sizes, dirs, unpacked
			// One folder at the top, as most archives hold, opens.
			if top := kids[""]; len(top) == 1 && dirs[top[0]] {
				ar.state.Open.Add(top[0])
			}
		})
	}()
}

func (a *App) archiveView(c *ui.Context, pal *palette, l *fileLook) {
	ar := l.arc
	switch {
	case ar == nil || ar.entries == nil && ar.err == "":
		lookWaiting(c, pal, l)
		return
	case ar.err != "":
		lookFailed(c, pal, ar.err)
		return
	}
	ui.Column(c.Key("archive")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Height(30).Shrink(0).Padding(0, 16).AlignItems(ui.Center).Children(func() {
			s := locales.LookArchiveSummary(locales.LookEntries(len(ar.entries)), sizeText(ar.unpacked))
			if ar.capped {
				s += ". " + locales.LookArchiveCapped()
			}
			ui.Text(c, s).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…").MinWidth(0)
		})
		ui.Outline(c.Key("entries"), &ar.state, ar.kids[""], func(k string) []string {
			if !ar.dirs[k] {
				return nil
			}
			return ar.kids[k]
		}, func(k string) {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(3, 0).Children(func() {
				name := path.Base(k)
				if ar.dirs[k] {
					ui.Icon(c, folderIcon).Size(14, 14).TextColor(pal.textFaint).Shrink(0)
				} else {
					fileIconImage(c, pal, name, false, false)
				}
				ui.Text(c, name).FontSize(fsBody).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).Tooltip(k)
				if !ar.dirs[k] {
					ui.Text(c, sizeText(ar.sizes[k])).FontSize(fsSmall).FontFeatures("tnum").TextColor(pal.textFaint).SingleLine().Shrink(0)
				}
			})
		}).Grow(1).MinHeight(0).Padding(0, 10, 10, 10)
	})
}

// --- hex ---

// hexBlock is the bytes a block of the hex view reads at once.
const hexBlock = 64 << 10

// lookHex is a file's bytes as a hex dump: rows of 16, read a block at a
// time as they show, a few blocks kept.
type lookHex struct {
	path  string
	size  int64
	list  ui.ListState
	mu    sync.Mutex
	f     *os.File
	data  map[int64][]byte
	order []int64
	asked map[int64]bool
	shown [2]int
	// per is the bytes a row shows: 16, or 8 in a narrow viewer.
	per int
}

func (a *App) startHex(l *fileLook) {
	if l.hex != nil {
		return
	}
	f, err := os.Open(l.path)
	h := &lookHex{path: l.path, size: l.size, data: map[int64][]byte{}, asked: map[int64]bool{}}
	if err == nil {
		h.f = f
	}
	l.hex = h
}

func (h *lookHex) close() {
	if h.f != nil {
		_ = h.f.Close()
	}
}

func (h *lookHex) first() int { return h.shown[0] }
func (h *lookHex) rows() int  { return max(h.shown[1]-h.shown[0], 1) }

// hexRow is the bytes of row i, and whether they are read; a block not yet
// read is read on a goroutine.
func (a *App) hexRow(h *lookHex, i int) ([]byte, bool) {
	off := int64(i) * int64(h.per)
	blk := off / hexBlock * hexBlock
	h.mu.Lock()
	b, ok := h.data[blk]
	if !ok && !h.asked[blk] && h.f != nil {
		h.asked[blk] = true
		go func() {
			buf := make([]byte, hexBlock)
			n, _ := h.f.ReadAt(buf, blk)
			h.mu.Lock()
			h.data[blk] = buf[:n]
			delete(h.asked, blk)
			h.order = append(h.order, blk)
			if len(h.order) > 32 {
				delete(h.data, h.order[0])
				h.order = h.order[1:]
			}
			h.mu.Unlock()
			a.update(func() {})
		}()
	}
	h.mu.Unlock()
	if !ok {
		return nil, false
	}
	lo := off - blk
	if lo >= int64(len(b)) {
		return nil, true
	}
	return b[lo:min(lo+int64(h.per), int64(len(b)))], true
}

func (a *App) hexView(c *ui.Context, pal *palette, l *fileLook) {
	if l.hex == nil {
		a.startHex(l)
	}
	h := l.hex
	if h.f == nil {
		lookFailed(c, pal, locales.LookUnreadable())
		return
	}
	mono := a.termFamily()
	col := ui.Column(c.Key("hex")).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
	// Sixteen bytes a row where they fit, eight where they do not: a row
	// of 16 is 76 characters of the code font.
	if w := col.Bounds().W; w > 0 {
		per := 16
		if w < 76*7.6+40 {
			per = 8
		}
		if per != h.per && h.per != 0 {
			first, _ := h.list.Visible()
			h.list.ScrollTo(first*h.per/per, ui.Start)
		}
		h.per = per
	}
	if h.per == 0 {
		h.per = 16
	}
	per := h.per
	rows := int((h.size + int64(per) - 1) / int64(per))
	col.Children(func() {
		ui.List(c.Key("rows"), &h.list, rows, func(i int) {
			b, ok := a.hexRow(h, i)
			off := fmt.Sprintf("%08x", int64(i)*int64(per))
			hexs, text := "", ""
			if ok {
				var hb, tb strings.Builder
				for k := 0; k < per; k++ {
					if k == 8 {
						hb.WriteByte(' ')
					}
					if k < len(b) {
						fmt.Fprintf(&hb, "%02x ", b[k])
						if b[k] >= 0x20 && b[k] < 0x7f {
							tb.WriteByte(b[k])
						} else {
							tb.WriteString("·")
						}
					} else {
						hb.WriteString("   ")
					}
				}
				hexs, text = hb.String(), tb.String()
			}
			ui.RichText(c,
				ui.Span{Text: off + "  ", Color: pal.textFaint, Font: mono, Size: 12.5},
				ui.Span{Text: hexs + " ", Color: pal.text, Font: mono, Size: 12.5},
				ui.Span{Text: text, Color: pal.textMuted, Font: mono, Size: 12.5},
			).SingleLine().Padding(1, 0)
		}).Grow(1).MinHeight(0).Padding(8, 16)
		h.shown[0], h.shown[1] = h.list.Visible()
	})
}

// --- details ---

// detailsView is what the file is: its size, its dates, its permissions,
// and what was read of it.
func (a *App) detailsView(c *ui.Context, pal *palette, l *fileLook) {
	rows := [][2]string{
		{locales.LookFactName(), l.name},
		{locales.LookFactKind(), kindName(l.kind)},
		{locales.LookFactSize(), locales.LookSizeBytes(sizeText(l.size), groupDigits(int(l.size)))},
		{locales.LookFactModified(), l.mod.Format("2 Jan 2006, 15:04")},
		{locales.LookFactPermissions(), l.mode.Perm().String()},
		{locales.LookFactFolder(), path.Dir(strings.ReplaceAll(l.path, `\`, "/"))},
	}
	add := func(k, v string) { rows = append(rows, [2]string{k, v}) }
	switch {
	case l.img != nil && l.img.w > 0:
		add(locales.LookFactDimensions(), locales.LookPixels(itoa(l.img.w)+" × "+itoa(l.img.h)))
		if l.img.frames > 1 {
			add(locales.LookFactFrames(), itoa(l.img.frames))
		}
	case l.pdf != nil && l.pdf.doc != nil:
		add(locales.LookFactPages(), itoa(l.pdf.pages))
		w, h := l.pdf.doc.PageSize(0)
		add(locales.LookFactPageSize(), trimFloat(w/72*25.4)+" × "+trimFloat(h/72*25.4)+" mm")
		add(locales.LookFactDrawnBy(), l.pdf.doc.Engine())
	case l.film != nil && l.film.facts != nil:
		f := l.film.facts
		add(locales.LookFactLength(), filmClock(f.DurationMs))
		if f.W > 0 {
			add(locales.LookFactFrameSize(), itoa(f.W)+" × "+itoa(f.H))
		}
		add(locales.LookFactReadBy(), f.Engine)
	case l.table != nil && l.table.data != nil:
		add(locales.LookFactRows(), groupDigits(len(l.table.data.Rows)))
		add(locales.LookFactColumns(), itoa(len(l.table.data.Header)))
	case l.font != nil && l.font.ff != nil:
		fi := l.font.ff.Info
		add(locales.LookFactFamily(), fi.Family)
		add(locales.LookFactStyle(), fi.Style)
		add(locales.LookFactVersion(), fi.Version)
		add(locales.LookFactGlyphs(), groupDigits(fi.Glyphs))
		if fi.Faces > 1 {
			add(locales.LookFactFaces(), itoa(fi.Faces))
		}
	}
	ui.Scroll(c.Key("details")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(20, 20).Children(func() {
			factsTable(c, pal, rows, 520)
		})
	})
}

// --- fonts ---

// lookFont is a font file's specimen, drawn for the viewer's width.
type lookFont struct {
	ff      *preview.FontFile
	err     string
	bm      *ui.Bitmap
	bmW     int
	bmScale float32
	bmDark  bool
	drawing bool
	size    float32 // the specimen's zoom, 0 for 1
	scroll  ui.ScrollState
	viewH   float32
	widthIn float32
}

func (f *lookFont) scaleOr1() float32 {
	if f.size == 0 {
		return 1
	}
	return f.size
}

func (a *App) startFont(l *fileLook) {
	if l.font != nil {
		return
	}
	fo := &lookFont{}
	l.font = fo
	ctx, p := l.ctx, l.path
	go func() {
		ff, err := preview.OpenFont(p)
		a.update(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				fo.err = lookErrText(err)
				return
			}
			fo.ff = ff
			l.mark("read")
		})
	}()
}

func (a *App) fontView(c *ui.Context, pal *palette, l *fileLook) {
	fo := l.font
	switch {
	case fo == nil || fo.ff == nil && fo.err == "":
		lookWaiting(c, pal, l)
		return
	case fo.err != "":
		lookFailed(c, pal, fo.err)
		return
	}
	info := fo.ff.Info
	sc := ui.Scroll(c.Key("font")).TrackScroll(&fo.scroll).Grow(1).MinHeight(0)
	if b := sc.Bounds(); b.W > 0 && (b.H != fo.viewH || b.W-48 != fo.widthIn) {
		fo.viewH, fo.widthIn = b.H, b.W-48
		c.Invalidate()
	}
	sc.Children(func() {
		ui.Column(c).Padding(20, 24).Gap(14).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, info.Family).FontSize(15).FontWeight(wSemibold).TextColor(pal.text).SingleLine().Ellipsis("…")
				meta := []string{}
				if info.Style != "" {
					meta = append(meta, info.Style)
				}
				if info.Version != "" {
					meta = append(meta, locales.LookVersionOf(info.Version))
				}
				meta = append(meta, locales.LookGlyphsCount(groupDigits(info.Glyphs)))
				ui.Text(c, strings.Join(meta, " · ")).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…")
			})
			sp := ui.Box(c.Key("specimen")).AlignSelf(ui.Start)
			bm := fo.bm
			if bm != nil {
				w, h := bm.Size()
				sp.Width(float32(w) / fo.bmScale).Height(float32(h) / fo.bmScale)
			} else {
				sp.Height(200)
			}
			sp.Draw(func(p *ui.Painter, r ui.Rect) {
				if bm != nil {
					p.Image(bm, r, ui.FillBox)
				}
				a.fontWant(l, pal, int(fo.widthIn*p.Scale()), p.Scale())
			})
			if info.Designer != "" || info.License != "" {
				rows := [][2]string{}
				if info.Designer != "" {
					rows = append(rows, [2]string{locales.LookFactDesigner(), info.Designer})
				}
				if info.License != "" {
					rows = append(rows, [2]string{locales.LookFactLicense(), oneLineText(info.License)})
				}
				factsTable(c, pal, rows, 520)
			}
		})
	})
}

// fontWant draws the specimen again for a new width, scale or theme, on a
// goroutine.
func (a *App) fontWant(l *fileLook, pal *palette, widthPx int, scale float32) {
	fo := l.font
	if widthPx <= 0 || fo.drawing {
		return
	}
	if fo.bm != nil && fo.bmW == widthPx && fo.bmScale == scale && fo.bmDark == pal.dark {
		return
	}
	fo.drawing = true
	ff, k, dark := fo.ff, fo.scaleOr1(), pal.dark
	fg := color.NRGBA{R: pal.text.R, G: pal.text.G, B: pal.text.B, A: pal.text.A}
	a.defer_(func() {
		go func() {
			img, err := ff.Specimen(widthPx, float64(scale*k), fg)
			var bm *ui.Bitmap
			if err == nil {
				bm = ui.NewBitmap(img)
			}
			a.update(func() {
				fo.drawing = false
				if bm != nil {
					fo.bm, fo.bmW, fo.bmScale, fo.bmDark = bm, widthPx, scale, dark
				}
			})
		}()
	})
}
