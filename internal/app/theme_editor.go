package app

import (
	"image/color"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	tint "github.com/lrstanley/bubbletint/v2"
)

// The theme editor: a side sheet that edits a theme file while the rail, the
// dock and the panes stay in view. It opens on the side with no rail and
// draws over the panes without moving them.
//
// Every change goes through a theme.Draft. A client alone in its process puts
// the draft in force under a scratch id, so the whole screen is the preview. A
// served client (tuios ssh, tuios-web) shares the theme with every client of
// its server, so it only measures the draft, and the sheet's swatches are the
// preview. Saving writes the sparse file through the draft and is refused on a
// client that may not write the config, like every writer.

// Tabs of the theme editor.
const (
	themeEditorTabRoles = iota
	themeEditorTabPane
	themeEditorTabCount
)

// themeEditorTabs are the tab labels, in tab order.
var themeEditorTabs = [themeEditorTabCount]string{"Roles", "Pane colours"}

// themeEditorRow is one row of a tab: a chrome role, or one of the pane
// colours, under a group heading.
type themeEditorRow struct {
	group string
	label string
	role  theme.Role
	// pane is the index into the draft's pane colours, or -1 for a role.
	pane int
	// fixed is a row that shows a value and takes no edit: the text inks,
	// which tuios measures from the surface.
	fixed bool
}

// themeEditorRoleRows are the Roles tab, grouped by what the user sees.
var themeEditorRoleRows = []themeEditorRow{
	{group: "Accents", label: "Accent", role: theme.RoleAccent, pane: -1},
	{group: "Accents", label: "Bright accent", role: theme.RoleAccentBright, pane: -1},
	{group: "Frame", label: "Unfocused border", role: theme.RoleBorderUnfocused, pane: -1},
	{group: "Frame", label: "Copy mode", role: theme.RoleBorderCopy, pane: -1},
	{group: "Frame", label: "Multifocus", role: theme.RoleBorderMultifocus, pane: -1},
	{group: "Frame", label: "Close button", role: theme.RoleDotClose, pane: -1},
	{group: "Frame", label: "Minimize button", role: theme.RoleDotMinimize, pane: -1},
	{group: "Frame", label: "Zoom button", role: theme.RoleDotZoom, pane: -1},
	{group: "Dock and rail", label: "Dock ground", role: theme.RoleDockGround, pane: -1},
	{group: "Dock and rail", label: "Rail ground", role: theme.RoleRailGround, pane: -1},
	{group: "Dock and rail", label: "Rule", role: theme.RoleRule, pane: -1},
	{group: "Dialogs", label: "Surface", role: theme.RoleSurface, pane: -1},
	{group: "Dialogs", label: "Canvas", role: theme.RoleCanvas, pane: -1},
	{group: "Dialogs", label: "Panel", role: theme.RolePanel, pane: -1},
	{group: "Dialogs", label: "Card", role: theme.RoleCard, pane: -1},
	{group: "Dialogs", label: "Text", role: theme.RoleText, pane: -1, fixed: true},
	{group: "Dialogs", label: "Dim text", role: theme.RoleDim, pane: -1, fixed: true},
	{group: "Dialogs", label: "Quiet text", role: theme.RoleMuted, pane: -1, fixed: true},
	{group: "Status", label: "Success", role: theme.RoleSuccess, pane: -1},
	{group: "Status", label: "Warning", role: theme.RoleWarning, pane: -1},
	{group: "Status", label: "Error", role: theme.RoleError, pane: -1},
	{group: "Status", label: "Info", role: theme.RoleInfo, pane: -1},
	{group: "Status", label: "Attention", role: theme.RoleAttention, pane: -1},
	{group: "Status", label: "Busy", role: theme.RoleBusy, pane: -1},
}

// themeEditorPaneRows are the Pane colours tab, in the draft's colour order.
var themeEditorPaneRows = func() []themeEditorRow {
	labels := []string{"Foreground", "Background", "Selection", "Cursor",
		"Black", "Red", "Green", "Yellow", "Blue", "Magenta", "Cyan", "White",
		"Bright black", "Bright red", "Bright green", "Bright yellow",
		"Bright blue", "Bright magenta", "Bright cyan", "Bright white"}
	rows := make([]themeEditorRow, len(labels))
	for i, l := range labels {
		group := "Terminal"
		if i >= 4 {
			group = "The sixteen"
		}
		rows[i] = themeEditorRow{group: group, label: l, pane: i}
	}
	return rows
}()

