package tmuxcompat

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

type pane struct {
	ID        string
	Num       uint32
	Index     int
	Workspace int
	Title     string
	Cwd       string
	X, Y      int
	Width     int
	Height    int
	Command   string
	History   int
	PID       int
	TTY       string
	Zoomed    bool
	sess      *sessionView
}

type sessionView struct {
	name      string
	id        string
	num       uint32
	server    bool
	activity  int64
	created   int64
	attached  bool
	width     int
	height    int
	panes     []pane
	current   int
	focused   string
	wsFocus   map[int]string
	wsName    map[int]string
	wsCount   map[int]int
	wsHistory map[int][]string
	workspace []int
}

type view struct {
	server   bool
	sessions []*sessionView
	def      *sessionView
}

type listedSession struct {
	Name       string `json:"name"`
	ID         string `json:"id"`
	Created    int64  `json:"created"`
	LastActive int64  `json:"last_active"`
	Attached   bool   `json:"attached"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

func (s *Shim) daemonSessions() ([]listedSession, error) {
	raw, err := s.Caller.Call("list-sessions", map[string]any{})
	if err != nil {
		return nil, err
	}
	var ls struct {
		Sessions []listedSession `json:"sessions"`
	}
	if err := jsonx.Unmarshal(raw, &ls); err != nil {
		return nil, errfmt.Errorf("read list-sessions: %w", err)
	}
	return ls.Sessions, nil
}

func (s *Shim) loadView() (*view, error) {
	v := &view{server: s.AllSessions}
	if !v.server {
		sv, err := s.loadSession(s.sessionInfo(), false)
		if err != nil {
			return nil, err
		}
		v.sessions = []*sessionView{sv}
		v.def = sv
		return v, nil
	}
	listed, err := s.daemonSessions()
	if err != nil {
		return nil, err
	}
	used := map[uint32]bool{}
	for _, l := range listed {
		sv, err := s.loadSession(l, true)
		if err != nil {
			var coded interface{ ErrorCode() string }
			if errors.As(err, &coded) && coded.ErrorCode() == "session_not_found" {
				continue
			}
			return nil, err
		}
		for used[sv.num] {
			sv.num = (sv.num + 1) & 0xfffff
			if sv.num == 0 {
				sv.num = 1
			}
		}
		used[sv.num] = true
		v.sessions = append(v.sessions, sv)
	}
	for _, sv := range v.sessions {
		if v.def == nil || sv.attached && !v.def.attached ||
			sv.attached == v.def.attached && sv.activity > v.def.activity {
			v.def = sv
		}
	}
	return v, nil
}

func (s *Shim) sessionInfo() listedSession {
	if listed, err := s.daemonSessions(); err == nil {
		for _, l := range listed {
			if l.Name == s.Session {
				return l
			}
		}
	}
	info := listedSession{Name: s.Session}
	raw, err := s.Caller.Call("session-info", map[string]any{"session": s.Session})
	if err != nil {
		return info
	}
	var si struct {
		ID       string `json:"session_id"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
		Attached bool   `json:"tui_attached"`
	}
	if jsonx.Unmarshal(raw, &si) == nil {
		info.ID, info.Width, info.Height, info.Attached = si.ID, si.Width, si.Height, si.Attached
	}
	return info
}

