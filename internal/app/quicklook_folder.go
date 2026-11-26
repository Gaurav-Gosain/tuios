package app

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

// A folder in quick look is a list you can walk. The rows are the rail's file
// rows: the same marks, icon colours and names. Inside, the keys move a
// selection, enter or l opens the selected row in place (a folder lists, a
// file shows its preview), and backspace or h goes to the folder above, with
// the selection on the row just left. r shows the folder in the rail.
//
// The walk keeps no history: up is the parent of what is shown, read again,
// so a folder that changed while you were in a file shows as it is now.
//
// The age is the rail's elapsed time format (agentElapsed). Size and age come from a stat of the rows on screen and the screen after
// them, never of the whole folder: one read per screen the selection reaches,
// one at a time, and none while the panel is too narrow for the columns.

const (
	// quickLookDirColsMin is the narrowest panel that shows size and age.
	quickLookDirColsMin = 60
	// quickLookDirSizeW and quickLookDirAgeW are the two columns' widths.
	quickLookDirSizeW = 9
	quickLookDirAgeW  = 4
	// quickLookStatMax bounds the names one stat read asks for.
	quickLookStatMax = 256
)

// quickLookStat is the size and the change time of one row of a folder.
type quickLookStat struct {
	size  int64
	mtime int64
	dir   bool
	// known says the stat answered. A name it did not describe is held as
	// asked, and its row shows no size and no age.
	known bool
}

// quickLookStatsMsg carries the stats of rows of a folder back to the loop.
type quickLookStatsMsg struct {
	res    *quickLookResult
	stats  map[string]quickLookStat
	failed bool
}

// quickLookOnFolder reports whether the panel shows a folder's rows.
func (ql *quickLookState) quickLookOnFolder() bool {
	r := ql.res
	return ql.open && r != nil && r.kind == "dir" && r.target == ql.target && !ql.quickLookOffMain()
}

// QuickLookOnFolder reports whether quick look shows a folder's rows, so
// enter, h and l walk it.
func (m *OS) QuickLookOnFolder() bool { return m.quickLook.quickLookOnFolder() }

// quickLookWalk shows t in place of what the panel shows, with the keyboard
// inside it. want is the row to select once a folder is read.
func (m *OS) quickLookWalk(t quickLookTarget, want string) tea.Cmd {
	ql := &m.quickLook
	m.quickLookStartWalk()
	cmd := m.quickLookShow(t)
	ql.inside = true
	ql.dirWant = want
	return cmd
}

// quickLookStartWalk notes the rail's row when a walk begins, so a move of the
// rail's cursor away from it ends the walk (QuickLookSync).
func (m *OS) quickLookStartWalk() {
	ql := &m.quickLook
	if !ql.walked {
		ql.walkFrom, _ = m.quickLookCursorTarget()
	}
	ql.walked = true
}

// QuickLookOpenEntry opens the selected row of a folder in place.
func (m *OS) QuickLookOpenEntry() tea.Cmd {
	ql := &m.quickLook
	if !ql.quickLookOnFolder() || ql.dirSel < 0 || ql.dirSel >= len(ql.res.folder.Entries) {
		return nil
	}
	e := ql.res.folder.Entries[ql.dirSel]
	t := ql.target
	t.path, t.name, t.dir = filepath.Join(t.path, e.Name), e.Name, e.Dir
	return m.quickLookWalk(t, "")
}

// QuickLookParent shows the folder above what the panel shows, with the
// selection on the row of the folder or file just left. It reports false at
// the top of the file system.
func (m *OS) QuickLookParent() tea.Cmd {
	ql := &m.quickLook
	t := ql.target
	if !ql.open || t.path == "" || t.up || t.refuse != "" {
		return nil
	}
	parent := filepath.Dir(t.path)
	if parent == t.path {
		return nil
	}
	left := filepath.Base(t.path)
	t.path, t.name, t.dir = parent, filepath.Base(parent), true
	return m.quickLookWalk(t, left)
}

