package app

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"image/color"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The other views of a file in quick look.
//
// v steps through them. The first press makes one read, of the details: the machine
// with the file sends its permissions, what git says, the diff of a changed file and
// the words of a PDF page. Nothing runs until then. The file's own view stays in
// ql.res, so v comes back to it with no read.
//
// A CSV file is also read here: a table in place of text, with h and l for columns.
const (
	quickLookViewMain    = "main"
	quickLookViewText    = "text"
	quickLookViewWords   = "words"
	quickLookViewDiff    = "diff"
	quickLookViewDetails = "details"
	quickLookColGap      = 2
	quickLookColMax      = 40
)

// quickLookTable is a CSV file in rows. numeric says a column is right aligned.
type quickLookTable struct {
	rows    [][]string
	widths  []int
	numeric []bool
	cut     bool
}

// quickLookArchiveRow is one row of an archive's listing, indented as a tree.
type quickLookArchiveRow struct {
	depth int
	name  string
	size  int64
	dir   bool
}

// quickLookDetails is the answer to the first v.
type quickLookDetails struct {
	target                   quickLookTarget
	path, mode, owner, group string
	mtime                    time.Time
	git                      string
	diff, words              []string
	failed                   string
}

// quickLookDetailsMsg carries the details back to the loop.
type quickLookDetailsMsg struct{ details *quickLookDetails }

// quickLookIsCSV reports whether the name is a table, and which comma it has.
func quickLookIsCSV(name string) (rune, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".csv":
		return ',', true
	case ".tsv":
		return '\t', true
	}
	return 0, false
}

// quickLookParseTable reads a CSV text, or nil for a text that is not one.
func quickLookParseTable(text string, comma rune, truncated, ascii bool) *quickLookTable {
	r := csv.NewReader(strings.NewReader(text))
	r.Comma, r.LazyQuotes, r.FieldsPerRecord = comma, true, -1
	rows, err := r.ReadAll()
	if err != nil && len(rows) == 0 {
		return nil
	}
	if truncated && len(rows) > 1 {
		rows = rows[:len(rows)-1]
	}
	if len(rows) < 2 {
		return nil
	}
	t := &quickLookTable{rows: rows, cut: truncated}
	for ri, row := range rows {
		if len(row) > len(t.widths) {
			t.widths = append(t.widths, make([]int, len(row)-len(t.widths))...)
			for len(t.numeric) < len(t.widths) {
				t.numeric = append(t.numeric, true)
			}
		}
		for i, cell := range row {
			cell = ckCodeText(strings.ReplaceAll(cell, "\n", " "), ascii)
			row[i] = cell
			t.widths[i] = min(max(t.widths[i], ckWidth(ckSeg{t: cell})), quickLookColMax)
			if ri > 0 && cell != "" {
				if _, err := strconv.ParseFloat(cell, 64); err != nil {
					t.numeric[i] = false
				}
			}
		}
	}
	return t
}

// quickLookHexLines is bytes as a hex dump: 16 to a row, then the same as text.
func quickLookHexLines(b []byte) []string {
	lines := make([]string, 0, len(b)/16+1)
	for off := 0; off < len(b); off += 16 {
		row := b[off:min(off+16, len(b))]
		var s strings.Builder
		s.WriteString(hex.EncodeToString([]byte{byte(off >> 24), byte(off >> 16), byte(off >> 8), byte(off)}))
		s.WriteString("  ")
		for i := range 16 {
			if i == 8 {
				s.WriteByte(' ')
			}
			if i < len(row) {
				s.WriteString(hex.EncodeToString(row[i : i+1]))
				s.WriteByte(' ')
			} else {
				s.WriteString("   ")
			}
		}
		s.WriteByte(' ')
		for _, c := range row {
			if c < 0x20 || c > 0x7e {
				c = '.'
			}
			s.WriteByte(c)
		}
		lines = append(lines, s.String())
	}
	return lines
}

