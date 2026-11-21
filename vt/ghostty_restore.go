//go:build ghostty

package vt

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	gh "go.mitchellh.com/libghostty"
)

type ghosttyRestore struct {
	scrollback         []uv.Line
	grids              [2]map[[2]int]*uv.Cell
	modes              map[int]bool
	charsets           [4]byte
	gl, gr             int
	hasCharsets        bool
	scrollRegion       uv.Rectangle
	hasScrollRegion    bool
	altScreen          bool
	hasAltScreen       bool
	cursorX, cursorY   int
	hasCursor          bool
	pendingWrap        bool
	pen                uv.Style
	penLink            uv.Link
	hasPen             bool
	kittyKbdStack      []int
	kittyKbdMainStack  []int
	modifyOtherKeys    int
	hasModifyOtherKeys bool
	screenWraps        []bool
	historyWraps       []bool
	penProtected       bool
	hasPenProtected    bool
	prot               [2][]CellRun
	lastPrinted        string
	hasLastPrinted     bool
	saved              [2]*SavedCursor
	tabStops           []int
	hasTabStops        bool
	titles             *Titles
	ansiModes          map[int]bool
}

func (t *GhosttyTerminal) pendingRestore() *ghosttyRestore {
	if t.restore == nil {
		t.restore = &ghosttyRestore{}
		t.restorePending.Store(true)
	}
	return t.restore
}

func (r *ghosttyRestore) setActiveCell(activeNow, x, y int, c *uv.Cell) {
	idx := activeNow
	if r.hasAltScreen {
		idx = 0
		if r.altScreen {
			idx = 1
		}
	}
	r.setGridCell(idx, x, y, c)
}

func (r *ghosttyRestore) setGridCell(idx, x, y int, c *uv.Cell) {
	if r.grids[idx] == nil {
		r.grids[idx] = make(map[[2]int]*uv.Cell)
	}
	var copied *uv.Cell
	if c != nil {
		cc := *c
		copied = &cc
	}
	r.grids[idx][[2]int{x, y}] = copied
}

