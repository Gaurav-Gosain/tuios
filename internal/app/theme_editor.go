package app

import (
	"errors"
	"image/color"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	tint "github.com/lrstanley/bubbletint/v2"
)

const (
	themeEditorTabQuick = iota
	themeEditorTabRoles
	themeEditorTabPane
	themeEditorTabCount
)

var themeEditorTabs = [themeEditorTabCount]string{"Quick", "Roles", "Pane colours"}

type themeEditorRow struct {
	group string
	label string
	role  theme.Role
	pane  int
	fixed bool
	dark  bool
}

var themeEditorQuickRows = []themeEditorRow{
	{group: "Seeds", label: "Accent", role: theme.RoleAccent, pane: -1},
	{group: "Seeds", label: "Surface", role: theme.RoleSurface, pane: -1},
	{group: "Shade", label: "Dark theme", pane: -1, dark: true},
}

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

const themeEditorPaneSelection = 2

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

type themeEditorState struct {
	open         bool
	hidden       bool
	tab          int
	lists        [themeEditorTabCount]listnav.State
	draft, saved *theme.Draft
	undo         []*theme.Draft
	original     string
	live         bool
	resolved     *tint.Tint
	chrome       *theme.Chrome
	pal          overlay.Palette
	err          error
	typing       textedit.Line
	typingOn     bool
	leaving      bool
	overwrite    bool
	flashSeq     int
}

// ThemeEditorOpen reports whether the theme editor is open.
func (m *OS) ThemeEditorOpen() bool { return m.themeEditor.open }

// ThemeEditorDirty reports whether the open editor holds a change that is not
// saved. The Learn tour reports it, so a lesson can see a colour change.
func (m *OS) ThemeEditorDirty() bool { return m.themeEditor.open && m.themeEditorDirty() }

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
	if !fresh {
		m.themeEditor.tab = themeEditorTabRoles
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
		m.followSchemeTheme()
	}
}

// ThemeEditorEscape is esc: it ends typing, or closes the editor. Over
// unsaved changes the first esc asks and the second drops them.
func (m *OS) ThemeEditorEscape() {
	st := &m.themeEditor
	switch {
	case st.typingOn:
		st.typingOn = false
		st.typing.Set("")
	case st.hidden:
		st.hidden = false
	case m.themeEditorDirty() && !st.leaving:
		st.leaving = true
		m.ShowNotification("Your changes are not saved. Press esc again to drop them, or ctrl+s to save", "warning", m.Settings.NotificationWarningDuration)
	default:
		m.CloseThemeEditor()
	}
}

func (m *OS) themeEditorDirty() bool {
	st := &m.themeEditor
	if st.draft == nil || st.saved == nil {
		return false
	}
	return *withoutDark(st.draft) != *withoutDark(st.saved) || !sameDark(st.draft.Dark, st.saved.Dark)
}

func withoutDark(d *theme.Draft) *theme.Draft {
	c := *d
	c.Dark = nil
	return &c
}

