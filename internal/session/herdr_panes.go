package session

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	tlayout "github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/google/uuid"
)

// herdr's pane navigation, zoom, process and agent start methods, and
// workspace.focus: the shapes of herdr's src/api/schema/panes.rs and
// agents.rs at herdrTargetVersion. Tools built for herdr call them through
// herdr's CLI: terminal-browser and terminal-code split, swap and find a
// neighbour, the Vim navigation plugins step across panes, and the Telegram
// bridges start agents and show a workspace.
//
// A neighbour is the pane focus moves to in tuios (layout.Neighbour), found
// in the layout of the pane's tab that pane.layout reports. A session with no
// client attached has no layout, its panes all hold the whole screen, and so
// no pane has a neighbour.
//
// As for the rest of the API, each method is held to the caller's grants
// before it changes anything: a read needs read, and a focus, swap or zoom
// needs admin, as focus-window, set-layout and run-command do. agent.start
// needs what start-agent needs and types with send-text.

// herdrProcessInfo is herdr's PaneProcessInfo.
type herdrProcessInfo struct {
	PaneID              string             `json:"pane_id"`
	ShellPID            int                `json:"shell_pid,omitempty"`
	ForegroundPGID      int                `json:"foreground_process_group_id,omitempty"`
	ForegroundProcesses []herdrProcessItem `json:"foreground_processes,omitempty"`
}

