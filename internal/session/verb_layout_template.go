package session

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/layouttmpl"
)

// layout-template: save the workspace a session shows as a layout, load one
// back, list them, delete one.
//
// The templates are files of this machine (internal/layouttmpl), so every
// client of a session reads one list, whatever machine it draws on. A load is
// a change to the session's state the daemon makes, as the other layout verbs
// are: the panes it needs are opened by the daemon, their names and the split
// tree are written in one state change, and every attached client lays the
// panes out from it. The TUI used to write the rectangles into its own model
// and push them, which a second client then had to take on trust.
//
// What a load does with the workspace's panes:
//
//   - the panes there now are reused in order, one per pane of the layout
//     that is not minimized; a reused pane keeps its folder and its program;
//   - a pane the layout has and the workspace has not is opened in the
//     layout's folder for it, with its name, and its command is typed into
//     its shell;
//   - a pane the workspace has and the layout has not is minimized;
//   - a bsp layout saved with its tree gets that tree back, so each pane is
//     in its own split; one saved without (an older file) is tiled by the
//     scheme; a floating layout gets its rectangles, scaled to the session.

// layoutTemplateActions are the things layout-template does.
var layoutTemplateActions = []string{"list", "save", "load", "delete"}

type layoutTemplateParams struct {
	Action  string `json:"action"`
	Session string `json:"session"`
	Name    string `json:"name"`
}