// quickLookArchiveRows turns a listing into a tree. An archive need not name its
// folders, so a folder is added the first time something is in it.
func quickLookArchiveRows(entries []session.PreviewArchiveEntry, ascii bool) (rows []quickLookArchiveRow, unpacked int64) {
	seen := map[string]bool{}
	slices.SortStableFunc(entries, func(a, b session.PreviewArchiveEntry) int { return strings.Compare(a.Name, b.Name) })
	for _, e := range entries {
		parts := strings.Split(strings.Trim(e.Name, "/"), "/")
		for i := range len(parts) - 1 {
			dir := strings.Join(parts[:i+1], "/")
			if !seen[dir] {
				seen[dir] = true
				rows = append(rows, quickLookArchiveRow{depth: i, name: ckCodeText(parts[i], ascii) + "/", dir: true})
			}
		}
		full := strings.Join(parts, "/")
		name := ckCodeText(parts[len(parts)-1], ascii)
		if e.Dir {
			if seen[full] {
				continue
			}
			seen[full] = true
			name += "/"
		}
		unpacked += e.Size
		rows = append(rows, quickLookArchiveRow{depth: len(parts) - 1, name: name, size: e.Size, dir: e.Dir})
	}
	return rows, unpacked
}

// quickLookViews is the views of the file now, in the order v steps through them.
func (m *OS) quickLookViews() []string {
	ql := &m.quickLook
	views := []string{quickLookViewMain}
	if ql.res != nil && ql.res.table != nil {
		views = append(views, quickLookViewText)
	}
	if d := ql.details; d != nil && d.target == ql.target {
		if d.words != nil {
			views = append(views, quickLookViewWords)
		}
		if d.diff != nil {
			views = append(views, quickLookViewDiff)
		}
		views = append(views, quickLookViewDetails)
	}
	return views
}

// quickLookViewName is a view's name on the row of tabs.
func quickLookViewName(r *quickLookResult, v string) string {
	switch v {
	case quickLookViewText:
		return "Text"
	case quickLookViewWords:
		return "Words"
	case quickLookViewDiff:
		return "Diff"
	case quickLookViewDetails:
		return "Details"
	}
	switch r.kind {
	case "pdf":
		return "Page"
	case "image":
		return "Picture"
	case "video":
		return "Frame"
	case "table":
		return "Table"
	case "archive":
		return "Files"
	case "hex":
		return "Hex"
	}
	return "Text"
}

// QuickLookNextView goes to the next view of the file. The first press reads them.
func (m *OS) QuickLookNextView() tea.Cmd {
	ql := &m.quickLook
	r := ql.res
	if !ql.open || r == nil || r.target != ql.target || r.kind == "dir" || r.kind == "error" || ql.target.dir {
		return nil
	}
	ql.prompt, ql.status = quickLookNoPrompt, ""
	if d := ql.details; (d == nil || d.target != ql.target) && r.table == nil {
		if ql.detailsOut {
			return nil
		}
		return m.quickLookReadDetails()
	}
	if d := ql.details; (d == nil || d.target != ql.target) && ql.view == quickLookViewText {
		return m.quickLookReadDetails()
	}
	views := m.quickLookViews()
	i := max(slices.Index(views, ql.view), 0)
	ql.view = views[(i+1)%len(views)]
	ql.viewScroll = 0
	return nil
}

// quickLookReadDetails reads the details off the loop.
func (m *OS) quickLookReadDetails() tea.Cmd {
	ql := &m.quickLook
	ql.detailsOut = true
	t := ql.target
	want := session.PreviewWant{Page: ql.pageWant, Details: true, PagesKnown: true, DurationKnown: true}
	var call func(host, verb string, params map[string]any) (json.RawMessage, error)
	if t.host != "" {
		call = m.verbOnHostCaller()
	}
	ascii := overlay.UseASCII()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), quickLookTimeout)
		defer cancel()
		raw, err := quickLookWantReply(ctx, call, t, want)
		return quickLookDetailsMsg{details: quickLookDecodeDetails(t, raw, err, ascii)}
	}
}

// quickLookDecodeDetails reads the answer to the first v.
func quickLookDecodeDetails(t quickLookTarget, raw json.RawMessage, err error, ascii bool) *quickLookDetails {
	d := &quickLookDetails{target: t}
	var r struct {
		Details                  bool
		Path, Mode, Owner, Group string
		Mtime                    int64
		Git                      string
		Diff, Words              string
	}
	switch {
	case err != nil:
		d.failed = quickLookErrorText(t, err)
	case json.Unmarshal(raw, &r) != nil:
		d.failed = "The answer could not be read."
	case !r.Details:
		d.failed = "This machine runs an older tuios. It sends no details."
	}
	if d.failed != "" {
		return d
	}
	d.path, d.mode, d.owner, d.group, d.git = r.Path, r.Mode, r.Owner, r.Group, r.Git
	d.mtime = time.UnixMilli(r.Mtime)
	if strings.TrimSpace(r.Diff) != "" {
		d.diff, _, _ = quickLookLines("", r.Diff, ascii)
		if i := slices.IndexFunc(d.diff, func(l string) bool { return strings.HasPrefix(l, "@@") }); i > 0 {
			d.diff = d.diff[i:]
		}
	}
	if words := strings.Trim(r.Words, " \f\r\n"); words != "" {
		d.words, _, _ = quickLookLines("", words, ascii)
	}
	return d
}