// QuickLookRevealInRail shows the folder on the panel in the rail's files
// section, with the rail's cursor on the selected row. The preview goes back
// to following the rail's cursor, which is on that row now.
func (m *OS) QuickLookRevealInRail() tea.Cmd {
	ql := &m.quickLook
	t := ql.target
	if !ql.open || t.path == "" || t.up || t.refuse != "" {
		return nil
	}
	dir, name := filepath.Dir(t.path), t.name
	if ql.quickLookOnFolder() {
		dir, name = t.path, ""
		if ql.dirSel >= 0 && ql.dirSel < len(ql.res.folder.Entries) {
			name = ql.res.folder.Entries[ql.dirSel].Name
		}
	}
	if ql.free {
		// A path from a pane is on this machine. The rail shows it when it
		// is on screen and lists this machine.
		if m.filesView.Host != "" || m.AttachedHost != "" || !m.OpenFileView(dir) {
			m.ShowNotification("The rail cannot show this folder now.", "info", m.Settings.NotificationDuration)
			return nil
		}
		m.followFileRow(name)
		m.CloseQuickLook()
		return nil
	}
	if !m.filesOn() {
		return nil
	}
	cmd := m.requestFileList(dir, m.filesView.Origin, true)
	m.followFileRow(name)
	// The rail's cursor lands on the row when the listing is drawn, and the
	// preview follows the rail again from then (quickLookRejoin). Until then
	// it shows the row itself, and a key on the rail does not move it.
	m.quickLookStartWalk()
	ql.rejoin, ql.inside, ql.dirWant = true, false, ""
	if ql.quickLookOnFolder() && name != "" {
		e := ql.res.folder.Entries[ql.dirSel]
		t.path, t.name, t.dir = filepath.Join(dir, e.Name), e.Name, e.Dir
		return tea.Batch(cmd, m.quickLookShow(t))
	}
	return cmd
}

// quickLookRejoin lets the preview follow the rail's cursor again once the
// rail has put its cursor on the row r showed. sidebarPublishNav calls it
// when a walk of the rail's listing lands.
func (ql *quickLookState) quickLookRejoin() {
	if ql.rejoin {
		ql.rejoin, ql.walked = false, false
	}
}

// quickLookSelect moves the selection of a folder by n rows, held to the rows.
func (ql *quickLookState) quickLookSelect(n int) {
	last := len(ql.res.folder.Entries) - 1
	ql.dirSel = max(min(ql.dirSel+n, last), 0)
}

// quickLookDirPick selects the row named want of a folder just read, or the
// first row, and scrolls so the selection is in the middle when it is far down.
func (ql *quickLookState) quickLookDirPick(r *quickLookResult, rows int) {
	ql.dirSel = 0
	if ql.dirWant != "" {
		for i, e := range r.folder.Entries {
			if e.Name == ql.dirWant {
				ql.dirSel = i
				break
			}
		}
		ql.dirWant = ""
	}
	if ql.dirSel >= rows {
		ql.scroll = ql.dirSel - rows/2
	}
}

// drawQuickLookDir draws a folder's rows from the top of the body.
func (m *OS) drawQuickLookDir(c *ckCanvas, pal overlay.Palette, r *quickLookResult, top, rows, w int) {
	ql := &m.quickLook
	n := len(r.folder.Entries)
	ql.dirSel = max(min(ql.dirSel, n-1), 0)
	if ql.dirSel < ql.scroll {
		ql.scroll = ql.dirSel
	}
	if ql.dirSel >= ql.scroll+rows {
		ql.scroll = ql.dirSel - rows + 1
	}
	ql.scroll = max(min(ql.scroll, n-rows), 0)
	ql.dirTop, ql.dirRows = top, rows
	cols := w >= quickLookDirColsMin
	ql.dirCols = cols
	now := time.Now()
	for i := 0; i < rows && ql.scroll+i < n; i++ {
		at := ql.scroll + i
		e := r.folder.Entries[at]
		y := top + i
		right := w - 1
		if cols {
			right -= quickLookDirSizeW + quickLookDirAgeW + 2
		}
		// The rail's own row, so the two lists look the same. The selection
		// is the rail's row under the pointer.
		st := sidebarRowState{Cursor: ql.inside && at == ql.dirSel}
		if st.Cursor {
			c.fill(0, y, w-1, 1, sidebarRowBg(st, pal))
		}
		c.draw(0, y, right, m.sidebarFileRow(fileRowSpec{Kind: sidebarRowFileEntry, Name: e.Name, Dir: e.Dir, Icon: e.Icon}, right, pal, st))
		if st, ok := ql.dirStats[e.Name]; cols && ok && st.known {
			c.textR(w-1, y, ckSeg{t: agentElapsed("file", st.mtime*int64(time.Millisecond), now), fg: pal.FgMute})
			if !st.dir {
				c.textR(w-2-quickLookDirAgeW, y, ckSeg{t: units.Bytes(st.size), fg: pal.FgMute})
			}
		}
	}
}