func sameDark(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (m *OS) themeEditorRows() []themeEditorRow {
	switch m.themeEditor.tab {
	case themeEditorTabQuick:
		return themeEditorQuickRows
	case themeEditorTabPane:
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
	st.lists[st.tab].Cursor = m.listStep(st.lists[st.tab].Cursor, delta, n)
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

func (m *OS) themeEditorRowAt() (themeEditorRow, bool) {
	rows := m.themeEditorRows()
	i := m.themeEditor.lists[m.themeEditor.tab].Cursor
	if i < 0 || i >= len(rows) {
		return themeEditorRow{}, false
	}
	return rows[i], true
}

func (st *themeEditorState) raw(row themeEditorRow) string {
	if row.dark {
		if st.draft.Dark == nil {
			return ""
		}
		if *st.draft.Dark {
			return "yes"
		}
		return "no"
	}
	if row.pane >= 0 {
		return st.draft.Colours[row.pane]
	}
	if row.role.Settable() {
		return st.draft.Chrome[row.role]
	}
	return ""
}

func (m *OS) themeEditorSet(row themeEditorRow, value string) {
	st := &m.themeEditor
	if row.fixed || st.raw(row) == value {
		return
	}
	st.undo = append(st.undo, st.draft.Clone())
	if len(st.undo) > themeEditorUndoMax {
		st.undo = st.undo[1:]
	}
	switch {
	case row.dark:
		st.draft.Dark = nil
		if value != "" {
			dark := value == "yes"
			st.draft.Dark = &dark
		}
	case row.pane >= 0:
		st.draft.Colours[row.pane] = value
	default:
		st.draft.Chrome[row.role] = value
	}
	st.leaving = false
	m.themeEditorChanged(row.pane >= 0 || row.dark)
}

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

func (m *OS) themeEditorColour(row themeEditorRow) color.Color {
	st := &m.themeEditor
	if st.resolved == nil {
		return nil
	}
	if row.pane >= 0 {
		c := theme.AsColor(theme.ColourOf(st.resolved, row.pane))
		if c == nil && row.pane == themeEditorPaneSelection {
			return configInk(m.Settings.SelectionBg)
		}
		return c
	}
	if row.role == theme.RoleDockGround || row.role == theme.RoleRailGround {
		return m.themeEditorGround(row)
	}
	return theme.RoleColour(row.role, st.resolved, st.chrome, st.pal)
}

func (m *OS) themeEditorGround(row themeEditorRow) color.Color {
	st := &m.themeEditor
	if m.themeEditorConfigWins(row) {
		which := surfaceDock
		if row.role == theme.RoleRailGround {
			which = surfaceSidebar
		}
		if g := m.surfaceGround(which); g.on() {
			return g.bg
		}
	} else if c := st.chrome.Furniture(row.role); c != nil {
		return c
	}
	if m.host.bg != nil {
		return m.host.bg
	}
	return theme.AsColor(st.resolved.Bg)
}

func (m *OS) themeEditorFloor(row themeEditorRow) (float64, color.Color) {
	st := &m.themeEditor
	if st.resolved == nil || theme.Depth() == overlay.Depth16 {
		return 0, nil
	}
	if row.pane >= 0 {
		bg := theme.AsColor(st.resolved.Bg)
		switch row.pane {
		case 0:
			return theme.ContrastFloor, bg
		case 1, 2:
			return 0, nil
		case 4, 11:
			return 0, nil
		}
		return theme.MarkFloor, bg
	}
	floor, ground := theme.FloorOf(row.role, st.resolved, st.pal)
	if ground == nil || overlay.IsNoColor(ground) {
		return 0, nil
	}
	return floor, ground
}

func themeEditorRatio(c, ground color.Color) float64 {
	return overlay.ContrastRatio(overlay.Shown(c), overlay.Shown(ground))
}

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
	if row.dark {
		value := "yes"
		if m.themeEditor.resolved != nil && m.themeEditor.resolved.Dark {
			value = "no"
		}
		m.themeEditorSet(row, value)
		return
	}
	c := m.themeEditorColour(row)
	if overlay.IsNoColor(c) {
		m.openAccentPicker(AccentTargetThemeRole, m.themeEditorRowID(row), Accent{}, accentSourceNone)
		return
	}
	src := accentSourceOwn
	if m.themeEditor.raw(row) == "" {
		src = accentSourceDefault
	}
	m.openAccentPicker(AccentTargetThemeRole, m.themeEditorRowID(row), RGBAccent(toRGBA(c)), src)
	if src == accentSourceDefault {
		m.AccentPicker.SrcWord = m.themeEditorSource(row)
	}
}

func (m *OS) themeEditorRowID(row themeEditorRow) string {
	if row.pane >= 0 {
		return "pane:" + theme.ColourKeys()[row.pane]
	}
	return row.role.String()
}

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
	if !ok || row.fixed || row.dark {
		return
	}
	st.typingOn = true
	st.typing.Set(st.raw(row))
}

// ThemeEditorTyping is the value being typed, and whether one is.
func (m *OS) ThemeEditorTyping() (string, bool) {
	return m.themeEditor.typing.String(), m.themeEditor.typingOn
}

// ThemeEditorComplete completes the value being typed to the next colour name
// that starts with it.
func (m *OS) ThemeEditorComplete() {
	st := &m.themeEditor
	prefix := strings.ToLower(st.typing.String())
	if prefix == "" || prefix[0] == '#' {
		return
	}
	for _, name := range theme.ColourNames() {
		if strings.HasPrefix(name, prefix) && name != prefix {
			st.typing.Set(name)
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
	value := strings.TrimSpace(st.typing.String())
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
		trial := st.draft.Clone()
		trial.Chrome[row.role] = value
		if _, _, err := trial.Resolve(); err != nil {
			m.ShowNotification(err.Error(), "warning", m.Settings.NotificationWarningDuration)
			return
		}
	}
	st.typingOn = false
	st.typing.Set("")
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
	save := st.draft.Save
	if st.overwrite {
		save = st.draft.SaveOver
	}
	path, err := save()
	if errors.Is(err, theme.ErrChangedOnDisk) {
		st.overwrite = true
		m.ShowNotification("The theme file changed on disk. Press ctrl+s again to replace it", "warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	st.overwrite = false
	if err != nil {
		m.ShowNotification("Theme not saved: "+err.Error(), "error", m.Settings.NotificationWarningDuration)
		return nil
	}
	st.saved, st.leaving = st.draft.Clone(), false
	m.ShowNotification("Saved "+path, "success", m.Settings.NotificationDuration)
	return nil
}

// ThemeEditorFix moves the selected row's colour, in lightness only, until it
// clears its contrast floor on its ground.
func (m *OS) ThemeEditorFix() {
	row, ok := m.themeEditorRowAt()
	if !ok || row.fixed || row.dark {
		return
	}
	if theme.Depth() == overlay.Depth16 {
		m.ShowNotification("This terminal shows 16 colours. Its own palette decides the contrast", "info", m.Settings.NotificationDuration)
		return
	}
	floor, ground := m.themeEditorFloor(row)
	c := m.themeEditorColour(row)
	if floor <= 0 || ground == nil || c == nil || overlay.IsNoColor(c) {
		return
	}
	if themeEditorRatio(c, ground) >= floor {
		m.ShowNotification(row.label+" already reads on its ground", "info", m.Settings.NotificationDuration)
		return
	}
	fixed := overlay.FixLightness(c, ground, floor)
	m.themeEditorSet(row, overlay.Hex(toRGBA(fixed)))
}

const themeEditorFlashFor = 400 * time.Millisecond

type themeEditorFlashEndMsg struct{ seq int }

// ThemeEditorFlash shows the selected role in its inverse for a moment, on a
// client that previews live: every piece of chrome in that role blinks, at
// no cost to the render path. It is the same pointer swap an edit is.
func (m *OS) ThemeEditorFlash() tea.Cmd {
	st := &m.themeEditor
	row, ok := m.themeEditorRowAt()
	if !st.live || !ok || row.fixed || row.dark || row.pane >= 0 || st.err != nil {
		return nil
	}
	c := m.themeEditorColour(row)
	if c == nil || overlay.IsNoColor(c) {
		return nil
	}
	rgb := toRGBA(c)
	flash := st.draft.Clone()
	flash.Chrome[row.role] = overlay.Hex(color.RGBA{R: 255 - rgb.R, G: 255 - rgb.G, B: 255 - rgb.B, A: 0xff})
	if flash.Preview() != nil {
		return nil
	}
	m.MarkAllDirty()
	st.flashSeq++
	seq := st.flashSeq
	return tea.Tick(themeEditorFlashFor, func(time.Time) tea.Msg { return themeEditorFlashEndMsg{seq: seq} })
}

func (m *OS) endThemeEditorFlash(msg themeEditorFlashEndMsg) {
	st := &m.themeEditor
	if !st.open || !st.live || msg.seq != st.flashSeq {
		return
	}
	if st.draft.Preview() == nil {
		m.MarkAllDirty()
	}
}

// ThemeEditorTypeKey applies one key press to the value being typed (see
// lineKey).
func (m *OS) ThemeEditorTypeKey(key, text string) {
	if m.themeEditor.typingOn {
		lineKey(&m.themeEditor.typing, key, text, cleanControl)
	}
}

// ThemeEditorTypePaste types a paste at the value's caret.
func (m *OS) ThemeEditorTypePaste(s string) {
	if m.themeEditor.typingOn {
		lineInsert(&m.themeEditor.typing, s, cleanControl)
	}
}