func (t *GhosttyTerminal) flushRestoreLocked() {
	r := t.restore
	if r == nil {
		return
	}
	t.restore = nil
	t.restorePending.Store(false)
	if t.closed.Load() {
		return
	}

	var seq bytes.Buffer

	extend := t.scrollbackLenLocked() > 0
	if extend {
		if t.activeAltLiveLocked() {
			seq.WriteString("\x1b[?1049l\x1b[?1047l")
		}
		seq.WriteString("\x1b[?40l\x1b[?69l\x1b[r\x1b[?6l\x1b[4l\x1b[20l\x1b(B\x1b)B\x1b*B\x1b+B\x0f\x1b[0m\x1b[0\"q\x1b]8;;\x1b\\\x1b[2J\x1b[H")
	} else {
		seq.WriteString("\x1bc")
	}

	if r.hasTabStops {
		seq.WriteString("\x1b[3g")
		for _, x := range r.tabStops {
			if x >= 0 && x < t.width {
				fmt.Fprintf(&seq, "\x1b[1;%dH\x1bH", x+1)
			}
		}
		seq.WriteString("\x1b[H")
	}
	if r.titles != nil {
		seq.WriteString("\x1b]2;" + r.titles.Title + "\x1b\\")
	}

	if len(r.scrollback) > 0 {
		off := len(r.historyWraps) - len(r.scrollback)
		for i, line := range r.scrollback {
			if i < len(r.scrollback)-1 && off+i >= 0 && r.historyWraps[off+i] {
				appendStyledLine(&seq, padLine(line, t.width))
				seq.WriteString("\x1b[0m")
				continue
			}
			appendStyledLine(&seq, trimTrailingBlanks(line))
			seq.WriteString("\x1b[0m\r\n")
		}
		rows := min(len(r.scrollback), t.height-1)
		fmt.Fprintf(&seq, "\x1b[%d;1H", t.height)
		for range rows {
			seq.WriteByte('\n')
		}
		seq.WriteString("\x1b[2J\x1b[H")
	}

	altActive := r.hasAltScreen && r.altScreen
	var mainWraps []bool
	if !altActive {
		mainWraps = r.screenWraps
	}
	mainProt := protRows(r.prot[0], t.width, t.height)
	appendGridPaint(&seq, r.grids[0], mainProt, t.width, t.height, mainWraps)

	if altActive {
		t.term.VTWrite(seq.Bytes())
		seq.Reset()
		t.captureScreenLocked(0)
		if n, err := t.term.ScrollbackRows(); err == nil {
			t.mainSbLen = int(n)
		}
		altMode := 1049
		switch {
		case r.modes[1049]:
		case r.modes[1047]:
			altMode = 1047
		case r.modes[47]:
			altMode = 47
		}
		if sc := r.saved[0]; sc != nil {
			appendSavedCursor(&seq, *sc, r.grids[0], mainProt, 0, 0)
			if altMode != 1049 {
				seq.WriteString("\x1b7")
			}
		}
		fmt.Fprintf(&seq, "\x1b[?%dh\x1b[?6l\x1b[0m\x1b[0\"q\x1b[2J\x1b[H", altMode)
		appendGridPaint(&seq, r.grids[1], protRows(r.prot[1], t.width, t.height), t.width, t.height, r.screenWraps)
	}
	active := 0
	if altActive {
		active = 1
	}
	activeProt := protRows(r.prot[active], t.width, t.height)

	var charsets bytes.Buffer
	if r.hasCharsets {
		appendCharsets(&charsets, r.charsets, r.gl, r.gr)
	}
	seq.Write(charsets.Bytes())

	if len(r.kittyKbdStack) > 0 {
		top := r.kittyKbdStack[len(r.kittyKbdStack)-1]
		fmt.Fprintf(&seq, "\x1b[=%d;1u", top)
	}

	if r.hasModifyOtherKeys {
		fmt.Fprintf(&seq, "\x1b[>4;%dm", r.modifyOtherKeys)
	}

	decom := false
	if r.modes != nil {
		for _, m := range ghosttyModeNumbers {
			v, ok := r.modes[m.num]
			if !ok {
				continue
			}
			switch m.num {
			case 47, 1047, 1049:
				continue
			case 1048:
				continue
			case 6:
				decom = v
				continue
			}
			ch := byte('l')
			if v {
				ch = 'h'
			}
			fmt.Fprintf(&seq, "\x1b[?%d%c", m.num, ch)
		}
	}

	regionTop, regionLeft := 0, 0
	if r.hasScrollRegion {
		reg := r.scrollRegion.Intersect(uv.Rect(0, 0, t.width, t.height))
		if !reg.Empty() && (reg.Min.Y > 0 || reg.Max.Y < t.height) {
			fmt.Fprintf(&seq, "\x1b[%d;%dr", reg.Min.Y+1, reg.Max.Y)
			regionTop = reg.Min.Y
		}
		if lrmm := r.modes[69]; lrmm && !reg.Empty() && (reg.Min.X > 0 || reg.Max.X < t.width) {
			fmt.Fprintf(&seq, "\x1b[%d;%ds", reg.Min.X+1, reg.Max.X)
			regionLeft = reg.Min.X
		}
	}

	if sc := r.saved[active]; sc != nil {
		appendSavedCursor(&seq, *sc, r.grids[active], activeProt, regionTop, regionLeft)
		seq.WriteString("\x1b7\x1b[?6l\x1b[0m\x1b[0\"q")
		seq.Write(charsets.Bytes())
	}

	reprintAs := ""
	if r.hasLastPrinted && r.lastPrinted != "" {
		if cell := cursorCell(r.grids[active], r.cursorX, r.cursorY); r.hasCursor && r.pendingWrap && cell != nil {
			switch {
			case cell.Content == r.lastPrinted:
				reprintAs = cell.Content
			case mapThroughCharset(r.lastPrinted, r.charsets, r.gl) == cell.Content:
				reprintAs = r.lastPrinted
			}
		}
		if reprintAs == "" {
			cols := max(cellWidthOf(r.lastPrinted), 1)
			if y, ok := lastPrintedRow(r.grids[active], activeProt, r.screenWraps, t.width, t.height, cols); ok {
				appendLastPrinted(&seq, r.lastPrinted, y, cols)
				seq.Write(charsets.Bytes())
			}
		}
	}

	if decom {
		seq.WriteString("\x1b[?6h")
	}

	if r.hasCursor {
		y, x := r.cursorY, r.cursorX
		if decom {
			y -= regionTop
			x -= regionLeft
		}
		if y < 0 {
			y = 0
		}
		fmt.Fprintf(&seq, "\x1b[%d;%dH", y+1, x+1)
		if r.pendingWrap {
			if reprintAs != "" && reprintAs != cursorCell(r.grids[active], r.cursorX, r.cursorY).Content {
				lead, _ := cursorLead(r.grids[active], r.cursorX, r.cursorY)
				fmt.Fprintf(&seq, "\x1b[%d;%dH", y+1, lead+(x-r.cursorX)+1)
				seq.Write(charsets.Bytes())
				cell := *cursorCell(r.grids[active], r.cursorX, r.cursorY)
				cell.Content = reprintAs
				var p []bool
				if row := activeProt[r.cursorY]; row != nil {
					p = row[lead : lead+1]
				}
				appendStyledLineProt(&seq, uv.Line{cell}, p)
				seq.WriteString("\x1b[0m")
			} else {
				appendReprint(&seq, r.grids[active], activeProt, r.cursorX, r.cursorY, y, x-r.cursorX)
				seq.Write(charsets.Bytes())
			}
		}
	}

	for _, m := range ghosttyANSIModes {
		if v, ok := r.ansiModes[m.num]; ok {
			ch := byte('l')
			if v {
				ch = 'h'
			}
			fmt.Fprintf(&seq, "\x1b[%d%c", m.num, ch)
		}
	}

	if r.hasPen {
		seq.WriteString("\x1b[0m")
		seq.WriteString(penStyleSequence(&r.pen))
		if r.penLink.URL != "" {
			seq.WriteString("\x1b]8;" + r.penLink.Params + ";" + r.penLink.URL + "\x1b\\")
		}
	}
	if r.hasPenProtected {
		if r.penProtected {
			seq.WriteString("\x1b[1\"q")
		} else {
			seq.WriteString("\x1b[0\"q")
		}
	}

	t.charsetIDs = defaultCharsetIDs
	t.gl = 0
	if !extend {
		t.savedCur = [2]SavedCursor{{Charsets: defaultCharsetIDs}, {Charsets: defaultCharsetIDs}}
		t.penProtected = false
		t.scanner.lastPrint = 0
		t.gr = 0
	}
	for i, sc := range r.saved {
		if sc != nil {
			c := *sc
			for k, id := range c.Charsets {
				if id != 'A' && id != '0' {
					c.Charsets[k] = 'B'
				}
			}
			t.savedCur[i] = c
		}
	}
	if r.hasPenProtected {
		t.penProtected = r.penProtected
	}
	if r.hasLastPrinted {
		t.scanner.lastPrint = 0
		if ch, _ := utf8.DecodeRuneInString(r.lastPrinted); r.lastPrinted != "" && ch != utf8.RuneError {
			t.scanner.lastPrint = ch
		}
	}
	if r.hasCharsets {
		for i, id := range r.charsets {
			switch id {
			case 'A', '0':
				t.charsetIDs[i] = id
			default:
				t.charsetIDs[i] = 'B'
			}
		}
		if r.gl >= 0 && r.gl < 4 {
			t.gl = r.gl
		}
		if r.gr >= 0 && r.gr < 4 {
			t.gr = r.gr
		}
	}
	switch {
	case r.hasTabStops:
		t.tabstops = tabStopsAt(t.width, r.tabStops)
	case !extend:
		t.tabstops = uv.DefaultTabStops(t.width)
	}
	if r.titles != nil {
		t.iconName, t.explicitIcon = r.titles.Icon, r.titles.ExplicitIcon
		t.titleStack = nil
		for _, e := range r.titles.Stack {
			t.titleStack = append(t.titleStack, savedTitleOf(e))
		}
	} else if !extend {
		t.iconName, t.explicitIcon = "", ""
		t.titleStack = nil
	}
	t.scrollRegion = uv.Rect(0, 0, t.width, t.height)
	if r.hasScrollRegion {
		t.scrollRegion = r.scrollRegion.Intersect(uv.Rect(0, 0, t.width, t.height))
	}
	if r.hasModifyOtherKeys {
		t.modifyOtherKeys.Store(int32(r.modifyOtherKeys)) //nolint:gosec // bounded by RestoreModifyOtherKeys
	} else if !extend {
		t.modifyOtherKeys.Store(0)
	}
	if !t.kkpUnavailable.Load() {
		if len(r.kittyKbdStack) > 0 {
			t.kittyKbd.Reset()
			t.kittyKbd.SelectScreen(altActive)
			t.kittyKbd.SetStack(r.kittyKbdStack)
		} else if !extend {
			t.kittyKbd.Reset()
			t.kittyKbd.SelectScreen(altActive)
		}
		if len(r.kittyKbdMainStack) > 0 {
			t.kittyKbd.SetMainStack(r.kittyKbdMainStack)
		}
	}

	t.term.VTWrite(seq.Bytes())
	t.gridStale = true
	t.scrollGeneration++
	t.markAllDirtyLocked()
	t.modesDirty = true
	t.refreshCachesLocked()
}