// themeEditorState is the editor's state, kept on the OS.
type themeEditorState struct {
	open bool
	// hidden takes the sheet off the screen and keeps the editor open, so the
	// whole screen shows the draft.
	hidden bool
	tab    int
	cursor [themeEditorTabCount]int
	scroll [themeEditorTabCount]int
	// draft is the theme being edited, saved what the file held when the
	// editor opened or last saved, and undo the drafts before each change.
	draft, saved *theme.Draft
	undo         []*theme.Draft
	// original is the theme in force when the editor opened, put back when
	// it closes.
	original string
	// live is whether this client previews by putting the draft in force.
	live bool
	// resolved is the draft merged over its base and the palette it draws,
	// rebuilt after each change. err is why the draft does not resolve.
	resolved *tint.Tint
	chrome   *theme.Chrome
	pal      overlay.Palette
	err      error
	// typing is the value being typed for the selected row, and typingOn
	// whether a value is being typed.
	typing   string
	typingOn bool
	// leaving is set by an esc over unsaved changes. A second esc drops them.
	leaving bool
}

// ThemeEditorOpen reports whether the theme editor is open.
func (m *OS) ThemeEditorOpen() bool { return m.themeEditor.open }

// ThemeEditorHidden reports whether the sheet is hidden with space.
func (m *OS) ThemeEditorHidden() bool { return m.themeEditor.hidden }

// OpenThemeEditor opens the editor on the theme id. With fresh set it starts
// a new theme on id instead, under the first free id made from it. original
// is the theme to put back when the editor closes; empty is the one in force.
func (m *OS) OpenThemeEditor(id string, fresh bool, original string) {
	if m.themeEditor.open || id == "" || id == themeNone {
		if id == themeNone {
			m.ShowNotification("Pick a theme to edit. The terminal's colours have no file", "info", m.Settings.NotificationDuration)
		}
		return
	}
	var d *theme.Draft
	var err error
	if fresh {
		d, err = theme.NewDraft(id)
	} else {
		d, err = theme.DraftOf(id)
	}
	if err != nil {
		m.ShowNotification("Cannot edit the theme: "+err.Error(), "error", m.Settings.NotificationWarningDuration)
		return
	}
	if original == "" {
		original = theme.CurrentThemeID()
		if original == "" {
			original = themeNone
		}
	}
	m.themeEditor = themeEditorState{
		open: true, draft: d, saved: d.Clone(), original: original, live: m.previewsLive(),
	}
	m.themeEditorChanged(true)
}

// CloseThemeEditor closes the editor and puts back the theme that was in
// force when it opened. Unsaved changes are dropped: the first esc over them
// asks, see ThemeEditorEscape.
func (m *OS) CloseThemeEditor() {
	st := &m.themeEditor
	if !st.open {
		return
	}
	id, live, original := st.draft.ID, st.live, st.original
	m.themeEditor = themeEditorState{}
	if live {
		m.applyTheme(original)
		theme.DropScratch(id)
	}
}

// ThemeEditorEscape is esc: it ends typing, or closes the editor. Over
// unsaved changes the first esc asks and the second drops them.
func (m *OS) ThemeEditorEscape() {
	st := &m.themeEditor
	switch {
	case st.typingOn:
		st.typingOn, st.typing = false, ""
	case st.hidden:
		st.hidden = false
	case m.themeEditorDirty() && !st.leaving:
		st.leaving = true
		m.ShowNotification("Your changes are not saved. Press esc again to drop them, or ctrl+s to save", "warning", m.Settings.NotificationWarningDuration)
	default:
		m.CloseThemeEditor()
	}
}

