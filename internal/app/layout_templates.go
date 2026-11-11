package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/layouttmpl"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

// LayoutTemplate is a saved layout, kept by internal/layouttmpl in
// ~/.config/tuios/layouts/.
//
// Integration points:
//   - Command palette: "Save Layout", "Load Layout"
//   - Keybinding: prefix+L l (load), prefix+L s (save)
//   - Tape scripting: SaveLayout/LoadLayout commands
//   - CLI: tuios layout list/delete/dir/export
type LayoutTemplate = layouttmpl.Template

// LayoutWindow is one pane of a template.
type LayoutWindow = layouttmpl.Window

// GetTemplatesDir returns the directory path for layout template files.
func GetTemplatesDir() string { return layouttmpl.Dir() }

// SaveLayoutTemplate saves the current workspace layout.
func SaveLayoutTemplate(name string, m *OS) error {
	// A layout is a workspace's, and a scratch group is not one: saved from
	// inside the box it would be empty, loaded there it would fill the box
	// with panes of the workspace's size.
	if m.InScratchView() {
		return errLayoutInScratch
	}
	if m.daemonLayouts() {
		m.queueTMLCmd(m.layoutTemplateCmd("save", name))
		return nil
	}
	tmpl := LayoutTemplate{
		Name:         name,
		CreatedAt:    time.Now(),
		Version:      2,
		AutoTiling:   m.AutoTiling,
		MasterRatio:  m.MasterRatio,
		ScreenWidth:  m.GetRenderWidth(),
		ScreenHeight: m.GetRenderHeight(),
	}

	// Save tiling scheme
	if tree := m.WorkspaceTrees[m.CurrentWorkspace]; tree != nil {
		tmpl.TilingScheme = tree.AutoScheme.String()
	}

	// Collect windows
	for _, w := range m.Windows {
		// The scratch terminal is not a pane of the layout. A template that
		// stored it would open it as an ordinary pane.
		if w.Workspace != m.CurrentWorkspace || isScratch(w) {
			continue
		}
		lw := LayoutWindow{
			X: w.X, Y: w.Y,
			Width: w.Width, Height: w.Height,
			Title:      w.Title(),
			CustomName: w.CustomName,
			Minimized:  w.Minimized,
		}

		// Where the pane is, so loading the template puts it back there.
		//
		// Through the platform's own reader rather than straight at /proc: this
		// read was Linux-only, so every layout saved on macOS recorded no
		// directory at all and every window it restored opened wherever the
		// daemon happened to be.
		if w.Terminal != nil {
			lw.WorkingDir = layoutPaneDir(w)
		}

		tmpl.Windows = append(tmpl.Windows, lw)
	}

	return layouttmpl.Save(tmpl)
}

// LayoutTemplateDoneMsg reports a layout save or load the daemon ran for
// this client.
type LayoutTemplateDoneMsg struct {
	Action, Name string
	Err          error
}

// daemonLayouts reports whether a save or a load goes to the daemon. The
// daemon writes and reads the files of its own machine and changes the
// session's state in one step, so every client of the session sees one
// layout, and an attach to another host uses that host's layouts.
func (m *OS) daemonLayouts() bool {
	return m.IsDaemonSession && m.DaemonClient != nil && m.SessionName != ""
}

// layoutTemplateCmd calls the layout-template verb off the Update goroutine,
// as sessionOrderCmd does. The state push that follows a load lays the
// panes out.
func (m *OS) layoutTemplateCmd(action, name string) tea.Cmd {
	call := m.layoutTemplateCall()
	return func() tea.Msg {
		return LayoutTemplateDoneMsg{Action: action, Name: name, Err: call(action, name)}
	}
}

// layoutTemplateCall returns a call of the layout-template verb for this
// client's session, which holds nothing of the model.
func (m *OS) layoutTemplateCall() func(action, name string) error {
	host, build, sess := m.AttachedHost, m.DaemonClient.ClientVersion(), m.SessionName
	return func(action, name string) error {
		c, err := dialSessionDaemon(host, build)
		if err != nil {
			return err
		}
		defer func() { _ = c.Close() }()
		_, err = c.Call("layout-template", map[string]any{"action": action, "session": sess, "name": name})
		return err
	}
}