// applyQuickLookDetails files the details away and goes to the first new view.
func (m *OS) applyQuickLookDetails(msg quickLookDetailsMsg) {
	ql := &m.quickLook
	ql.detailsOut = false
	d := msg.details
	if !ql.open || d.target != ql.target {
		return
	}
	if d.failed != "" {
		ql.status, ql.statusLine = d.failed, true
		return
	}
	ql.details = d
	views := m.quickLookViews()
	i := max(slices.Index(views, ql.view), 0)
	ql.view = views[min(i+1, len(views)-1)]
	ql.viewScroll = 0
}

// quickLookOffMain reports whether the panel shows a view other than the file's own.
func (ql *quickLookState) quickLookOffMain() bool {
	return ql.view != "" && ql.view != quickLookViewMain
}

// QuickLookColumn moves the table one column, or a film 1 second.
func (m *OS) QuickLookColumn(forward bool) {
	ql := &m.quickLook
	if !ql.open || ql.quickLookOffMain() {
		return
	}
	if r := ql.res; r != nil && r.table != nil {
		if forward {
			ql.column = min(ql.column+1, len(r.table.widths)-1)
		} else {
			ql.column = max(ql.column-1, 0)
		}
		return
	}
	if quickLookFilm(ql.res) {
		m.QuickLookLine(forward)
	}
}

// quickLookDrawTabs draws the row of views, and returns the first row below it.
func (m *OS) quickLookDrawTabs(c *ckCanvas, pal overlay.Palette, r *quickLookResult, y, w int) {
	ql := &m.quickLook
	x := 1
	for _, v := range m.quickLookViews() {
		name := " " + quickLookViewName(r, v) + " "
		seg := ckSeg{t: name, fg: pal.FgDim}
		if v == ql.view || (ql.view == "" && v == quickLookViewMain) {
			seg = ckSeg{t: name, fg: pal.Fg, bg: pal.Card, attrs: ckBold}
		}
		c.text(x, y, w-1, seg)
		x += ckWidth(seg) + 1
	}
}

// quickLookDrawView draws a view that is not the file's own.
func (m *OS) quickLookDrawView(c *ckCanvas, pal overlay.Palette, r *quickLookResult, top, rows, w int) {
	ql := &m.quickLook
	d := ql.details
	var lines []string
	switch ql.view {
	case quickLookViewText:
		lines = r.lines
	case quickLookViewWords:
		lines = d.words
	case quickLookViewDiff:
		lines = d.diff
	case quickLookViewDetails:
		quickLookDrawDetails(c, pal, d, top, w)
		return
	}
	ql.viewScroll = max(min(ql.viewScroll, len(lines)-rows), 0)
	for i := 0; i < rows && ql.viewScroll+i < len(lines); i++ {
		line := lines[ql.viewScroll+i]
		ink := pal.Fg
		var ground color.Color
		if ql.view == quickLookViewDiff {
			switch {
			case strings.HasPrefix(line, "@@"):
				ink = pal.FgMute
			case strings.HasPrefix(line, "+"):
				ink, ground = pal.Success, pal.Card
			case strings.HasPrefix(line, "-"):
				ink, ground = pal.Warn, pal.Card
			}
		}
		c.text(1, top+i, w-1, ckSeg{t: line, fg: ink, bg: ground})
	}
}

// quickLookDrawDetails draws the facts of a file.
func quickLookDrawDetails(c *ckCanvas, pal overlay.Palette, d *quickLookDetails, top, w int) {
	rows := [][2]string{
		{"Path", d.path},
		{"Permissions", strings.TrimSpace(d.mode + "  " + d.owner + "  " + d.group)},
		{"Modified", d.mtime.Format("2006-01-02 15:04")},
	}
	if d.git != "" {
		rows = append(rows, [2]string{"Git", d.git})
	}
	for i, row := range rows {
		c.text(1, top+i, w-1, ckSeg{t: row[0], fg: pal.FgMute})
		c.text(14, top+i, w-1, ckSeg{t: row[1], fg: pal.Fg})
	}
}