func (s *Shim) loadSession(info listedSession, server bool) (*sessionView, error) {
	raw, err := s.Caller.Call("list-windows", map[string]any{"session": info.Name})
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
			Foreground  string `json:"foreground_cmd"`
			History     *int   `json:"history_rows"`
			Scratch     bool   `json:"scratch"`
			PID         int    `json:"pid"`
			TTY         string `json:"tty"`
			Zoomed      bool   `json:"zoomed"`
		} `json:"windows"`
		Focused string `json:"focused_window_id"`
		Current int    `json:"current_workspace"`
	}
	if err := jsonx.Unmarshal(raw, &wl); err != nil {
		return nil, errfmt.Errorf("read list-windows: %w", err)
	}
	sv := &sessionView{
		name:      info.Name,
		id:        info.ID,
		server:    server,
		activity:  info.LastActive,
		created:   info.Created,
		attached:  info.Attached,
		width:     info.Width,
		height:    info.Height,
		current:   wl.Current,
		focused:   wl.Focused,
		wsFocus:   map[int]string{},
		wsName:    map[int]string{},
		wsCount:   map[int]int{},
		wsHistory: map[int][]string{},
	}
	if server {
		sv.num = sessionNumber(cmpOr(info.ID, info.Name))
	}
	for _, w := range wl.Windows {
		if w.Scratch || w.Workspace >= windowStride {
			continue
		}
		p := pane{
			ID:        w.ID,
			Num:       PaneNumber(w.ID),
			Index:     sv.wsCount[w.Workspace],
			Workspace: w.Workspace,
			Title:     w.DisplayName,
			Cwd:       w.Cwd,
			X:         w.X,
			Y:         w.Y,
			Width:     w.Width,
			Height:    w.Height,
			Command:   w.Foreground,
			History:   -1,
			PID:       w.PID,
			TTY:       w.TTY,
			Zoomed:    w.Zoomed,
		}
		if w.History != nil {
			p.History = *w.History
		}
		sv.wsCount[w.Workspace]++
		sv.panes = append(sv.panes, p)
	}
	for i := range sv.panes {
		sv.panes[i].sess = sv
	}

	raw, err = s.Caller.Call("list-workspaces", map[string]any{"session": info.Name})
	if err != nil {
		return nil, err
	}
	var ws struct {
		Workspaces []struct {
			Workspace int      `json:"workspace"`
			Name      string   `json:"name"`
			Focused   string   `json:"focused_window_id"`
			History   []string `json:"focus_history"`
		} `json:"workspaces"`
	}
	if err := jsonx.Unmarshal(raw, &ws); err != nil {
		return nil, errfmt.Errorf("read list-workspaces: %w", err)
	}
	for _, w := range ws.Workspaces {
		if w.Workspace >= windowStride {
			continue
		}
		sv.workspace = append(sv.workspace, w.Workspace)
		if w.Name != "" {
			sv.wsName[w.Workspace] = w.Name
		}
		if w.Focused != "" {
			sv.wsFocus[w.Workspace] = w.Focused
		}
		if len(w.History) > 0 {
			sv.wsHistory[w.Workspace] = w.History
		}
	}
	return sv, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (sv *sessionView) sessionID() string {
	return "$" + strconv.FormatUint(uint64(sv.num), 10)
}

func (sv *sessionView) windowID(ws int) string {
	if !sv.server {
		return "@" + strconv.Itoa(ws)
	}
	return "@" + strconv.FormatUint(uint64(sv.num)*windowStride+uint64(ws), 10)
}

func (sv *sessionView) panesOn(ws int) []*pane {
	var out []*pane
	for i := range sv.panes {
		if sv.panes[i].Workspace == ws {
			out = append(out, &sv.panes[i])
		}
	}
	return out
}

func (sv *sessionView) byWindowID(id string) *pane {
	for i := range sv.panes {
		if sv.panes[i].ID == id {
			return &sv.panes[i]
		}
	}
	return nil
}

func (sv *sessionView) active(ws int) *pane {
	on := sv.panesOn(ws)
	if len(on) == 0 {
		return nil
	}
	want := sv.wsFocus[ws]
	if want == "" && ws == sv.current {
		want = sv.focused
	}
	for _, p := range on {
		if p.ID == want {
			return p
		}
	}
	return on[0]
}

func (sv *sessionView) isActive(p *pane) bool {
	a := sv.active(p.Workspace)
	return a != nil && a.ID == p.ID
}