func appendGridPaint(seq *bytes.Buffer, grid map[[2]int]*uv.Cell, prot map[int][]bool, width, height int, wraps []bool) {
	if len(grid) == 0 {
		return
	}
	carrying := false
	for y := 0; y < height; y++ {
		wrapped := y < len(wraps) && wraps[y] && y < height-1
		rowHas := wrapped || carrying
		for x := 0; x < width && !rowHas; x++ {
			if _, ok := grid[[2]int{x, y}]; ok {
				rowHas = true
			}
		}
		if !rowHas {
			continue
		}
		if !carrying {
			fmt.Fprintf(seq, "\x1b[%d;1H", y+1)
		}
		line := make(uv.Line, width)
		for x := 0; x < width; x++ {
			if c, ok := grid[[2]int{x, y}]; ok && c != nil {
				line[x] = *c
			} else {
				line[x] = uv.Cell{Content: " ", Width: 1}
			}
		}
		if !wrapped {
			line = trimTrailingBlanks(line)
		}
		var p []bool
		if row := prot[y]; row != nil {
			p = row[:len(line)]
		}
		appendStyledLineProt(seq, line, p)
		seq.WriteString("\x1b[0m")
		carrying = wrapped
	}
}

func padLine(line uv.Line, width int) uv.Line {
	if len(line) >= width {
		return line[:width]
	}
	out := make(uv.Line, width)
	copy(out, line)
	for x := len(line); x < width; x++ {
		out[x] = uv.Cell{Content: " ", Width: 1}
	}
	return out
}