// herdrProcessItem is herdr's PaneProcessInfoProcess.
type herdrProcessItem struct {
	PID     int      `json:"pid"`
	Name    string   `json:"name"`
	Argv0   string   `json:"argv0,omitempty"`
	Argv    []string `json:"argv,omitempty"`
	Cmdline string   `json:"cmdline,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
}

// herdrNeighbor is herdr's PaneNeighborResult.
type herdrNeighbor struct {
	PaneID         string      `json:"pane_id"`
	Direction      string      `json:"direction"`
	NeighborPaneID string      `json:"neighbor_pane_id,omitempty"`
	Layout         herdrLayout `json:"layout"`
}

// herdrEdges is herdr's PaneEdgesResult: true on a side where the pane is at
// the edge of its tab.
type herdrEdges struct {
	PaneID string      `json:"pane_id"`
	Left   bool        `json:"left"`
	Right  bool        `json:"right"`
	Up     bool        `json:"up"`
	Down   bool        `json:"down"`
	Layout herdrLayout `json:"layout"`
}

// herdrFocusMove is herdr's PaneFocusDirectionResult.
type herdrFocusMove struct {
	Changed       bool        `json:"changed"`
	Reason        string      `json:"reason,omitempty"`
	SourcePaneID  string      `json:"source_pane_id"`
	FocusedPaneID string      `json:"focused_pane_id,omitempty"`
	Layout        herdrLayout `json:"layout"`
}

// herdrSwap is herdr's PaneSwapResult.
type herdrSwap struct {
	Changed       bool        `json:"changed"`
	Reason        string      `json:"reason,omitempty"`
	SourcePaneID  string      `json:"source_pane_id"`
	TargetPaneID  string      `json:"target_pane_id,omitempty"`
	FocusedPaneID string      `json:"focused_pane_id"`
	Layout        herdrLayout `json:"layout"`
}

// herdrZoom is herdr's PaneZoomResult.
type herdrZoom struct {
	Changed       bool        `json:"changed"`
	ZoomChanged   bool        `json:"zoom_changed"`
	FocusChanged  bool        `json:"focus_changed"`
	Reason        string      `json:"reason,omitempty"`
	PaneID        string      `json:"pane_id"`
	FocusedPaneID string      `json:"focused_pane_id"`
	Zoomed        bool        `json:"zoomed"`
	Layout        herdrLayout `json:"layout"`
}

// herdrSite is a pane with the layout of its tab.
type herdrSite struct {
	sess   *Session
	win    WindowState
	pane   *herdrPaneInfo
	layout herdrLayout
}

// herdrSiteOf finds the pane id names, or with none the focused pane, and
// the layout of its tab. The caller must be allowed to read the session.
func (d *Daemon) herdrSiteOf(cs *connState, id string) (*herdrSite, *herdrError) {
	if id == "" {
		if id = d.buildHerdrView(cs).focusedPane; id == "" {
			return nil, herdrErr("pane_not_found", "pane not found")
		}
	}
	sess, win, pane, v, herr := d.herdrPaneView(cs, id)
	if herr != nil {
		return nil, herr
	}
	for _, l := range v.layouts {
		if l.TabID == pane.TabID {
			return &herdrSite{sess: sess, win: win, pane: pane, layout: l}, nil
		}
	}
	return nil, herdrErr("pane_layout_unavailable", "pane layout unavailable")
}

// neighbour is the pane focus moves to from the site's pane toward side, ""
// for none: the one tuios's own directional focus picks.
func (s *herdrSite) neighbour(side string) string {
	sd, ok := tlayout.ParseSide(side)
	if !ok {
		return ""
	}
	var from *tlayout.Rect
	var ids []string
	var cands []tlayout.Rect
	for _, p := range s.layout.Panes {
		r := tlayout.Rect{X: p.Rect.X, Y: p.Rect.Y, W: p.Rect.Width, H: p.Rect.Height}
		if p.PaneID == s.pane.PaneID {
			from = &r
			continue
		}
		ids, cands = append(ids, p.PaneID), append(cands, r)
	}
	if from == nil {
		return ""
	}
	if i := tlayout.Neighbour(*from, cands, sd, false); i >= 0 {
		return ids[i]
	}
	return ""
}

// herdrDirection checks a pane direction, in serde's words.
func herdrDirection(dir string) *herdrError {
	switch dir {
	case "left", "right", "up", "down":
		return nil
	}
	return herdrErr("invalid_request", "invalid request: unknown variant `"+echoName(dir)+"`, expected one of `left`, `right`, `up`, `down`")
}

// herdrAdmit holds the caller to what verb needs on the session.
func (d *Daemon) herdrAdmit(cs *connState, verb string, sess *Session) *herdrError {
	if _, _, verr := d.admitVerb(cs, verb, herdrSessionParams(sess)); verr != nil {
		return herdrFromVerb(verr, "forbidden")
	}
	return nil
}

func (d *Daemon) herdrPaneProcessInfo(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	site, herr := d.herdrSiteOf(cs, in.PaneID)
	if herr != nil {
		return nil, herr
	}
	pty := site.sess.GetPTY(site.win.PTYID)
	if pty == nil {
		return nil, herdrErr("pane_not_found", "pane not found")
	}
	info := &herdrProcessInfo{PaneID: site.pane.PaneID, ShellPID: pty.ShellPID()}
	// A command line and a directory can carry what never shows on the
	// screen: a token passed as an argument, a private path. The read grant
	// shows the screen, so it gets the pids and names. The arguments and the
	// directories go to the person, to the pane itself, and to a pane that
	// may type into this one (write on its session, or admin).
	full := true
	if pa := d.paneAuthority(cs); pa != nil && pa.window != site.win.ID {
		full = (pa.grants.Has(GrantAdmin) || pa.grants.Has(GrantWrite)) && d.paneWriteReach(pa, site.sess.Name()) == ""
	}
	if info.ShellPID > 0 {
		if pgid, ok := readForegroundPGID(info.ShellPID); ok && pgid > 0 {
			info.ForegroundPGID = pgid
			leader := readProcessInfo(pgid)
			leader.pid = pgid
			procs := []foregroundInfo{leader}
			if pgid != info.ShellPID {
				if walk := foregroundGroup(pgid, agentGroupWalkLimit, agentGroupWalkDepth); walk != nil {
					for p := range walk {
						procs = append(procs, p)
					}
				}
			}
			for _, p := range procs {
				if p.comm == "" && len(p.argv) == 0 {
					continue
				}
				item := herdrProcessItem{PID: p.pid, Name: p.comm}
				if item.Name == "" && len(p.argv) > 0 {
					item.Name = agentBaseName(p.argv[0])
				}
				if full {
					item.Argv, item.Cmdline = p.argv, strings.Join(p.argv, " ")
					if len(p.argv) > 0 {
						item.Argv0 = p.argv[0]
					}
					item.Cwd, _ = ptyspawn.ProcessCwd(p.pid)
				}
				info.ForegroundProcesses = append(info.ForegroundProcesses, item)
			}
		}
	}
	return &herdrResult{Type: "pane_process_info", ProcessInfo: info}, nil
}

func (d *Daemon) herdrPaneNeighbor(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if herr := herdrDirection(in.Direction); herr != nil {
		return nil, herr
	}
	site, herr := d.herdrSiteOf(cs, in.PaneID)
	if herr != nil {
		return nil, herr
	}
	return &herdrResult{Type: "pane_neighbor", Neighbor: &herdrNeighbor{
		PaneID: site.pane.PaneID, Direction: in.Direction, NeighborPaneID: site.neighbour(in.Direction), Layout: site.layout,
	}}, nil
}

func (d *Daemon) herdrPaneEdges(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	site, herr := d.herdrSiteOf(cs, in.PaneID)
	if herr != nil {
		return nil, herr
	}
	return &herdrResult{Type: "pane_edges", Edges: &herdrEdges{
		PaneID: site.pane.PaneID,
		Left:   site.neighbour("left") == "", Right: site.neighbour("right") == "",
		Up: site.neighbour("up") == "", Down: site.neighbour("down") == "",
		Layout: site.layout,
	}}, nil
}

func (d *Daemon) herdrPaneFocusDirection(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if herr := herdrDirection(in.Direction); herr != nil {
		return nil, herr
	}
	site, herr := d.herdrSiteOf(cs, in.PaneID)
	if herr != nil {
		return nil, herr
	}
	if herr := d.herdrAdmit(cs, "focus-window", site.sess); herr != nil {
		return nil, herr
	}
	res := &herdrFocusMove{SourcePaneID: site.pane.PaneID}
	if target := site.neighbour(in.Direction); target != "" {
		if _, herr := d.herdrFocusPane(cs, target); herr != nil {
			return nil, herr
		}
		res.Changed = true
	} else {
		res.Reason = "no_neighbor"
	}
	if after, herr := d.herdrSiteOf(cs, site.pane.PaneID); herr == nil {
		site = after
	}
	res.FocusedPaneID, res.Layout = site.layout.FocusedPaneID, site.layout
	return &herdrResult{Type: "pane_focus_direction", Focus: res}, nil
}

// herdrPaneSwap swaps a pane with its neighbour, or two named panes of one
// tab. The split tree lives in the client, so the swap is routed to the
// attached client, which swaps the pair and leaves the focus on the source,
// as herdr does. A swap that cannot happen answers with a reason and changes
// nothing, as in herdr.
func (d *Daemon) herdrPaneSwap(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	directional := in.Direction != ""
	if directional == (in.SourcePaneID != "" || in.TargetPaneID != "") {
		return nil, herdrErr("invalid_pane_swap", "provide either direction with optional pane_id, or source_pane_id and target_pane_id")
	}
	var site *herdrSite
	res := &herdrSwap{}
	if directional {
		if herr := herdrDirection(in.Direction); herr != nil {
			return nil, herr
		}
		s, herr := d.herdrSiteOf(cs, in.PaneID)
		if herr != nil {
			return nil, herr
		}
		site, res.SourcePaneID = s, s.pane.PaneID
		if res.TargetPaneID = s.neighbour(in.Direction); res.TargetPaneID == "" {
			res.Reason = "no_neighbor"
		}
	} else {
		src, serr := d.herdrSiteOf(cs, in.SourcePaneID)
		dst, derr := d.herdrSiteOf(cs, in.TargetPaneID)
		res.SourcePaneID, res.TargetPaneID = in.SourcePaneID, in.TargetPaneID
		switch {
		case serr != nil && derr != nil:
			s, herr := d.herdrSiteOf(cs, "")
			if herr != nil {
				return nil, herr
			}
			site, res.Reason = s, "not_found"
		case serr != nil || derr != nil:
			site, res.Reason = cmpOr(src, dst), "not_found"
		case src.pane.PaneID == dst.pane.PaneID:
			site, res.Reason = src, "same_pane"
		case src.pane.TabID != dst.pane.TabID:
			site, res.Reason = src, "cross_tab"
		default:
			site = src
		}
		if src != nil {
			res.SourcePaneID = src.pane.PaneID
		}
		if dst != nil {
			res.TargetPaneID = dst.pane.PaneID
		}
	}
	if herr := d.herdrAdmit(cs, "set-layout", site.sess); herr != nil {
		return nil, herr
	}
	if res.Reason == "" {
		_, src, ierr := d.herdrFindPane(res.SourcePaneID)
		if ierr != nil {
			return nil, ierr.herdr()
		}
		_, dst, ierr := d.herdrFindPane(res.TargetPaneID)
		if ierr != nil {
			return nil, ierr.herdr()
		}
		if herr := d.herdrRouteClient(site.sess, "swap_windows", "pane_swap_failed", src.ID, dst.ID); herr != nil {
			return nil, herr
		}
		res.Changed = true
	}
	if after, herr := d.herdrSiteOf(cs, site.pane.PaneID); herr == nil {
		site = after
	}
	res.FocusedPaneID, res.Layout = site.layout.FocusedPaneID, site.layout
	return &herdrResult{Type: "pane_swap", Swap: res}, nil
}

// cmpOr is the first of a and b that is not nil.
func cmpOr(a, b *herdrSite) *herdrSite {
	if a != nil {
		return a
	}
	return b
}

// herdrRouteClient sends one command to the client attached to sess and
// waits for it to be done.
func (d *Daemon) herdrRouteClient(sess *Session, command, fail string, args ...string) *herdrError {
	tui := d.findTUIClient(sess.ID)
	if tui == nil {
		return herdrErr("no_client", "this needs a tuios client attached to the session. Attach one with tuios attach "+sess.Name()+", then try again")
	}
	res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{CommandType: command, TapeArgs: args}, routedVerbTimeout)
	if err != nil {
		return herdrErr("timeout", "the attached client did not answer: "+err.Error())
	}
	if res == nil || !res.Success {
		msg := "the attached client refused the request"
		if res != nil && res.Message != "" {
			msg = res.Message
		}
		return herdrErr(fail, msg)
	}
	return nil
}

// herdrPaneZoom zooms a pane, the way herdr zooms a tab: the pane is focused,
// and a tab is zoomed on its focused pane or not at all. The client does both
// in one routed command that names the pane, so the zoom can never land on
// the pane that had the focus before. The answer waits until the session's
// state shows the zoom on that pane, or no zoom for off.
func (d *Daemon) herdrPaneZoom(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	mode := in.Mode
	switch mode {
	case "":
		mode = "toggle"
	case "toggle", "on", "off":
	default:
		return nil, herdrErr("invalid_request", "invalid request: unknown variant `"+echoName(mode)+"`, expected one of `toggle`, `on`, `off`")
	}
	site, herr := d.herdrSiteOf(cs, in.PaneID)
	if herr != nil {
		return nil, herr
	}
	if herr := d.herdrAdmit(cs, "run-command", site.sess); herr != nil {
		return nil, herr
	}
	res := &herdrZoom{PaneID: site.pane.PaneID}
	st := site.sess.GetState()
	res.FocusChanged = st.FocusedWindowID != site.win.ID || st.CurrentWorkspace != site.win.Workspace
	tabZoomed := herdrZoomedIn(st, site.win.Workspace) != ""
	want := !tabZoomed
	switch {
	case len(site.layout.Panes) <= 1:
		res.Reason = "single_pane"
	case mode == "on" && tabZoomed:
		res.Reason = "already_zoomed"
	case mode == "off" && !tabZoomed:
		res.Reason = "already_unzoomed"
	case mode != "toggle":
		want = mode == "on"
	}
	if res.Reason != "" {
		want = tabZoomed
		if res.FocusChanged {
			if _, herr := d.herdrFocusPane(cs, site.pane.PaneID); herr != nil {
				return nil, herr
			}
		}
	} else {
		arg := "off"
		if want {
			arg = "on"
		}
		if herr := d.herdrRouteClient(site.sess, "zoom_window", "pane_zoom_failed", site.win.ID, arg); herr != nil {
			return nil, herr
		}
		res.ZoomChanged = true
	}
	// The client pushes its state before it answers, but a push from an
	// earlier change can still be on its way. The answer waits, bounded, for
	// the state that shows the zoom where it was asked for.
	done := func(st *SessionState) bool {
		z := herdrZoomedIn(st, site.win.Workspace)
		return st.FocusedWindowID == site.win.ID && (want && z == site.win.ID || !want && z == "")
	}
	for deadline := time.Now().Add(routedVerbTimeout); !done(site.sess.GetState()) && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if after, herr := d.herdrSiteOf(cs, site.pane.PaneID); herr == nil {
		site = after
	}
	res.Changed = res.ZoomChanged || res.FocusChanged
	res.Zoomed, res.FocusedPaneID, res.Layout = site.layout.Zoomed, site.layout.FocusedPaneID, site.layout
	return &herdrResult{Type: "pane_zoom", Zoom: res}, nil
}

// herdrZoomedIn is the window zoomed on workspace ws, "" for none.
func herdrZoomedIn(st *SessionState, ws int) string {
	for i := range st.Windows {
		if w := &st.Windows[i]; w.Workspace == ws && w.Zoomed && !herdrScratch(w) {
			return w.ID
		}
	}
	return ""
}

// herdrWorkspaceFocus shows a session on the client the caller is shown
// on: the client of the caller's own session for a caller in a pane, else
// the client of the session herdr calls the active workspace. The client
// switches to the session, as it does when the person picks it in the
// session switcher.
func (d *Daemon) herdrWorkspaceFocus(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	sess, ierr := d.herdrFindSession(in.WorkspaceID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	if herr := d.herdrAdmit(cs, "select-workspace", sess); herr != nil {
		return nil, herr
	}
	if d.findTUIClient(sess.ID) == nil {
		var from *Session
		if fromPane, window := d.peerPane(cs); fromPane {
			from = d.sessionHoldingWindow(window)
		}
		if from == nil || d.findTUIClient(from.ID) == nil {
			from = d.herdrFocusedSession(d.herdrSessions(cs))
		}
		if from == nil || d.findTUIClient(from.ID) == nil {
			return nil, herdrErr("no_client", "no tuios client is attached to show the workspace on. Attach one with tuios attach "+sess.Name())
		}
		if herr := d.herdrRouteClient(from, "switch_session", "workspace_focus_failed", sess.Name()); herr != nil {
			return nil, herr
		}
		// The client answers before it switches, so the answer below waits
		// for the switch to show.
		for deadline := time.Now().Add(routedVerbTimeout); d.findTUIClient(sess.ID) == nil && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
	}
	return d.herdrWorkspaceInfo(cs, in.WorkspaceID)
}

// herdrAgentNameOK is herdr's rule for an agent name.
func herdrAgentNameOK(name string) bool {
	if name == "" || len(name) > 32 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// herdrAgentStart starts an agent in a pane that sits at its shell prompt,
// as herdr's agent.start does: the pane takes the agent's name, and the
// agent's command is typed at the prompt. tuios then finds the agent by its
// process, as for one a person starts. The caller needs what start-agent
// needs in the session, and the typing is held to send-text's checks.
func (d *Daemon) herdrAgentStart(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if !herdrAgentNameOK(in.Name) {
		return nil, herdrErr("invalid_agent_name", "agent name must start with a lowercase letter and contain only lowercase letters, digits, '-' or '_' (1-32 characters)")
	}
	harness, command := "", ""
	if reg := d.agentMatcher.registry; reg != nil {
		id, ok := integration.Canonical(in.Kind)
		if !ok {
			id = in.Kind
		}
		if m := reg.Lookup(id); m != nil {
			harness, command = m.ID, m.Command()
		} else if m, cmd, ok := reg.Resolve(in.Kind); ok {
			harness, command = m.ID, cmd
		}
	}
	if harness == "" || command == "" {
		return nil, herdrErr("unsupported_agent_kind", "unsupported interactive agent kind "+echoName(in.Kind))
	}
	for _, a := range in.Args {
		if strings.ContainsFunc(a, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return nil, herdrErr("invalid_agent_argument", "agent arguments cannot be encoded safely for the target shell")
		}
	}
	if in.TimeoutMS != nil && (*in.TimeoutMS <= 3000 || *in.TimeoutMS > 300000) {
		return nil, herdrErr("invalid_agent_timeout", "agent start timeout must be greater than 3000ms and at most 300000ms")
	}
	sess, win, ierr := d.herdrFindPane(in.PaneID)
	if ierr != nil {
		return nil, herdrErr("agent_pane_not_found", "agent target pane "+echoName(in.PaneID)+" not found")
	}
	paneID := herdrPaneID(sess.ID, win.ID)
	if herr := d.herdrAdmit(cs, "start-agent", sess); herr != nil {
		return nil, herr
	}
	for _, p := range d.buildHerdrView(cs).panes {
		if p.Agent != "" && p.Label == in.Name && p.PaneID != paneID {
			return nil, herdrErr("agent_name_taken", "agent name "+in.Name+" is already used; candidates: pane_id="+p.PaneID)
		}
	}
	pty := sess.GetPTY(win.PTYID)
	if pty == nil || pty.IsExited() {
		return nil, herdrErr("agent_pane_unavailable", "agent target pane "+paneID+" has no live terminal")
	}
	// A pane whose foreground the kernel does not report is refused too:
	// typing an agent's command into an unknown program is not safe.
	if (win.AgentHarness != "" && win.AgentState != AgentStateNone) || !shellAtPrompt(pty) {
		return nil, herdrErr("agent_pane_busy", "agent target pane "+paneID+" is not an available shell")
	}
	argv := append([]string{command}, in.Args...)
	shell := herdrPaneShell(pty.ShellPID())
	quoted := make([]string, len(argv))
	for i, a := range argv {
		q, ok := herdrShellQuote(shell, a)
		if !ok {
			return nil, herdrErr("invalid_agent_argument", "agent arguments cannot be encoded safely for the target shell "+echoName(shell)+". Pass plain arguments, or start the agent with tuios start-agent")
		}
		quoted[i] = q
	}
	// The name goes first, so the pane is found by it as soon as the agent
	// is. It is part of starting the agent, which the caller was admitted
	// for, so it is set as the daemon. A refused typing takes it back off.
	name := func(n string) *verbError {
		raw, _ := json.Marshal(herdrArgs{Session: sess.Name(), Window: win.ID, Name: &n})
		_, verr := d.verbSetWindow(nil, raw)
		return verr
	}
	if verr := name(in.Name); verr != nil {
		return nil, herdrFromVerb(verr, "agent_start_input_failed")
	}
	if _, herr := d.herdrSend(cs, paneID, strings.Join(quoted, " "), []string{"Enter"}, "agent_start_input_failed"); herr != nil {
		_ = name(win.CustomName)
		return nil, herr
	}
	_, w, pane, _, herr := d.herdrPaneView(cs, paneID)
	if herr != nil {
		return nil, herr
	}
	info := herdrAgentFromPane(*pane, &w)
	info.Name = in.Name
	if info.Agent == "" {
		info.Agent, info.AgentStatus = herdrAgentLabel(harness), "unknown"
	}
	return &herdrResult{Type: "agent_started", Agent: &info, Argv: argv}, nil
}

// herdrPaneShell is the name of the shell a pane runs: its process name,
// without the "-" of a login shell. "" when it cannot be read.
func herdrPaneShell(pid int) string {
	if pid <= 0 {
		return ""
	}
	info := readProcessInfo(pid)
	name := info.comm
	if len(info.argv) > 0 {
		name = filepath.Base(info.argv[0])
	}
	return strings.TrimPrefix(name, "-")
}

// herdrPlainWord reports whether s needs no quoting in any shell tuios
// types into: letters, digits and -_./:+@, and not a leading = (zsh
// expands =cmd to the path of cmd). A % is quoted, as fish expands %self.
func herdrPlainWord(s string) bool {
	return s != "" && s[0] != '=' && !strings.ContainsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=+@,", r))
	})
}

// herdrShellQuote quotes one word for the shell named, so the shell reads it
// back as the same single word. A POSIX shell (sh, bash, zsh and the rest)
// takes everything inside single quotes as it is, and a quote is closed,
// escaped and opened again. fish reads a backslash and a quote as escapes inside single
// quotes, so both are escaped. Any other shell gets plain words only: ok is
// false for a word that would need quoting there.
func herdrShellQuote(shell, s string) (string, bool) {
	if herdrPlainWord(s) {
		return s, true
	}
	switch shell {
	case "sh", "bash", "zsh", "dash", "ksh", "mksh", "ash", "yash", "posh":
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'", true
	case "fish":
		return "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(s) + "'", true
	}
	return "", false
}