func (sv *sessionView) workspaceOf(ref string) (int, error) {
	ref = strings.TrimPrefix(ref, "=")
	if num, ok := strings.CutPrefix(ref, "@"); ok {
		n, err := strconv.ParseUint(num, 10, 32)
		if err != nil {
			return 0, errfmt.Errorf("can't find window: %s", ref)
		}
		ws := int(n)
		if sv.server {
			if uint32(n/windowStride) != sv.num {
				return 0, errfmt.Errorf("can't find window: %s", ref)
			}
			ws = int(n % windowStride)
		}
		if sv.hasWorkspace(ws) {
			return ws, nil
		}
		return 0, errfmt.Errorf("can't find window: %s", ref)
	}
	if n, err := strconv.Atoi(ref); err == nil {
		if sv.hasWorkspace(n) {
			return n, nil
		}
		return 0, errfmt.Errorf("can't find window: %s", ref)
	}
	for ws, name := range sv.wsName {
		if name == ref {
			return ws, nil
		}
	}
	return 0, errfmt.Errorf("can't find window: %s", ref)
}

func (sv *sessionView) hasWorkspace(ws int) bool {
	if slices.Contains(sv.workspace, ws) {
		return true
	}
	_, ok := sv.wsCount[ws]
	return ok
}

func (sv *sessionView) windowsInUse() []int {
	var out []int
	seen := map[int]bool{}
	for _, ws := range sv.workspace {
		if sv.wsCount[ws] > 0 && !seen[ws] {
			out = append(out, ws)
			seen[ws] = true
		}
	}
	for ws, n := range sv.wsCount {
		if n > 0 && !seen[ws] {
			out = append(out, ws)
			seen[ws] = true
		}
	}
	slices.Sort(out)
	return out
}

func (sv *sessionView) path() string {
	if len(sv.panes) > 0 {
		return sv.panes[0].Cwd
	}
	return ""
}

func (v *view) allPanes() []*pane {
	var out []*pane
	for _, sv := range v.sessions {
		for i := range sv.panes {
			out = append(out, &sv.panes[i])
		}
	}
	return out
}

func (v *view) byWindowID(id string) *pane {
	if id == "" {
		return nil
	}
	for _, sv := range v.sessions {
		if p := sv.byWindowID(id); p != nil {
			return p
		}
	}
	return nil
}

func (v *view) sessionOf(ref string) (*sessionView, bool) {
	ref = strings.TrimPrefix(ref, "=")
	if ref == "" {
		return nil, false
	}
	if num, ok := strings.CutPrefix(ref, "$"); ok {
		n, err := strconv.ParseUint(num, 10, 32)
		if err != nil {
			return nil, false
		}
		for _, sv := range v.sessions {
			if uint64(sv.num) == n {
				return sv, true
			}
		}
		return nil, false
	}
	for _, sv := range v.sessions {
		if sv.name == ref {
			return sv, true
		}
	}
	return nil, false
}

func (v *view) isSession(ref string) bool {
	_, ok := v.sessionOf(ref)
	return ok
}

func (v *view) windowByID(ref string) (*sessionView, int, bool) {
	num, ok := strings.CutPrefix(strings.TrimPrefix(ref, "="), "@")
	if !ok || !v.server {
		return nil, 0, false
	}
	n, err := strconv.ParseUint(num, 10, 32)
	if err != nil {
		return nil, 0, false
	}
	for _, sv := range v.sessions {
		if uint64(sv.num) == n/windowStride {
			ws := int(n % windowStride)
			if sv.hasWorkspace(ws) {
				return sv, ws, true
			}
		}
	}
	return nil, 0, false
}

func (v *view) paneByID(ref string) (*pane, error) {
	all := v.allPanes()
	if n, err := strconv.ParseUint(ref, 10, 32); err == nil {
		for _, p := range all {
			if uint64(p.Num) == n {
				return p, nil
			}
		}
	}
	if p := v.byWindowID(ref); p != nil {
		return p, nil
	}
	if len(ref) >= 4 {
		var hit *pane
		for _, p := range all {
			if strings.HasPrefix(p.ID, ref) {
				if hit != nil {
					return nil, errfmt.Errorf("can't find pane: %%%s (it matches more than one)", ref)
				}
				hit = p
			}
		}
		if hit != nil {
			return hit, nil
		}
	}
	return nil, errfmt.Errorf("can't find pane: %%%s", ref)
}

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

