package app

import (
	"errors"
	"image/color"
	"slices"
	"strings"
	"time"

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
	themeEditorTabQuick = iota
	themeEditorTabRoles
	themeEditorTabPane
	themeEditorTabCount
)

// themeEditorTabs are the tab labels, in tab order.
var themeEditorTabs = [themeEditorTabCount]string{"Quick", "Roles", "Pane colours"}

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
	// dark is the row that says whether the theme is dark: enter toggles it.
	dark bool
}

// themeEditorQuickRows are the Quick tab: two seeds and the shade. The rest of
// the chrome derives from them: the ramp and the text inks from the surface,
// the pills and marks from the accent and the sixteen.
var themeEditorQuickRows = []themeEditorRow{
	{group: "Seeds", label: "Accent", role: theme.RoleAccent, pane: -1},
	{group: "Seeds", label: "Surface", role: theme.RoleSurface, pane: -1},
	{group: "Shade", label: "Dark theme", pane: -1, dark: true},
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
	// overwrite is set when a save found the file changed on disk. The next
	// ctrl+s replaces it.
	overwrite bool
	// flashSeq numbers the role flashes, so only the last one's end puts
	// the draft back.
	flashSeq int
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
		// An existing theme opens on the roles, where most edits are. A new
		// one opens on the two seeds.
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
		// A light and dark switch while the editor was open was held back.
		m.followSchemeTheme()
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
		return theme.AsColor(theme.ColourOf(st.resolved, row.pane))
	}
	return theme.RoleColour(row.role, st.resolved, st.chrome, st.pal)
}

// themeEditorFloor is the contrast row has to clear and the ground it is
// measured on, or 0 for a row with no floor.
func (m *OS) themeEditorFloor(row themeEditorRow) (float64, color.Color) {
	st := &m.themeEditor
	// At 16 colours the frame writer steps each colour down to a slot whose
	// RGB only the terminal knows, so there is no ratio to show or to fix.
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
			// Black and white: one of the two is meant to sit near the
			// ground, so a floor would only ask for a broken palette.
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

// themeEditorRatio is the contrast of c on ground as the terminal shows
// them: at 256 colours, the palette entries the frame writer picks.
func themeEditorRatio(c, ground color.Color) float64 {
	return overlay.ContrastRatio(overlay.Shown(c), overlay.Shown(ground))
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
	if row.dark {
		value := "yes"
		if m.themeEditor.resolved != nil && m.themeEditor.resolved.Dark {
			value = "no"
		}
		m.themeEditorSet(row, value)
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
	if !ok || row.fixed || row.dark {
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
		// A role that names itself, or two that name each other, parses
		// and does not resolve. Refuse it here, while the value is still
		// in the field to correct.
		trial := st.draft.Clone()
		trial.Chrome[row.role] = value
		if _, _, err := trial.Resolve(); err != nil {
			m.ShowNotification(err.Error(), "warning", m.Settings.NotificationWarningDuration)
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
	save := st.draft.Save
	if st.overwrite {
		save = st.draft.SaveOver
	}
	path, err := save()
	if errors.Is(err, theme.ErrChangedOnDisk) {
		// Someone else wrote the file since the editor read it. Their
		// change is lost on a write, so the second ctrl+s decides.
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

// themeEditorFlashFor is how long a role shows inverted after the cursor
// lands on it, so every piece of the screen in that role blinks once.
const themeEditorFlashFor = 400 * time.Millisecond

// themeEditorFlashEndMsg ends the flash numbered seq.
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

// endThemeEditorFlash puts the draft back after a flash, unless a later
// flash or a closed editor made it stale.
func (m *OS) endThemeEditorFlash(msg themeEditorFlashEndMsg) {
	st := &m.themeEditor
	if !st.open || !st.live || msg.seq != st.flashSeq {
		return
	}
	if st.draft.Preview() == nil {
		m.MarkAllDirty()
	}
}