func appendStyledLine(seq *bytes.Buffer, line uv.Line) {
	appendStyledLineProt(seq, line, nil)
}

func appendStyledLineProt(seq *bytes.Buffer, line uv.Line, prot []bool) {
	protecting := false
	setProt := func(x int) {
		on := x < len(prot) && prot[x]
		if on == protecting {
			return
		}
		protecting = on
		if on {
			seq.WriteString("\x1b[1\"q")
		} else {
			seq.WriteString("\x1b[0\"q")
		}
	}
	defer func() {
		if protecting {
			seq.WriteString("\x1b[0\"q")
		}
	}()
	var cur uv.Style
	curSet := false
	link := ""
	skipNext := false
	for x := 0; x < len(line); x++ {
		c := line[x]
		if skipNext {
			skipNext = false
			continue
		}
		if c.Width == 2 {
			skipNext = true
		}
		setProt(x)
		if c.Width == 0 {
			seq.WriteByte(' ')
			continue
		}
		if !curSet || !c.Style.Equal(&cur) {
			seq.WriteString("\x1b[0m")
			seq.WriteString(penStyleSequence(&c.Style))
			cur = c.Style
			curSet = true
		}
		if c.Link.URL != link {
			if c.Link.URL != "" {
				seq.WriteString("\x1b]8;;" + c.Link.URL + "\x1b\\")
			} else {
				seq.WriteString("\x1b]8;;\x1b\\")
			}
			link = c.Link.URL
		}
		if c.Content == "" {
			seq.WriteByte(' ')
		} else {
			seq.WriteString(c.Content)
		}
	}
	if link != "" {
		seq.WriteString("\x1b]8;;\x1b\\")
	}
}

func (t *GhosttyTerminal) captureScreenLocked(idx int) {
	if t.closed.Load() {
		return
	}
	_ = t.rs.SetDirty(gh.RenderStateDirtyFull)
	if err := t.rs.Update(t.term); err != nil {
		return
	}
	clear(t.styleCache)
	if err := t.rs.RowIterator(t.ri); err != nil {
		return
	}
	for {
		y, ok := t.ri.NextDirty()
		if !ok {
			break
		}
		if int(y) >= t.height {
			continue
		}
		t.syncRowLocked(t.bufAt(idx), int(y))
	}
	_ = t.rs.Clean()
}

func trimTrailingBlanks(line uv.Line) uv.Line {
	end := len(line)
	for end > 0 {
		c := line[end-1]
		if (c.Content == "" || c.Content == " ") && c.Style.IsZero() && c.Link.URL == "" {
			end--
			continue
		}
		break
	}
	return line[:end]
}

func cursorLead(grid map[[2]int]*uv.Cell, x, y int) (int, *uv.Cell) {
	cell := grid[[2]int{x, y}]
	if x > 0 && (cell == nil || cell.Width == 0) {
		if lead := grid[[2]int{x - 1, y}]; lead != nil && lead.Width == 2 {
			return x - 1, lead
		}
	}
	return x, cell
}

func cursorCell(grid map[[2]int]*uv.Cell, x, y int) *uv.Cell {
	_, cell := cursorLead(grid, x, y)
	if cell == nil || cell.Content == "" {
		return &uv.Cell{Content: " ", Width: 1}
	}
	return cell
}

func mapThroughCharset(ch string, ids [4]byte, gl int) string {
	if len(ch) != 1 || gl < 0 || gl > 3 {
		return ch
	}
	var set CharSet
	switch ids[gl] {
	case '0':
		set = charsetSpecialDrawing
	case 'A':
		set = charsetUK
	}
	if m, ok := set[ch[0]]; ok {
		return m
	}
	return ch
}

func cellWidthOf(ch string) int {
	_, w := ansi.FirstGraphemeCluster(ch, ansi.GraphemeWidth)
	return w
}