// layoutTemplateRow is one template as list answers it.
type layoutTemplateRow struct {
	Name      string `json:"name"`
	Panes     int    `json:"panes"`
	Mode      string `json:"mode,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	Version   int    `json:"version"`
}

func (d *Daemon) verbLayoutTemplate(cs *connState, params json.RawMessage) (any, *verbError) {
	var p layoutTemplateParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	name := strings.TrimSpace(p.Name)
	if p.Action != "list" {
		if name == "" {
			return nil, invalidParam("name", "name the layout")
		}
		if len(name) > 128 || strings.ContainsAny(name, "\x00\n\r") {
			return nil, invalidParam("name", "a layout name is one line of at most 128 bytes")
		}
	}
	switch p.Action {
	case "list":
		ts, err := layouttmpl.List()
		if err != nil {
			return nil, newVerbError(ErrVerbInternal, "read the layouts folder: "+err.Error())
		}
		rows := make([]layoutTemplateRow, 0, len(ts))
		for _, t := range ts {
			r := layoutTemplateRow{Name: t.Name, Panes: len(t.Windows), Mode: t.Mode, Version: t.Version}
			if !t.CreatedAt.IsZero() {
				r.CreatedAt = t.CreatedAt.UTC().Format(time.RFC3339)
			}
			rows = append(rows, r)
		}
		return map[string]any{"layouts": rows, "dir": layouttmpl.Dir()}, nil
	case "delete":
		if _, err := os.Stat(layouttmpl.Path(name)); err != nil {
			return nil, layoutTemplateMissing(name)
		}
		if err := layouttmpl.Delete(name); err != nil {
			return nil, newVerbError(ErrVerbInternal, "delete layout: "+err.Error())
		}
		return map[string]any{"name": name, "deleted": true}, nil
	case "save":
		sess, verr := d.resolveVerbSession(cs, p.Session)
		if verr != nil {
			return nil, verr
		}
		st := sess.GetState()
		if IsScratchWorkspace(st.CurrentWorkspace) {
			return nil, invalidParam("session", "the session shows a scratch group, which is not a workspace. Show a workspace, then save it")
		}
		w, h := sess.Size()
		t := templateFromState(st, name, w, h)
		if len(t.Windows) == 0 {
			return nil, invalidParam("session", "the workspace the session shows has no panes to save")
		}
		if err := layouttmpl.Save(t); err != nil {
			return nil, newVerbError(ErrVerbInternal, "save layout: "+err.Error())
		}
		return map[string]any{"name": name, "panes": len(t.Windows), "mode": t.Mode, "workspace": st.CurrentWorkspace, "path": layouttmpl.Path(name)}, nil
	case "load":
		sess, verr := d.resolveVerbSession(cs, p.Session)
		if verr != nil {
			return nil, verr
		}
		t, err := layouttmpl.Load(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, layoutTemplateMissing(name)
			}
			return nil, invalidParam("name", err.Error())
		}
		return d.loadLayoutTemplate(cs, sess, t)
	}
	return nil, invalidParam("action", "action is list, save, load or delete")
}

func layoutTemplateMissing(name string) *verbError {
	return hintedVerbError(ErrVerbNoFile, "no layout is called "+echoName(name), &VerbHint{
		Verb:   "layout-template",
		Detail: "List the layouts with action list.",
	})
}

// templateFromState is the layout of the workspace st shows, named name, at
// the session size w by h.
func templateFromState(st *SessionState, name string, w, h int) layouttmpl.Template {
	ws := st.CurrentWorkspace
	mode := st.LayoutMode
	if m, ok := st.WorkspaceLayoutModes[ws]; ok {
		mode = m
	}
	if !st.AutoTiling {
		mode = "floating"
	}
	if mode == "" {
		mode = "bsp"
	}
	t := layouttmpl.Template{
		Name: name, CreatedAt: time.Now(), Version: layouttmpl.Version,
		AutoTiling: st.AutoTiling, Mode: mode, MasterRatio: st.MasterRatio,
		TilingScheme: layout.AutoScheme(st.TilingScheme).String(),
		ScreenWidth:  w, ScreenHeight: h,
	}
	if r, ok := st.WorkspaceMasterRatio[ws]; ok && r > 0 {
		t.MasterRatio = r
	}
	index := map[int]int{} // BSP leaf number to the window's place in t.Windows, from 1
	for i := range st.Windows {
		win := &st.Windows[i]
		if win.Workspace != ws || win.Scratch || win.Rail != "" || win.Popup {
			continue
		}
		title := win.Title
		t.Windows = append(t.Windows, layouttmpl.Window{
			X: win.X, Y: win.Y, Width: win.Width, Height: win.Height,
			Title: title, CustomName: win.CustomName, WorkingDir: win.Cwd,
			Minimized: win.Minimized, Floating: win.IsFloating,
		})
		if n, ok := st.WindowToBSPID[win.ID]; ok {
			index[n] = len(t.Windows)
		}
	}
	if tree := st.WorkspaceTrees[ws]; mode == "bsp" && tree != nil && tree.Root != nil {
		t.Tree = templateNode(tree.Root, index)
	}
	return t
}

// templateNode is a tree node with its leaves numbered by window place, or
// nil for a leaf whose window the template does not hold.
func templateNode(n *SerializedBSPNode, index map[int]int) *layouttmpl.Node {
	if n == nil {
		return nil
	}
	if n.Left == nil && n.Right == nil {
		if i, ok := index[n.WindowID]; ok {
			return &layouttmpl.Node{Window: i}
		}
		return nil
	}
	l, r := templateNode(n.Left, index), templateNode(n.Right, index)
	switch {
	case l == nil:
		return r
	case r == nil:
		return l
	}
	return &layouttmpl.Node{Split: n.SplitType, Ratio: n.SplitRatio, Left: l, Right: r}
}

// loadLayoutTemplate applies t to the workspace sess shows.
func (d *Daemon) loadLayoutTemplate(cs *connState, sess *Session, t layouttmpl.Template) (any, *verbError) {
	st := sess.GetState()
	ws := st.CurrentWorkspace
	if IsScratchWorkspace(ws) {
		return nil, invalidParam("session", "the session shows a scratch group, which is not a workspace. Show a workspace, then load the layout there")
	}
	var existing []string
	for i := range st.Windows {
		w := &st.Windows[i]
		if w.Workspace == ws && !w.Minimized && !w.Scratch && w.Rail == "" && !w.Popup {
			existing = append(existing, w.ID)
		}
	}
	// The layout's panes that show, by their place in t.Windows (from 1).
	var slots []int
	for i, w := range t.Windows {
		if !w.Minimized {
			slots = append(slots, i+1)
		}
	}
	if len(slots) == 0 {
		return nil, invalidParam("name", "the layout has no pane that shows")
	}
	ids := map[int]string{} // place in t.Windows to the window that takes it
	var created []string
	focus := false
	for k, slot := range slots {
		tw := t.Windows[slot-1]
		if k < len(existing) {
			ids[slot] = existing[k]
			continue
		}
		res, verr := d.openWindow(cs, &newWindowParams{
			Session: sess.Name(), Name: templateName(tw), Workspace: ws,
			Cwd: templateDir(tw.WorkingDir), Focus: &focus,
		}, nil)
		if verr != nil {
			return nil, verr
		}
		id := newWindowIDOf(res)
		if id == "" {
			return nil, newVerbError(ErrVerbInternal, "the daemon opened a pane for the layout and gave no id")
		}
		ids[slot] = id
		created = append(created, id)
	}
	extra := existing[min(len(slots), len(existing)):]

	box := sess.layoutBox()
	mode := t.Mode
	if mode == "" {
		mode = "bsp"
		if !t.AutoTiling {
			mode = "floating"
		}
	}
	place := !d.hasTUIClient(sess)
	var focused string
	err := sess.arrangeLayout(place, func(state *SessionState, e *layoutEdit) (int, error) {
		for _, id := range extra {
			if i, err := findWindowStateIndex(state.Windows, id); err == nil {
				state.Windows[i].Minimized = true
			}
		}
		for slot, id := range ids {
			i, err := findWindowStateIndex(state.Windows, id)
			if err != nil {
				continue
			}
			w := &state.Windows[i]
			if name := templateName(t.Windows[slot-1]); name != "" {
				w.CustomName = name
			}
			w.Minimized = false
			w.IsFloating = t.Windows[slot-1].Floating && mode != "floating"
		}
		// The workspace's focus stays when it is on a pane of the layout, and
		// is otherwise the layout's first pane, so a client has a pane to type
		// into, as the TUI's own load gave it.
		keep := false
		for _, id := range ids {
			keep = keep || id == state.FocusedWindowID
		}
		if !keep {
			state.FocusedWindowID = ids[slots[0]]
		}
		focused = state.FocusedWindowID
		if t.TilingScheme != "" {
			state.TilingScheme = int(layout.ParseAutoScheme(t.TilingScheme))
		}
		if t.MasterRatio > 0 {
			state.MasterRatio = t.MasterRatio
			ratios := cloneIntFloatMap(state.WorkspaceMasterRatio)
			ratios[ws] = t.MasterRatio
			state.WorkspaceMasterRatio = ratios
		}
		modes := cloneIntStringMap(state.WorkspaceLayoutModes)
		modes[ws] = mode
		state.WorkspaceLayoutModes = modes
		if mode == "floating" {
			state.AutoTiling = false
			scaleTemplateRects(state, t, ids, box)
			return ws, nil
		}
		state.AutoTiling = true
		if mode == "bsp" && t.Tree != nil {
			if tree := templateTree(t.Tree, ids, e); tree != nil {
				tree.AutoScheme = layout.AutoScheme(state.TilingScheme)
				e.trees[ws] = tree
			}
		}
		// Panes the tree does not name go in by the scheme.
		e.tree(ws)
		return ws, nil
	})
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "load layout: "+err.Error())
	}
	// The command of each pane the load opened, typed into its shell as the
	// TUI's load did.
	st = sess.GetState()
	for slot, id := range ids {
		tw := t.Windows[slot-1]
		if tw.Command == "" || !slices.Contains(created, id) {
			continue
		}
		line := tw.Command
		if len(tw.Args) > 0 {
			line += " " + strings.Join(tw.Args, " ")
		}
		if w, ok := findWindowState(st, id); ok {
			if pty := sess.GetPTY(w.PTYID); pty != nil {
				_, _ = pty.Write([]byte(line + "\n"))
			}
		}
	}
	return map[string]any{
		"name": t.Name, "workspace": ws, "mode": mode, "focus": focused,
		"panes": len(slots), "opened": len(created), "reused": len(slots) - len(created), "minimized": len(extra),
	}, nil
}

// templateName is the name a layout gives its pane.
func templateName(w layouttmpl.Window) string {
	if w.CustomName != "" {
		return w.CustomName
	}
	return ""
}

// templateDir is the folder a layout may open a pane in, or "": a template is
// a file anyone can edit, so the path must be absolute, clean and a folder.
func templateDir(dir string) string {
	if dir == "" || !filepath.IsAbs(dir) || strings.ContainsAny(dir, "\x00\n\r") {
		return ""
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return ""
	}
	return filepath.Clean(dir)
}

// templateTree is t's tree with its leaves turned into the session's leaf
// numbers of the windows that took each place, or nil when it names none.
func templateTree(n *layouttmpl.Node, ids map[int]string, e *layoutEdit) *layout.BSPTree {
	root := templateLayoutNode(n, ids, e)
	if root == nil {
		return nil
	}
	return (&layout.SerializedBSPTree{Root: root}).Deserialize()
}

func templateLayoutNode(n *layouttmpl.Node, ids map[int]string, e *layoutEdit) *layout.SerializedNode {
	if n == nil {
		return nil
	}
	if n.Left == nil && n.Right == nil {
		id, ok := ids[n.Window]
		if !ok {
			return nil
		}
		return &layout.SerializedNode{WindowID: e.leaf(id)}
	}
	l, r := templateLayoutNode(n.Left, ids, e), templateLayoutNode(n.Right, ids, e)
	switch {
	case l == nil:
		return r
	case r == nil:
		return l
	}
	return &layout.SerializedNode{SplitType: n.Split, SplitRatio: n.Ratio, Left: l, Right: r}
}

// scaleTemplateRects puts each window that took a place at its rectangle,
// scaled from the size the layout was saved at to box.
func scaleTemplateRects(state *SessionState, t layouttmpl.Template, ids map[int]string, box layout.Rect) {
	sx, sy := 1.0, 1.0
	if t.ScreenWidth > 0 && t.ScreenHeight > 0 {
		sx = float64(box.X+box.W) / float64(t.ScreenWidth)
		sy = float64(box.Y+box.H) / float64(t.ScreenHeight)
	}
	for slot, id := range ids {
		i, err := findWindowStateIndex(state.Windows, id)
		if err != nil {
			continue
		}
		tw, w := t.Windows[slot-1], &state.Windows[i]
		w.X, w.Y = int(float64(tw.X)*sx), int(float64(tw.Y)*sy)
		w.Width, w.Height = max(int(float64(tw.Width)*sx), 10), max(int(float64(tw.Height)*sy), 5)
		w.Unplaced = false
	}
}

// newWindowIDOf is the window_id of a new-window result.
func newWindowIDOf(res any) string {
	if m, ok := res.(map[string]any); ok {
		if id, ok := m["window_id"].(string); ok {
			return id
		}
	}
	return ""
}

// cloneIntFloatMap copies m into a map a write may go to, also when m is nil.
func cloneIntFloatMap(m map[int]float64) map[int]float64 {
	out := make(map[int]float64, len(m)+1)
	maps.Copy(out, m)
	return out
}

// cloneIntStringMap copies m into a map a write may go to, also when m is nil.
func cloneIntStringMap(m map[int]string) map[int]string {
	out := make(map[int]string, len(m)+1)
	maps.Copy(out, m)
	return out
}