// LayoutTemplateNow saves ("save") or loads ("load") the layout called name
// and returns when it is done: for a caller that answers only after the
// layout is written or applied, such as a tape command or the GUI bridge,
// which has no Bubble Tea loop to run a command on.
func (m *OS) LayoutTemplateNow(action, name string) error {
	if m.InScratchView() {
		return errLayoutInScratch
	}
	if m.daemonLayouts() {
		return m.layoutTemplateCall()(action, name)
	}
	switch action {
	case "save":
		return SaveLayoutTemplate(name, m)
	case "load":
		t, err := layouttmpl.Load(name)
		if err != nil {
			return errfmt.Errorf("layout template not found: %s", name)
		}
		ApplyLayoutTemplate(t, m)
		return nil
	}
	return errfmt.Errorf("%s is not a layout action", action)
}

// layoutTemplatesListedMsg carries the daemon's layouts to the callback a
// withLayoutTemplates call left.
type layoutTemplatesListedMsg struct {
	items []LayoutTemplate
	err   error
	then  func([]LayoutTemplate) tea.Cmd
}

// withLayoutTemplates runs then with the saved layouts, on the Update
// goroutine: at once from this machine's folder, or in a daemon session when
// the daemon's list arrives, so an attach to another host lists that host's
// layouts, the ones a load there reads.
func (m *OS) withLayoutTemplates(then func([]LayoutTemplate) tea.Cmd) tea.Cmd {
	if !m.daemonLayouts() {
		ts, _ := LoadLayoutTemplates()
		return then(ts)
	}
	host, build, sess := m.AttachedHost, m.DaemonClient.ClientVersion(), m.SessionName
	return func() tea.Msg {
		ts, err := listDaemonLayouts(host, build, sess)
		return layoutTemplatesListedMsg{items: ts, err: err, then: then}
	}
}