func (v *view) targetSession(sessRef string, hasSess bool, dflt *pane) (*sessionView, error) {
	if hasSess && sessRef != "" {
		sv, ok := v.sessionOf(sessRef)
		if !ok {
			return nil, errfmt.Errorf("can't find session: %s", sessRef)
		}
		return sv, nil
	}
	if dflt != nil {
		return dflt.sess, nil
	}
	if v.def == nil {
		return nil, errfmt.Errorf("no current session")
	}
	return v.def, nil
}

func (v *view) resolvePane(t string, dflt *pane) (*pane, error) {
	if t == "" {
		if dflt == nil {
			return nil, errfmt.Errorf("no current pane")
		}
		return dflt, nil
	}
	if strings.HasPrefix(t, "%") {
		return v.paneByID(t[1:])
	}
	if p := v.byWindowID(t); p != nil {
		return p, nil
	}
	sessRef, win, pn, hasSess, hasPane := splitTarget(t)
	sv, err := v.targetSession(sessRef, hasSess, dflt)
	if err != nil {
		return nil, err
	}
	if !hasSess && !hasPane {
		if named, ok := v.sessionOf(win); ok {
			sv, win = named, ""
		}
	}
	if win != "" {
		if wsv, _, ok := v.windowByID(win); ok && !hasSess {
			sv = wsv
		}
	}
	ws := sv.current
	if dflt != nil && dflt.sess == sv {
		ws = dflt.Workspace
	}
	if win != "" {
		n, err := sv.workspaceOf(win)
		if err != nil {
			if !hasSess && !hasPane {
				return nil, errfmt.Errorf("can't find pane: %s", t)
			}
			return nil, err
		}
		ws = n
	}
	if !hasPane || pn == "" {
		if win == "" && dflt != nil && dflt.sess == sv && !hasSess {
			return dflt, nil
		}
		if p := sv.active(ws); p != nil {
			return p, nil
		}
		return nil, errfmt.Errorf("can't find pane: %s", t)
	}
	if strings.HasPrefix(pn, "%") {
		return v.paneByID(pn[1:])
	}
	idx, err := strconv.Atoi(pn)
	if err != nil {
		return nil, errfmt.Errorf("can't find pane: %s", pn)
	}
	for _, p := range sv.panesOn(ws) {
		if p.Index == idx {
			return p, nil
		}
	}
	return nil, errfmt.Errorf("can't find pane: %s", pn)
}

func (v *view) resolveWindow(t string, dflt *pane) (*sessionView, int, error) {
	if t == "" {
		if dflt != nil {
			return dflt.sess, dflt.Workspace, nil
		}
		if v.def == nil {
			return nil, 0, errfmt.Errorf("no current window")
		}
		return v.def, v.def.current, nil
	}
	if strings.HasPrefix(t, "%") {
		p, err := v.paneByID(t[1:])
		if err != nil {
			return nil, 0, err
		}
		return p.sess, p.Workspace, nil
	}
	if p := v.byWindowID(t); p != nil {
		return p.sess, p.Workspace, nil
	}
	sessRef, win, _, hasSess, _ := splitTarget(t)
	if !hasSess {
		if sv, ws, ok := v.windowByID(win); ok {
			return sv, ws, nil
		}
	}
	sv, err := v.targetSession(sessRef, hasSess, dflt)
	if err != nil {
		return nil, 0, err
	}
	if !hasSess {
		if named, ok := v.sessionOf(win); ok {
			sv, win = named, ""
		}
	}
	if win == "" {
		if dflt != nil && dflt.sess == sv && !hasSess {
			return sv, dflt.Workspace, nil
		}
		return sv, sv.current, nil
	}
	ws, err := sv.workspaceOf(win)
	return sv, ws, err
}