// quickLookDrawTable draws a table: the header, a rule, and the rows from the
// position, with the first column the keys chose on the left.
func quickLookDrawTable(c *ckCanvas, pal overlay.Palette, ql *quickLookState, t *quickLookTable, top, rows, w int) {
	cell := func(row []string, y int, attrs uint8) {
		x := 1
		for i := ql.column; i < len(t.widths) && x < w-1; i++ {
			s := ""
			if i < len(row) {
				s = row[i]
			}
			width := t.widths[i]
			at := x
			if t.numeric[i] {
				at += max(width-ckWidth(ckSeg{t: s}), 0)
			}
			c.text(at, y, min(x+width, w-1), ckSeg{t: s, fg: pal.Fg, attrs: attrs})
			x += width + quickLookColGap
		}
	}
	cell(t.rows[0], top, ckBold)
	rule := "─"
	if overlay.UseASCII() {
		rule = "-"
	}
	c.hline(1, top+1, max(w-2, 0), rule, pal.FgMute)
	body := t.rows[1:]
	rows = max(rows-2, 1)
	ql.scroll = max(min(ql.scroll, len(body)-rows), 0)
	for i := 0; i < rows && ql.scroll+i < len(body); i++ {
		cell(body[ql.scroll+i], top+2+i, 0)
	}
}

// quickLookColumnsShown says which columns fit, for the top of the panel.
func quickLookColumnsShown(t *quickLookTable, first, w int) string {
	x, last := 1, first
	for i := first; i < len(t.widths) && x+t.widths[i] < w-1; i++ {
		x += t.widths[i] + quickLookColGap
		last = i
	}
	if first == 0 && last == len(t.widths)-1 {
		return ""
	}
	return "Columns " + strconv.Itoa(first+1) + " to " + strconv.Itoa(last+1)
}

// quickLookDrawArchive draws an archive's listing.
func quickLookDrawArchive(c *ckCanvas, pal overlay.Palette, ql *quickLookState, r *quickLookResult, top, rows, w int) {
	ql.scroll = max(min(ql.scroll, len(r.archive)-rows), 0)
	for i := 0; i < rows && ql.scroll+i < len(r.archive); i++ {
		e := r.archive[ql.scroll+i]
		ink := pal.Fg
		if e.dir {
			ink = pal.AccentBright
		}
		right := w - 1
		if !e.dir {
			right = c.textR(w-1, top+i, ckSeg{t: quickLookSize(e.size), fg: pal.FgMute}) - 1
		}
		c.text(1+e.depth*2, top+i, right, ckSeg{t: e.name, fg: ink})
	}
}

// quickLookDrawHex draws a hex dump, with the offset in the quiet ink.
func quickLookDrawHex(c *ckCanvas, pal overlay.Palette, ql *quickLookState, r *quickLookResult, top, rows, w int) {
	ql.scroll = max(min(ql.scroll, len(r.lines)-rows), 0)
	for i := 0; i < rows && ql.scroll+i < len(r.lines); i++ {
		line := r.lines[ql.scroll+i]
		c.text(1, top+i, w-1, ckSeg{t: line[:8], fg: pal.FgMute}, ckSeg{t: line[8:], fg: pal.Fg})
	}
}

// quickLookViewMeta is the words at the top of a table, an archive or a hex dump.
func quickLookViewMeta(r *quickLookResult) []string {
	switch r.kind {
	case "table":
		t := r.table
		parts := []string{"CSV", plural.Count(len(t.rows)-1, "row"), plural.Count(len(t.widths), "column"), quickLookSize(r.size)}
		if t.cut {
			parts[1] = "The first " + parts[1]
		}
		return parts
	case "archive":
		parts := []string{"Archive", plural.Count(r.archiveItems, "item"), quickLookSize(r.size) + ", " + quickLookSize(r.unpacked) + " unpacked"}
		if r.capped {
			parts[1] = "More than " + parts[1]
		}
		return parts
	case "hex":
		parts := []string{r.mime, quickLookSize(r.size)}
		if int64(len(r.lines))*16 < r.size {
			parts = append(parts, "Showing the first "+quickLookSize(int64(len(r.lines))*16)+".")
		}
		return parts
	}
	return nil
}

// quickLookDecodeHead turns the start of a binary file into a hex dump.
func quickLookDecodeHead(res *quickLookResult, head string) {
	if b, err := base64.StdEncoding.DecodeString(head); err == nil && len(b) > 0 {
		res.kind, res.lines = "hex", quickLookHexLines(b)
	}
}
