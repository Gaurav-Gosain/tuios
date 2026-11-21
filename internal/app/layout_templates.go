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

	if tree := m.WorkspaceTrees[m.CurrentWorkspace]; tree != nil {
		tmpl.TilingScheme = tree.AutoScheme.String()
	}

	for _, w := range m.Windows {
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

func (m *OS) daemonLayouts() bool {
	return m.IsDaemonSession && m.DaemonClient != nil && m.SessionName != ""
}

func (m *OS) layoutTemplateCmd(action, name string) tea.Cmd {
	call := m.layoutTemplateCall()
	return func() tea.Msg {
		return LayoutTemplateDoneMsg{Action: action, Name: name, Err: call(action, name)}
	}
}

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

type layoutTemplatesListedMsg struct {
	items []LayoutTemplate
	err   error
	then  func([]LayoutTemplate) tea.Cmd
}

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

func (m *OS) fillLayoutPicker(ts []LayoutTemplate) tea.Cmd {
	m.LayoutPickerLoading = false
	if !m.ShowLayoutPicker || m.LayoutPickerMode != "load" {
		return nil
	}
	m.LayoutPickerItems = ts
	m.layoutList.Fit(len(m.layoutPickerRows()), listOverlayRows)
	return nil
}

func (m *OS) layoutPickerRows() []LayoutTemplate {
	return FilterLayoutTemplates(m.LayoutPickerItems, m.layoutList.Query.String())
}

func layoutPicker() *listPicker {
	return &listPicker{
		kind:  "layout",
		state: func(m *OS) *listnav.State { return &m.layoutList },
		count: func(m *OS) int { return len(m.layoutPickerRows()) },
		keys: func(m *OS, key string) (tea.Cmd, bool) {
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
	var existingWindows []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !isScratch(w) {
			existingWindows = append(existingWindows, w)
		}
	}

	var templateSlots []LayoutWindow
	for _, tw := range tmpl.Windows {
		if !tw.Minimized {
			templateSlots = append(templateSlots, tw)
		}
	}

	m.AutoTiling = false

	scaleX, scaleY := 1.0, 1.0
	if tmpl.ScreenWidth > 0 && tmpl.ScreenHeight > 0 {
		scaleX = float64(m.GetRenderWidth()) / float64(tmpl.ScreenWidth)
		scaleY = float64(m.GetRenderHeight()) / float64(tmpl.ScreenHeight)
	}

	refused := 0
	var first *terminal.Window

	for i, tw := range templateSlots {
		var win *terminal.Window
		dir := layoutLoadDir(tw.WorkingDir)
		if i < len(existingWindows) {
			win = existingWindows[i]
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
			title := tw.CustomName
			if title == "" {
				title = tw.Title
			}
			before := len(m.Windows)
			m.AddWindowIn(dir, title)
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

	for i := len(templateSlots); i < len(existingWindows); i++ {
		existingWindows[i].Minimized = true
	}

	m.AutoTiling = tmpl.AutoTiling
	if tmpl.MasterRatio > 0 {
		m.MasterRatio = tmpl.MasterRatio
	}

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
		m.ClampWindowsToView()
	}

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
			fmt.Fprintf(&sb, "RenameWindow %q\n", w.CustomName)
		}
		if dir := layoutLoadDir(w.WorkingDir); dir != "" {
			if line, ok := cdLine(dir); ok {
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
			fmt.Fprintf(&sb, "Run %q\n", cmd)
		}
		sb.WriteString("Sleep 200ms\n")
	}

	return sb.String()
}

func layoutPaneDir(w *terminal.Window) string {
	if w.ShellPgid <= 0 {
		return ""
	}
	dir, _ := terminal.ShellCWD(w.ShellPgid)
	return dir
}

func layoutLoadDir(dir string) string {
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	if strings.ContainsFunc(dir, func(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) }) {
		return ""
	}
	return dir
}

var errLayoutInScratch = errors.New("hide the scratch terminal first. A layout belongs to a workspace")