func (s *Shim) sessionVars(sv *sessionView) map[string]string {
	host, _ := os.Hostname()
	short := host
	if i := strings.IndexByte(short, '.'); i >= 0 {
		short = short[:i]
	}
	vars := map[string]string{
		"host":       host,
		"host_short": short,
		"version":    Version,
	}
	if s.Dir != "" {
		vars["socket_path"] = SocketPath(s.Dir)
	}
	if sv == nil {
		return vars
	}
	vars["session_name"] = sv.name
	vars["session_id"] = sv.sessionID()
	vars["session_windows"] = strconv.Itoa(len(sv.windowsInUse()))
	vars["session_attached"] = boolString(sv.attached || !sv.server)
	vars["session_activity"] = strconv.FormatInt(sv.activity, 10)
	vars["session_created"] = strconv.FormatInt(sv.created, 10)
	vars["session_path"] = sv.path()
	return vars
}

func (s *Shim) windowVars(sv *sessionView, ws int, vars map[string]string) {
	on := sv.panesOn(ws)
	name := sv.wsName[ws]
	auto := name == ""
	if name == "" {
		if a := sv.active(ws); a != nil && a.Title != "" {
			name = a.Title
		} else {
			name = strconv.Itoa(ws)
		}
	}
	minX, minY, maxX, maxY := bounds(on)
	zoomed := false
	for _, p := range on {
		zoomed = zoomed || p.Zoomed
	}
	flags := ""
	if ws == sv.current {
		flags = "*"
	}
	if zoomed {
		flags += "Z"
	}
	vars["window_id"] = sv.windowID(ws)
	vars["window_index"] = strconv.Itoa(ws)
	vars["window_name"] = name
	vars["window_active"] = boolString(ws == sv.current)
	vars["window_panes"] = strconv.Itoa(len(on))
	vars["window_flags"] = flags
	vars["window_width"] = strconv.Itoa(maxX - minX)
	vars["window_height"] = strconv.Itoa(maxY - minY)
	vars["automatic-rename"] = boolString(auto)
	vars["window_zoomed_flag"] = boolString(zoomed)
	layout := layoutOf(on)
	vars["window_layout"] = layout
	vars["window_visible_layout"] = layout
}

func bounds(panes []*pane) (minX, minY, maxX, maxY int) {
	for i, p := range panes {
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
	return
}

func (s *Shim) serverVar(name string) (string, bool) {
	if name != "pid" {
		return "", false
	}
	if !s.pidRead {
		s.pidRead = true
		if s.Caller != nil {
			if raw, err := s.Caller.Call("hello", map[string]any{}); err == nil {
				var h struct {
					PID int `json:"pid"`
				}
				if jsonx.Unmarshal(raw, &h) == nil {
					s.daemonPID = h.PID
				}
			}
		}
	}
	if s.daemonPID > 0 {
		return strconv.Itoa(s.daemonPID), true
	}
	if s.ServerPID > 0 {
		return strconv.Itoa(s.ServerPID), true
	}
	return "", false
}

func (s *Shim) paneVars(p *pane) map[string]string {
	vars := s.sessionVars(p.sess)
	s.windowVars(p.sess, p.Workspace, vars)
	vars["pane_id"] = "%" + strconv.FormatUint(uint64(p.Num), 10)
	vars["pane_index"] = strconv.Itoa(p.Index)
	vars["pane_title"] = p.Title
	vars["pane_current_path"] = p.Cwd
	vars["pane_active"] = boolString(p.sess.isActive(p))
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
	minX, minY, maxX, maxY := bounds(p.sess.panesOn(p.Workspace))
	vars["pane_at_left"] = boolString(p.X <= minX)
	vars["pane_at_top"] = boolString(p.Y <= minY)
	vars["pane_at_right"] = boolString(p.X+p.Width >= maxX)
	vars["pane_at_bottom"] = boolString(p.Y+p.Height >= maxY)
	if p.PID > 0 {
		vars["pane_pid"] = strconv.Itoa(p.PID)
	}
	if p.TTY != "" {
		vars["pane_tty"] = p.TTY
	}
	shell := ""
	if s.Shell != "" {
		shell = filepath.Base(s.Shell)
	}
	vars["pane_current_command"] = cmpOr(p.Command, shell)
	if p.History >= 0 {
		vars["history_size"] = strconv.Itoa(p.History)
	}
	vars["tuios_window_id"] = p.ID
	return vars
}
