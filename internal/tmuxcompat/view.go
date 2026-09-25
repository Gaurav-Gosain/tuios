package tmuxcompat

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// pane is one tuios window seen as a tmux pane.
type pane struct {
	ID        string
	Num       uint32
	Index     int // position among the panes of its workspace
	Workspace int
	Title     string
	Cwd       string
	X, Y      int
	Width     int
	Height    int
}

// view is one read of the caller's session: its windows and workspaces.
type view struct {
	session   string
	panes     []pane
	current   int
	focused   string
	wsFocus   map[int]string
	wsName    map[int]string
	wsCount   map[int]int
	workspace []int // every workspace number the session has
}

// loadView reads the session through list-windows and list-workspaces.
func (s *Shim) loadView() (*view, error) {
	raw, err := s.Caller.Call("list-windows", map[string]any{"session": s.Session})
	if err != nil {
		return nil, err
	}
	var wl struct {
		Windows []struct {
			ID          string `json:"window_id"`
			DisplayName string `json:"display_name"`
			Workspace   int    `json:"workspace"`
			Cwd         string `json:"cwd"`
			X           int    `json:"x"`
			Y           int    `json:"y"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
		} `json:"windows"`
		Focused string `json:"focused_window_id"`
		Current int    `json:"current_workspace"`
	}
	if err := json.Unmarshal(raw, &wl); err != nil {
		return nil, fmt.Errorf("read list-windows: %w", err)
	}
	v := &view{
		session: s.Session,
		current: wl.Current,
		focused: wl.Focused,
		wsFocus: map[int]string{},
		wsName:  map[int]string{},
		wsCount: map[int]int{},
	}
	for _, w := range wl.Windows {
		p := pane{
			ID:        w.ID,
			Num:       PaneNumber(w.ID),
			Index:     v.wsCount[w.Workspace],
			Workspace: w.Workspace,
			Title:     w.DisplayName,
			Cwd:       w.Cwd,
			X:         w.X,
			Y:         w.Y,
			Width:     w.Width,
			Height:    w.Height,
		}
		v.wsCount[w.Workspace]++
		v.panes = append(v.panes, p)
	}

	raw, err = s.Caller.Call("list-workspaces", map[string]any{"session": s.Session})
	if err != nil {
		return nil, err
	}
	var ws struct {
		Workspaces []struct {
			Workspace int    `json:"workspace"`
			Name      string `json:"name"`
			Focused   string `json:"focused_window_id"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &ws); err != nil {
		return nil, fmt.Errorf("read list-workspaces: %w", err)
	}
	for _, w := range ws.Workspaces {
		v.workspace = append(v.workspace, w.Workspace)
		if w.Name != "" {
			v.wsName[w.Workspace] = w.Name
		}
		if w.Focused != "" {
			v.wsFocus[w.Workspace] = w.Focused
		}
	}
	return v, nil
}

// panesOn lists the panes of one workspace, in list-windows order.
func (v *view) panesOn(ws int) []*pane {
	var out []*pane
	for i := range v.panes {
		if v.panes[i].Workspace == ws {
			out = append(out, &v.panes[i])
		}
	}
	return out
}

// byWindowID finds the pane of a tuios window id.
func (v *view) byWindowID(id string) *pane {
	for i := range v.panes {
		if v.panes[i].ID == id {
			return &v.panes[i]
		}
	}
	return nil
}

// active is the active pane of a workspace: its focused window, or its first.
func (v *view) active(ws int) *pane {
	on := v.panesOn(ws)
	if len(on) == 0 {
		return nil
	}
	want := v.wsFocus[ws]
	if want == "" && ws == v.current {
		want = v.focused
	}
	for _, p := range on {
		if p.ID == want {
			return p
		}
	}
	return on[0]
}

func (v *view) isActive(p *pane) bool {
	a := v.active(p.Workspace)
	return a != nil && a.ID == p.ID
}

// isSession reports whether a target's session part names the caller's
// session. "=" asks for an exact match, which is the only kind the shim does.
func (v *view) isSession(name string) bool {
	name = strings.TrimPrefix(name, "=")
	return name == v.session || name == "$0"
}

// paneByID resolves the part after "%": the pane number, or a tuios window id
// or a prefix of one at least four characters long that matches one window.
func (v *view) paneByID(ref string) (*pane, error) {
	if n, err := strconv.ParseUint(ref, 10, 32); err == nil {
		for i := range v.panes {
			if uint64(v.panes[i].Num) == n {
				return &v.panes[i], nil
			}
		}
	}
	if p := v.byWindowID(ref); p != nil {
		return p, nil
	}
	if len(ref) >= 4 {
		var hit *pane
		for i := range v.panes {
			if strings.HasPrefix(v.panes[i].ID, ref) {
				if hit != nil {
					return nil, fmt.Errorf("can't find pane: %%%s (it matches more than one)", ref)
				}
				hit = &v.panes[i]
			}
		}
		if hit != nil {
			return hit, nil
		}
	}
	return nil, fmt.Errorf("can't find pane: %%%s", ref)
}

// workspaceOf resolves a tmux window reference: "@N", "N", or a workspace
// name.
func (v *view) workspaceOf(ref string) (int, error) {
	ref = strings.TrimPrefix(ref, "=")
	num := strings.TrimPrefix(ref, "@")
	if n, err := strconv.Atoi(num); err == nil {
		if slices.Contains(v.workspace, n) {
			return n, nil
		}
		if _, ok := v.wsCount[n]; ok {
			return n, nil
		}
		return 0, fmt.Errorf("can't find window: %s", ref)
	}
	for ws, name := range v.wsName {
		if name == ref {
			return ws, nil
		}
	}
	return 0, fmt.Errorf("can't find window: %s", ref)
}

// splitTarget cuts a target ("session:window.pane") into its parts. hasSess
// is false when the target has no ":", and hasPane when it has no ".".
func splitTarget(t string) (sess, win, pn string, hasSess, hasPane bool) {
	rest := t
	if before, after, ok := strings.Cut(t, ":"); ok {
		sess, rest, hasSess = before, after, true
	}
	if i := strings.LastIndexByte(rest, '.'); i >= 0 {
		win, pn, hasPane = rest[:i], rest[i+1:], true
	} else {
		win = rest
	}
	return
}

// resolvePane resolves a pane target. dflt is the pane an empty target means.
func (v *view) resolvePane(t string, dflt *pane) (*pane, error) {
	if t == "" {
		if dflt == nil {
			return nil, fmt.Errorf("no current pane")
		}
		return dflt, nil
	}
	if strings.HasPrefix(t, "%") {
		return v.paneByID(t[1:])
	}
	if p := v.byWindowID(t); p != nil {
		return p, nil
	}
	sess, win, pn, hasSess, hasPane := splitTarget(t)
	if hasSess && sess != "" && !v.isSession(sess) {
		return nil, fmt.Errorf("can't find session: %s", sess)
	}
	if !hasSess && !hasPane && v.isSession(win) {
		win = ""
	}
	ws := v.current
	if dflt != nil {
		ws = dflt.Workspace
	}
	if win != "" {
		n, err := v.workspaceOf(win)
		if err != nil {
			if !hasSess && !hasPane {
				return nil, fmt.Errorf("can't find pane: %s", t)
			}
			return nil, err
		}
		ws = n
	}
	if !hasPane || pn == "" {
		if win == "" && dflt != nil && !hasSess {
			return dflt, nil
		}
		if p := v.active(ws); p != nil {
			return p, nil
		}
		return nil, fmt.Errorf("can't find pane: %s", t)
	}
	if strings.HasPrefix(pn, "%") {
		return v.paneByID(pn[1:])
	}
	idx, err := strconv.Atoi(pn)
	if err != nil {
		return nil, fmt.Errorf("can't find pane: %s", pn)
	}
	for _, p := range v.panesOn(ws) {
		if p.Index == idx {
			return p, nil
		}
	}
	return nil, fmt.Errorf("can't find pane: %s", pn)
}

// resolveWindow resolves a window target to a workspace number.
func (v *view) resolveWindow(t string, dflt *pane) (int, error) {
	if t == "" {
		if dflt != nil {
			return dflt.Workspace, nil
		}
		return v.current, nil
	}
	if strings.HasPrefix(t, "%") {
		p, err := v.paneByID(t[1:])
		if err != nil {
			return 0, err
		}
		return p.Workspace, nil
	}
	if p := v.byWindowID(t); p != nil {
		return p.Workspace, nil
	}
	sess, win, _, hasSess, _ := splitTarget(t)
	if hasSess && sess != "" && !v.isSession(sess) {
		return 0, fmt.Errorf("can't find session: %s", sess)
	}
	if !hasSess && v.isSession(win) {
		win = ""
	}
	if win == "" {
		if dflt != nil && !hasSess {
			return dflt.Workspace, nil
		}
		return v.current, nil
	}
	return v.workspaceOf(win)
}

// windowsInUse lists the workspaces that hold panes, ascending: the session's
// tmux windows.
func (v *view) windowsInUse() []int {
	var out []int
	seen := map[int]bool{}
	for _, ws := range v.workspace {
		if v.wsCount[ws] > 0 && !seen[ws] {
			out = append(out, ws)
			seen[ws] = true
		}
	}
	for ws, n := range v.wsCount {
		if n > 0 && !seen[ws] {
			out = append(out, ws)
			seen[ws] = true
		}
	}
	slices.Sort(out)
	return out
}

// sessionVars are the format variables every context has.
func (s *Shim) sessionVars(v *view) map[string]string {
	host, _ := os.Hostname()
	short := host
	if i := strings.IndexByte(short, '.'); i >= 0 {
		short = short[:i]
	}
	vars := map[string]string{
		"session_name":     v.session,
		"session_id":       "$0",
		"session_windows":  strconv.Itoa(len(v.windowsInUse())),
		"session_attached": "1",
		"host":             host,
		"host_short":       short,
		"version":          Version,
		"pid":              strconv.Itoa(s.ServerPID),
	}
	if s.Dir != "" {
		vars["socket_path"] = SocketPath(s.Dir)
	}
	return vars
}

// windowVars adds the variables of workspace ws.
func (s *Shim) windowVars(v *view, ws int, vars map[string]string) {
	on := v.panesOn(ws)
	name := v.wsName[ws]
	if name == "" {
		if a := v.active(ws); a != nil && a.Title != "" {
			name = a.Title
		} else {
			name = strconv.Itoa(ws)
		}
	}
	minX, minY, maxX, maxY := 0, 0, 0, 0
	for i, p := range on {
		if i == 0 || p.X < minX {
			minX = p.X
		}
		if i == 0 || p.Y < minY {
			minY = p.Y
		}
		if p.X+p.Width > maxX {
			maxX = p.X + p.Width
		}
		if p.Y+p.Height > maxY {
			maxY = p.Y + p.Height
		}
	}
	flags := ""
	if ws == v.current {
		flags = "*"
	}
	vars["window_id"] = "@" + strconv.Itoa(ws)
	vars["window_index"] = strconv.Itoa(ws)
	vars["window_name"] = name
	vars["window_active"] = boolString(ws == v.current)
	vars["window_panes"] = strconv.Itoa(len(on))
	vars["window_flags"] = flags
	vars["window_width"] = strconv.Itoa(maxX - minX)
	vars["window_height"] = strconv.Itoa(maxY - minY)
}

// paneVars is the whole context for one pane.
func (s *Shim) paneVars(v *view, p *pane) map[string]string {
	vars := s.sessionVars(v)
	s.windowVars(v, p.Workspace, vars)
	vars["pane_id"] = "%" + strconv.FormatUint(uint64(p.Num), 10)
	vars["pane_index"] = strconv.Itoa(p.Index)
	vars["pane_title"] = p.Title
	vars["pane_current_path"] = p.Cwd
	vars["pane_active"] = boolString(v.isActive(p))
	vars["pane_width"] = strconv.Itoa(p.Width)
	vars["pane_height"] = strconv.Itoa(p.Height)
	vars["pane_left"] = strconv.Itoa(p.X)
	vars["pane_top"] = strconv.Itoa(p.Y)
	vars["pane_right"] = strconv.Itoa(p.X + p.Width - 1)
	vars["pane_bottom"] = strconv.Itoa(p.Y + p.Height - 1)
	vars["pane_dead"] = "0"
	vars["pane_in_mode"] = "0"
	vars["pane_marked"] = "0"
	vars["pane_synchronized"] = "0"
	vars["tuios_window_id"] = p.ID
	return vars
}