// quickLookDirPlace is the folder's path in at most w cells, cut from the
// front: the last folders are the ones that say where you are.
func quickLookDirPlace(t quickLookTarget, w int) string {
	where := t.path
	if t.host == "" {
		where = shortenHome(where)
	}
	return truncPathLeft(where, max(w, 1))
}

// QuickLookFolderClick acts on a press on a folder's row at x, y on the
// screen: the first press selects the row and takes the keyboard inside, a
// press on the selected row opens it. It reports false off the rows.
func (m *OS) quickLookFolderClick(x, y int) (tea.Cmd, bool) {
	ql := &m.quickLook
	if !ql.quickLookOnFolder() {
		return nil, false
	}
	row := y - ql.box.Y0 - 1 - ql.dirTop
	if row < 0 || row >= ql.dirRows || x <= ql.box.X0 || x >= ql.box.X1-2 {
		return nil, false
	}
	at := ql.scroll + row
	if at >= len(ql.res.folder.Entries) {
		return nil, false
	}
	if ql.inside && at == ql.dirSel {
		return m.QuickLookOpenEntry(), true
	}
	ql.dirSel, ql.inside = at, true
	return nil, true
}

// quickLookDirStatsRead reads the size and age of the rows on screen and of
// the screen after them, when they are not held. Update asks after every
// message. Only one read is out at a time.
func (m *OS) quickLookDirStatsRead() tea.Cmd {
	ql := &m.quickLook
	if !ql.quickLookOnFolder() || ql.dirStatOut {
		return nil
	}
	r := ql.res
	if ql.dirStatsFor != r {
		ql.dirStats, ql.dirStatsFor, ql.dirStatFailed = nil, r, false
	}
	innerW, bodyH := m.quickLookSize()
	if ql.dirStatFailed || innerW < quickLookDirColsMin {
		return nil
	}
	from := max(ql.scroll, 0)
	to := min(from+2*bodyH, len(r.folder.Entries))
	var names []string
	for _, e := range r.folder.Entries[from:to] {
		if _, ok := ql.dirStats[e.Name]; !ok {
			names = append(names, e.Name)
		}
		if len(names) == quickLookStatMax {
			break
		}
	}
	if len(names) == 0 {
		return nil
	}
	ql.dirStatOut = true
	t := r.target
	var call func(host, verb string, params map[string]any) (json.RawMessage, error)
	if !m.quickLookHere(t) {
		call = m.verbOnHostCaller()
	}
	return func() tea.Msg {
		if call == nil {
			return quickLookStatsOf(r, names, session.StatNames(t.path, names, nil))
		}
		raw, err := call(t.host, "file-stat", map[string]any{"path": t.path, "names": names})
		var reply struct {
			Infos []session.FileInfo `json:"infos"`
		}
		if err != nil || jsonx.Unmarshal(raw, &reply) != nil || reply.Infos == nil {
			return quickLookStatsMsg{res: r, failed: true}
		}
		return quickLookStatsOf(r, names, reply.Infos)
	}
}

// quickLookStatsOf holds the stats of names from what the stat said. A name
// it did not describe is held as asked, with no size and no age.
func quickLookStatsOf(r *quickLookResult, names []string, infos []session.FileInfo) quickLookStatsMsg {
	out := make(map[string]quickLookStat, len(names))
	for _, name := range names {
		out[name] = quickLookStat{}
	}
	for _, in := range infos {
		out[in.Name] = quickLookStat{size: in.Size, mtime: in.MTime, dir: in.Kind == "dir" || in.LinkKind == "dir", known: true}
	}
	return quickLookStatsMsg{res: r, stats: out}
}

// applyQuickLookStats keeps the stats of a folder still on the panel.
func (m *OS) applyQuickLookStats(msg quickLookStatsMsg) {
	ql := &m.quickLook
	ql.dirStatOut = false
	if ql.dirStatsFor != msg.res {
		return
	}
	if msg.failed {
		ql.dirStatFailed = true
		return
	}
	if ql.dirStats == nil {
		ql.dirStats = make(map[string]quickLookStat, len(msg.stats))
	}
	maps.Copy(ql.dirStats, msg.stats)
	ql.dirStatsGen++
}