// themeEditorDirty reports whether the draft differs from what was saved.
func (m *OS) themeEditorDirty() bool {
	st := &m.themeEditor
	if st.draft == nil || st.saved == nil {
		return false
	}
	return *withoutDark(st.draft) != *withoutDark(st.saved) || !sameDark(st.draft.Dark, st.saved.Dark)
}

// withoutDark is a copy of d without its dark pointer, so two drafts compare
// by value.
func withoutDark(d *theme.Draft) *theme.Draft {
	c := *d
	c.Dark = nil
	return &c
}

// sameDark reports whether two dark flags say the same.
func sameDark(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// themeEditorRows are the rows of the open tab.
func (m *OS) themeEditorRows() []themeEditorRow {
	if m.themeEditor.tab == themeEditorTabPane {
		return themeEditorPaneRows
	}
	return themeEditorRoleRows
}

// ThemeEditorMove moves the cursor by delta rows on the open tab.
func (m *OS) ThemeEditorMove(delta int) {
	st := &m.themeEditor
	n := len(m.themeEditorRows())
	if n == 0 {
		return
	}
	st.leaving = false
	st.cursor[st.tab] = max(0, min(st.cursor[st.tab]+delta, n-1))
}

// ThemeEditorTab moves to the next tab, or the one before for a negative
// delta.
func (m *OS) ThemeEditorTab(delta int) {
	st := &m.themeEditor
	st.leaving, st.typingOn = false, false
	st.tab = (st.tab + delta + themeEditorTabCount) % themeEditorTabCount
}

// ThemeEditorToggleHidden takes the sheet off the screen, or puts it back.
func (m *OS) ThemeEditorToggleHidden() {
	m.themeEditor.hidden = !m.themeEditor.hidden
}

// themeEditorRowAt is the selected row of the open tab.
func (m *OS) themeEditorRowAt() (themeEditorRow, bool) {
	rows := m.themeEditorRows()
	i := m.themeEditor.cursor[m.themeEditor.tab]
	if i < 0 || i >= len(rows) {
		return themeEditorRow{}, false
	}
	return rows[i], true
}

// themeEditorRaw is what the draft's own file sets for row, "" for nothing.
func (st *themeEditorState) raw(row themeEditorRow) string {
	if row.pane >= 0 {
		return st.draft.Colours[row.pane]
	}
	if row.role.Settable() {
		return st.draft.Chrome[row.role]
	}
	return ""
}

// set records a value for row in the draft, with the draft before it on the
// undo stack. An empty value takes the base's again.
func (m *OS) themeEditorSet(row themeEditorRow, value string) {
	st := &m.themeEditor
	if row.fixed || st.raw(row) == value {
		return
	}
	st.undo = append(st.undo, st.draft.Clone())
	if len(st.undo) > themeEditorUndoMax {
		st.undo = st.undo[1:]
	}
	if row.pane >= 0 {
		st.draft.Colours[row.pane] = value
	} else {
		st.draft.Chrome[row.role] = value
	}
	st.leaving = false
	m.themeEditorChanged(row.pane >= 0)
}

// themeEditorUndoMax bounds the undo stack.
const themeEditorUndoMax = 200

// ThemeEditorUndo takes back the last change.
func (m *OS) ThemeEditorUndo() {
	st := &m.themeEditor
	if len(st.undo) == 0 {
		return
	}
	prev := st.undo[len(st.undo)-1]
	st.undo = st.undo[:len(st.undo)-1]
	pane := prev.Colours != st.draft.Colours || !sameDark(prev.Dark, st.draft.Dark)
	st.draft = prev
	m.themeEditorChanged(pane)
}

// ThemeEditorReset gives the selected row back to the base.
func (m *OS) ThemeEditorReset() {
	if row, ok := m.themeEditorRowAt(); ok {
		m.themeEditorSet(row, "")
	}
}

// themeEditorChanged resolves the draft again and, on a client that previews
// live, puts it in force. Only a change to the pane colours repaints the
// panes' emulators; a chrome change is the next frame's palette.
func (m *OS) themeEditorChanged(pane bool) {
	st := &m.themeEditor
	t, c, err := st.draft.Resolve()
	st.err = err
	if err != nil {
		return
	}
	st.resolved, st.chrome = t, c
	st.pal = theme.PaletteOf(t, c)
	if !st.live {
		return
	}
	if err := st.draft.Preview(); err != nil {
		st.err = err
		return
	}
	if pane {
		m.UpdateAllWindowThemes()
	}
	m.MarkAllDirty()
}

// themeEditorColour is the colour row draws in the draft, or nil for a
// ground the theme does not set.
func (m *OS) themeEditorColour(row themeEditorRow) color.Color {
	st := &m.themeEditor
	if st.resolved == nil {
		return nil
	}
	if row.pane >= 0 {
		cols := themeColourList(st.resolved)
		return theme.AsColor(cols[row.pane])
	}
	return theme.RoleColour(row.role, st.resolved, st.chrome, st.pal)
}

// themeColourList is a tint's colours in theme.ColourKeys order.
func themeColourList(t *tint.Tint) []*tint.Color {
	return []*tint.Color{
		t.Fg, t.Bg, t.SelectionBg, t.Cursor,
		t.Black, t.Red, t.Green, t.Yellow, t.Blue, t.Purple, t.Cyan, t.White,
		t.BrightBlack, t.BrightRed, t.BrightGreen, t.BrightYellow,
		t.BrightBlue, t.BrightPurple, t.BrightCyan, t.BrightWhite,
	}
}

// themeEditorFloor is the contrast row has to clear and the ground it is
// measured on, or 0 for a row with no floor.
func (m *OS) themeEditorFloor(row themeEditorRow) (float64, color.Color) {
	st := &m.themeEditor
	if st.resolved == nil {
		return 0, nil
	}
	if row.pane >= 0 {
		bg := theme.AsColor(st.resolved.Bg)
		switch {
		case row.pane == 0:
			return theme.ContrastFloor, bg
		case row.pane == 1 || row.pane == 2:
			return 0, nil
		}
		return theme.MarkFloor, bg
	}
	return theme.FloorOf(row.role, st.resolved, st.pal)
}

// themeEditorSource says where row's colour comes from: a key in the config
// that wins over the theme, the draft's own value, or the base.
func (m *OS) themeEditorSource(row themeEditorRow) string {
	if row.fixed {
		return "measured"
	}
	if m.themeEditorConfigWins(row) {
		return "set in config.toml"
	}
	raw := m.themeEditor.raw(row)
	switch {
	case raw == "":
		return "from base"
	case raw[0] == '#':
		return "custom"
	}
	if spec := theme.SpecOf(raw); spec.Role() != theme.RoleNone {
		return "role"
	}
	return "slot"
}

// themeEditorConfigWins reports whether a key in the config draws row's piece
// in place of the theme's colour.
func (m *OS) themeEditorConfigWins(row themeEditorRow) bool {
	if row.pane >= 0 || m.UserConfig == nil {
		return false
	}
	a := m.UserConfig.Appearance
	switch row.role {
	case theme.RoleBorderUnfocused:
		return a.BorderUnfocusedColor != ""
	case theme.RoleDockGround:
		return a.DockBackground != "" || (a.Background != "" && a.Background != "off")
	case theme.RoleRailGround:
		return a.Sidebar.Background != "" || (a.Background != "" && a.Background != "off")
	}
	return false
}

// ThemeEditorPick opens the colour picker on the selected row.
func (m *OS) ThemeEditorPick() {
	row, ok := m.themeEditorRowAt()
	if !ok || row.fixed {
		return
	}
	start := toRGBA(m.themeEditorColour(row))
	src := accentSourceOwn
	if m.themeEditor.raw(row) == "" {
		src = accentSourceDefault
	}
	m.openAccentPicker(AccentTargetThemeRole, m.themeEditorRowID(row), RGBAccent(start), src)
	if src == accentSourceDefault {
		m.AccentPicker.SrcWord = "from base"
	}
}

// themeEditorRowID names a row for the colour picker: the role's name, or
// pane: and the colour's key.
func (m *OS) themeEditorRowID(row themeEditorRow) string {
	if row.pane >= 0 {
		return "pane:" + theme.ColourKeys()[row.pane]
	}
	return row.role.String()
}

// themeEditorRowByID is the row the colour picker was opened on.
func (m *OS) themeEditorRowByID(id string) (themeEditorRow, bool) {
	if key, ok := strings.CutPrefix(id, "pane:"); ok {
		i := slices.Index(theme.ColourKeys(), key)
		if i < 0 {
			return themeEditorRow{}, false
		}
		return themeEditorPaneRows[i], true
	}
	for _, r := range themeEditorRoleRows {
		if r.role.String() == id {
			return r, true
		}
	}
	return themeEditorRow{}, false
}

// applyThemeRoleColor takes the colour picker's choice for a row. A slot
// chip is kept as the slot's name on a role, so the role follows the slot. A
// pane colour is always written as hex.
func (m *OS) applyThemeRoleColor(id string, sel Accent) {
	row, ok := m.themeEditorRowByID(id)
	if !ok || !m.themeEditor.open {
		return
	}
	value := sel.Hex()
	if sel.IsSlot() && row.pane < 0 {
		value = accentPayload(sel)
	}
	m.themeEditorSet(row, value)
}

// ThemeEditorStartTyping starts typing a value for the selected row.
func (m *OS) ThemeEditorStartTyping() {
	st := &m.themeEditor
	row, ok := m.themeEditorRowAt()
	if !ok || row.fixed {
		return
	}
	st.typingOn, st.typing = true, st.raw(row)
}

// ThemeEditorTyped edits the value being typed.
func (m *OS) ThemeEditorTyped(s string) { m.themeEditor.typing = s }

// ThemeEditorTyping is the value being typed, and whether one is.
func (m *OS) ThemeEditorTyping() (string, bool) {
	return m.themeEditor.typing, m.themeEditor.typingOn
}

// ThemeEditorComplete completes the value being typed to the next colour name
// that starts with it.
func (m *OS) ThemeEditorComplete() {
	st := &m.themeEditor
	prefix := strings.ToLower(st.typing)
	if prefix == "" || prefix[0] == '#' {
		return
	}
	for _, name := range theme.ColourNames() {
		if strings.HasPrefix(name, prefix) && name != prefix {
			st.typing = name
			return
		}
	}
}

// ThemeEditorCommitTyping takes the typed value: a pane colour takes
// #rrggbb, a role any colour value. An empty value resets the row.
func (m *OS) ThemeEditorCommitTyping() {
	st := &m.themeEditor
	row, ok := m.themeEditorRowAt()
	if !ok || !st.typingOn {
		return
	}
	value := strings.TrimSpace(st.typing)
	switch {
	case value == "":
	case row.pane >= 0:
		c, ok := overlay.ParseHex(value)
		if !ok {
			m.ShowNotification("A pane colour is #rrggbb. "+value+" is not", "warning", m.Settings.NotificationWarningDuration)
			return
		}
		value = overlay.Hex(c)
	default:
		if _, err := theme.ParseColor(value); err != nil {
			m.ShowNotification(err.Error(), "warning", m.Settings.NotificationWarningDuration)
			return
		}
		if r := theme.SpecOf(value).Role(); r != theme.RoleNone && !r.Settable() {
			m.ShowNotification("A theme cannot name "+value+". tuios measures the text inks", "warning", m.Settings.NotificationWarningDuration)
			return
		}
	}
	st.typingOn, st.typing = false, ""
	m.themeEditorSet(row, value)
}

// ThemeEditorSave writes the draft as a theme file. A client that may not
// write the config may not write a theme file either.
func (m *OS) ThemeEditorSave() tea.Cmd {
	st := &m.themeEditor
	if !st.open {
		return nil
	}
	if m.ConfigReadOnly {
		m.ShowNotification("This client cannot change the theme files", "info", m.Settings.NotificationDuration)
		return nil
	}
	path, err := st.draft.Save()
	if err != nil {
		m.ShowNotification("Theme not saved: "+err.Error(), "error", m.Settings.NotificationWarningDuration)
		return nil
	}
	st.saved, st.leaving = st.draft.Clone(), false
	m.ShowNotification("Saved "+path, "success", m.Settings.NotificationDuration)
	return nil
}