// listDaemonLayouts reads the layout-template list of the daemon that holds
// session sess. A row names a layout and how many panes it has; the panes
// themselves stay in the daemon's file, which a load reads by name.
func listDaemonLayouts(host, build, sess string) ([]LayoutTemplate, error) {
	c, err := dialSessionDaemon(host, build)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	raw, err := c.Call("layout-template", map[string]any{"action": "list", "session": sess})
	if err != nil {
		return nil, err
	}
	var res struct {
		Layouts []struct {
			Name      string `json:"name"`
			Panes     int    `json:"panes"`
			Mode      string `json:"mode"`
			Tiling    bool   `json:"tiling"`
			CreatedAt string `json:"created_at"`
			Version   int    `json:"version"`
		} `json:"layouts"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	out := make([]LayoutTemplate, 0, len(res.Layouts))
	for _, r := range res.Layouts {
		t := LayoutTemplate{
			Name: r.Name, Mode: r.Mode, AutoTiling: r.Tiling, Version: r.Version,
			Windows: make([]LayoutWindow, r.Panes),
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, r.CreatedAt)
		out = append(out, t)
	}
	return out, nil
}

// handleLayoutTemplatesListed hands the daemon's layouts to the caller that
// asked for them. A list that failed is shown, and the caller gets none.
func (m *OS) handleLayoutTemplatesListed(msg layoutTemplatesListedMsg) tea.Cmd {
	if msg.err != nil {
		m.ShowNotification("Failed to list layouts: "+msg.err.Error(), "error", m.Settings.NotificationDuration)
	}
	if msg.then == nil {
		return nil
	}
	return msg.then(msg.items)
}

// OpenLayoutPicker opens the layout picker on the saved layouts. In a daemon
// session the list arrives from the daemon; until then the picker says it
// is reading.
func (m *OS) OpenLayoutPicker() tea.Cmd {
	m.ShowLayoutPicker = true
	m.LayoutPickerMode = "load"
	m.LayoutPickerItems = nil
	m.layoutList.Reset()
	m.LayoutPickerLoading = true
	return m.withLayoutTemplates(m.fillLayoutPicker)
}

// fillLayoutPicker puts ts in the picker, if it is still open on the list.
func (m *OS) fillLayoutPicker(ts []LayoutTemplate) tea.Cmd {
	m.LayoutPickerLoading = false
	if !m.ShowLayoutPicker || m.LayoutPickerMode != "load" {
		return nil
	}
	m.LayoutPickerItems = ts
	m.layoutList.Fit(len(m.layoutPickerRows()), listOverlayRows)
	return nil
}

// layoutPickerRows is the saved layouts as the query filters them.
func (m *OS) layoutPickerRows() []LayoutTemplate {
	return FilterLayoutTemplates(m.LayoutPickerItems, m.layoutList.Query.String())
}

// layoutPicker is the layout picker's list behaviour, in its load mode. See
// list_picker.go. Its save mode is one name field (LayoutSaveKey).
func layoutPicker() *listPicker {
	return &listPicker{
		kind:  "layout",
		state: func(m *OS) *listnav.State { return &m.layoutList },
		count: func(m *OS) int { return len(m.layoutPickerRows()) },
		keys: func(m *OS, key string) (tea.Cmd, bool) {
			// Deleting a layout is behind a modifier, which is where the session
			// switcher and the keybind manager put theirs. A bare "d" would fire
			// in the state the picker opens in: typing "docs" to filter would
			// delete the selected layout and then filter on "ocs". A filter
			// keystroke must never destroy saved state.
			if key != "ctrl+d" {
				return nil, false
			}
			rows := m.layoutPickerRows()
			if c := m.layoutList.Cursor; c >= 0 && c < len(rows) {
				return m.DeleteLayoutFromPicker(rows[c].Name), true
			}
			return nil, true
		},
		accept: func(m *OS) tea.Cmd {
			m.layoutPickerActivate(m.layoutList.Cursor)
			return nil
		},
		close: func(m *OS) tea.Cmd {
			m.CloseLayoutPicker()
			return nil
		},
		clearKey: true,
	}
}

// CloseLayoutPicker puts the layout picker away, in either mode, and forgets
// what was typed.
func (m *OS) CloseLayoutPicker() {
	m.ShowLayoutPicker = false
	m.layoutList.Reset()
	m.layoutSaveName.Set("")
}

// OpenLayoutSave opens the layout picker on the name to save the layout
// under.
func (m *OS) OpenLayoutSave() {
	m.CloseLayoutPicker()
	m.ShowLayoutPicker = true
	m.LayoutPickerMode = "save"
}

// LayoutSaveKey reads a key press in the save mode: enter saves under the name,
// esc closes, and any other key edits the name.
func (m *OS) LayoutSaveKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc":
		m.CloseLayoutPicker()
	case "enter":
		name := m.layoutSaveName.String()
		if name == "" {
			m.ShowNotification("Layout name cannot be empty", "warning", m.Settings.NotificationDuration)
			return
		}
		if err := SaveLayoutTemplate(name, m); err != nil {
			m.ShowNotification("Failed to save layout: "+err.Error(), "error", m.Settings.NotificationDuration)
		} else {
			m.ShowNotification("Layout saved: "+name, "success", m.Settings.NotificationDuration)
		}
		m.CloseLayoutPicker()
	default:
		m.layoutSaveName.Edit(msg.String(), msg.Text, textedit.LineOpts{AllowClear: true})
	}
}

// LayoutSavePaste types a paste into the name the layout is saved under.
func (m *OS) LayoutSavePaste(s string) {
	m.layoutSaveName.Insert(s)
}

// DeleteLayoutFromPicker deletes the layout called name, from the daemon's
// machine in a daemon session, and fills the picker again.
func (m *OS) DeleteLayoutFromPicker(name string) tea.Cmd {
	done := func(err error) {
		if err != nil {
			m.ShowNotification("Failed to delete layout: "+err.Error(), "error", m.Settings.NotificationDuration)
			return
		}
		m.ShowNotification("Layout deleted: "+name, "info", m.Settings.NotificationDuration)
	}
	if !m.daemonLayouts() {
		done(DeleteLayoutTemplate(name))
		return m.withLayoutTemplates(m.fillLayoutPicker)
	}
	call := m.layoutTemplateCall()
	host, build, sess := m.AttachedHost, m.DaemonClient.ClientVersion(), m.SessionName
	return func() tea.Msg {
		err := call("delete", name)
		ts, lerr := listDaemonLayouts(host, build, sess)
		return layoutTemplatesListedMsg{items: ts, err: lerr, then: func(ts []LayoutTemplate) tea.Cmd {
			done(err)
			return m.fillLayoutPicker(ts)
		}}
	}
}

// handleLayoutTemplateDone shows a save or load the daemon refused.
func (m *OS) handleLayoutTemplateDone(msg LayoutTemplateDoneMsg) {
	if msg.Err == nil {
		return
	}
	m.ShowNotification(fmt.Sprintf("Failed to %s layout %s: %v", msg.Action, msg.Name, msg.Err), "error", m.Settings.NotificationDuration)
}

// LoadLayoutTemplates reads all templates from the layouts directory.
func LoadLayoutTemplates() ([]LayoutTemplate, error) { return layouttmpl.List() }

// ApplyLayoutTemplate recreates a workspace from a template.
func ApplyLayoutTemplate(tmpl LayoutTemplate, m *OS) {
	if m.InScratchView() {
		m.ShowNotification(errLayoutInScratch.Error(), "warning", m.Settings.NotificationDuration)
		return
	}
	if m.daemonLayouts() {
		m.queueTMLCmd(m.layoutTemplateCmd("load", tmpl.Name))
		return
	}
	// Collect existing windows in current workspace (reuse them instead of killing)
	var existingWindows []*terminal.Window
	for _, w := range m.Windows {
		// The scratch terminal keeps its popup. It never fills a slot.
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !isScratch(w) {
			existingWindows = append(existingWindows, w)
		}
	}

	// Count non-minimized template slots
	var templateSlots []LayoutWindow
	for _, tw := range tmpl.Windows {
		if !tw.Minimized {
			templateSlots = append(templateSlots, tw)
		}
	}

	// Disable auto-tiling during layout to prevent retiling
	m.AutoTiling = false

	// Scale factor for different screen sizes
	scaleX, scaleY := 1.0, 1.0
	if tmpl.ScreenWidth > 0 && tmpl.ScreenHeight > 0 {
		scaleX = float64(m.GetRenderWidth()) / float64(tmpl.ScreenWidth)
		scaleY = float64(m.GetRenderHeight()) / float64(tmpl.ScreenHeight)
	}

	// Panes whose folder tuios would not type a cd for. See cdLine.
	refused := 0
	// The first pane of the layout that is here now, which takes the focus.
	var first *terminal.Window

	// Assign existing windows to template slots
	for i, tw := range templateSlots {
		var win *terminal.Window
		dir := layoutLoadDir(tw.WorkingDir)
		if i < len(existingWindows) {
			// Reuse existing window
			win = existingWindows[i]
			// An existing pane is moved with a typed cd, and only when tuios
			// can see that its shell is at a prompt: into an editor or an agent
			// the line would be keys or a prompt. A pane it cannot see into (a
			// daemon pane, or a platform that does not say) stays where it is.
			if dir != "" {
				_, ok := cdLine(dir)
				_, idle := paneBusyReason(win)
				switch {
				case !ok:
					refused++
				case idle:
					m.cdAtPrompt(win, dir, true, "")
				}
			}
		} else {
			// Need more windows than we have, so create new ones.
			title := tw.CustomName
			if title == "" {
				title = tw.Title
			}
			before := len(m.Windows)
			// A new pane starts in the directory, so nothing is typed.
			m.AddWindowIn(dir, title)
			// In a daemon session the window does not exist yet: the daemon is
			// creating it and will push it back. Only take the new window when
			// one actually appeared, or this would grab the last existing window
			// and move it to the template slot meant for a window that is not
			// here yet.
			if len(m.Windows) > before {
				win = m.Windows[len(m.Windows)-1]
			}
		}
		if win == nil {
			continue
		}
		if first == nil {
			first = win
		}

		// Apply scaled positions
		win.X = int(float64(tw.X) * scaleX)
		win.Y = int(float64(tw.Y) * scaleY)
		win.Resize(
			max(int(float64(tw.Width)*scaleX), 10),
			max(int(float64(tw.Height)*scaleY), 5),
		)
		win.Minimized = false

		if tw.CustomName != "" {
			win.CustomName = tw.CustomName
		}

		// If template specifies a startup command, run it (only for newly created windows)
		if i >= len(existingWindows) && tw.Command != "" {
			cmd := tw.Command
			if len(tw.Args) > 0 {
				cmd += " " + strings.Join(tw.Args, " ")
			}
			if win.Pty != nil {
				_, _ = win.Pty.Write([]byte(cmd + "\n"))
			} else if win.DaemonWriteFunc != nil {
				_ = win.DaemonWriteFunc([]byte(cmd + "\n"))
			}
		}

		win.InvalidateCache()
	}

	if refused > 0 {
		m.ShowNotification(cdRefusedMessage, "warning", m.Settings.NotificationDuration)
	}

	// If we have MORE existing windows than template slots, minimize the extras
	for i := len(templateSlots); i < len(existingWindows); i++ {
		existingWindows[i].Minimized = true
	}

	// Restore tiling configuration
	m.AutoTiling = tmpl.AutoTiling
	if tmpl.MasterRatio > 0 {
		m.MasterRatio = tmpl.MasterRatio
	}

	// If tiled, rebuild BSP tree from the loaded window positions
	// instead of retiling (which would override the loaded layout)
	if m.AutoTiling {
		if m.UseScrollingLayout {
			m.TileAllWindows()
		} else {
			if tmpl.TilingScheme != "" {
				if tree := m.GetOrCreateBSPTree(); tree != nil {
					tree.AutoScheme = layout.ParseAutoScheme(tmpl.TilingScheme)
				}
			}
			m.RebuildBSPTreeFromPositions()
		}
	} else {
		// Always clamp windows after loading, to handle resolution differences.
		m.ClampWindowsToView()
	}

	// The layout's first pane takes the focus. Window 0 of the session can be
	// on another workspace, and focusing it switched there. In a daemon
	// session the new panes are not here yet, and the daemon gives the focus
	// to the panes it opens, so with no pane here the focus is left alone.
	if first != nil {
		if i := slices.Index(m.Windows, first); i >= 0 {
			m.FocusWindow(i)
		}
	}

	m.MarkAllDirty()
}

// DeleteLayoutTemplate removes a template file.
func DeleteLayoutTemplate(name string) error { return layouttmpl.Delete(name) }

// FilterLayoutTemplates filters by name substring.
func FilterLayoutTemplates(templates []LayoutTemplate, query string) []LayoutTemplate {
	return layouttmpl.Filter(templates, query)
}

// GenerateTapeScript converts a layout template to a tape script that
// recreates the layout. This enables layout templates to be shared,
// version-controlled, and executed in CI/headless environments.
func GenerateTapeScript(tmpl LayoutTemplate) string {
	var sb strings.Builder
	sb.WriteString("# Auto-generated layout script: ")
	sb.WriteString(tmpl.Name)
	sb.WriteString("\n")
	sb.WriteString("# Created: ")
	sb.WriteString(tmpl.CreatedAt.Format(time.RFC3339))
	sb.WriteString("\n\n")

	if tmpl.AutoTiling {
		sb.WriteString("EnableTiling\n")
	} else {
		sb.WriteString("DisableTiling\n")
	}

	for i, w := range tmpl.Windows {
		if i > 0 {
			sb.WriteString("NewWindow\n")
		}
		if w.CustomName != "" {
			// Quoted, because a name may hold spaces and the tape lexer splits an
			// unquoted argument on them: an unquoted "my project" replayed as "my".
			fmt.Fprintf(&sb, "RenameWindow %q\n", w.CustomName)
		}
		if dir := layoutLoadDir(w.WorkingDir); dir != "" {
			if line, ok := cdLine(dir); ok {
				// Quoted twice: for the shell that runs the cd, then for the
				// tape lexer that reads the Type line.
				fmt.Fprintf(&sb, "Type %q\nEnter\n", line)
			} else {
				sb.WriteString("# No cd: the folder name holds a quote, a backslash or a control character.\n")
			}
		}
		if w.Command != "" {
			cmd := w.Command
			if len(w.Args) > 0 {
				cmd += " " + strings.Join(w.Args, " ")
			}
			// Quoted: Type takes a string, and an unquoted command line did
			// not parse, so an exported layout with a command never played.
			fmt.Fprintf(&sb, "Run %q\n", cmd)
		}
		sb.WriteString("Sleep 200ms\n")
	}

	return sb.String()
}

// layoutPaneDir is the directory a saved layout records for a pane.
//
// It is the directory the kernel reports for the pane's shell. The pane's
// OSC 7 announcement is any text a program printed, and where the kernel
// cannot be asked (no shell pid, a pane on another machine, a platform with
// no answer) nothing checks it, so no directory is recorded at all.
func layoutPaneDir(w *terminal.Window) string {
	if w.ShellPgid <= 0 {
		return ""
	}
	dir, _ := terminal.ShellCWD(w.ShellPgid)
	return dir
}

// layoutLoadDir is the directory a layout entry may move a pane to, or "".
// A template is a file anyone can edit, and an older tuios saved whatever a
// pane announced, so the path is checked again when it is used: it must be
// absolute and hold no control characters.
func layoutLoadDir(dir string) string {
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	if strings.ContainsFunc(dir, func(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) }) {
		return ""
	}
	return dir
}

// errLayoutInScratch refuses a layout save or load inside a scratch group.
var errLayoutInScratch = errors.New("hide the scratch terminal first. A layout belongs to a workspace")
