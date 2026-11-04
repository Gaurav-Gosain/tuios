package main

import (
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/adrg/xdg"
	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// tuios keys: the TUI's prefix key and its menus, in guios. The keymap is
// tuios's own: config.toml read through internal/config, the registry and
// the which-key tables the TUI reads, so a key the user rebinds there is
// rebound here too. An action runs on guios's own path where it has one
// (split, close, focus, workspaces), else as `tuios run-command` on the
// session, which the TUI runs when one shows the session.
//
// A key reaches the state machine two ways. Keys with a modifier, and keys
// that type no text (Escape, Tab, the arrows), come as key presses through
// the terminal's OnKey, on the main thread. Keys that type text come on
// Linux as text alone, which OnKey never sees: the terminal writes it to
// its connection, and gated.Write hands it here while the prefix is armed.
// Both paths step the same locked state, in the order the keys came.

// repeatWindow is how long the prefix stays armed after an action that
// repeats, as the TUI's prefix_repeat_time default.
const repeatWindow = config.PrefixRepeatTimeDefault * time.Millisecond

// whichKeyDelay is how long after the prefix the menu shows, as the TUI's
// config.WhichKeyDelay: a person who knows the keys never sees it.
const whichKeyDelay = config.WhichKeyDelay

// tuiosKeys is the prefix's state and the keymap it reads.
type tuiosKeys struct {
	mu       sync.Mutex
	reg      *config.KeybindRegistry
	leader   string // the leader key as the registry spells keys, "ctrl+b"
	literal  []byte // what the leader sends to a program, 0x02 for ctrl+b
	whichKey bool
	path     string // config.toml, and its time when it was read
	modTime  time.Time
	// palette are the command palette's keys (palette_keys.go).
	palette []keyChord

	armed atomic.Bool // read by the terminals' writers
	// repeat is when the repeat window of an action that stays armed ends
	// (Unix ns), 0 outside one: until then only such actions take keys.
	repeat atomic.Int64
	menu   string    // "" the leader's menu, else a sub-prefix's
	since  time.Time // when the prefix was pressed
	pane   string    // the pane whose terminal took the leader, "" none
}

// loadKeys reads tuios's keymap, again only when config.toml changed. It
// runs as the app starts, as its window takes the focus and as the prefix
// is pressed, so a rebind applies with no watcher. Call it on the main
// thread.
func (a *App) loadKeys() {
	tk := &a.tk
	path, _ := xdg.SearchConfigFile("tuios/config.toml")
	var mod time.Time
	if fi, err := os.Stat(path); err == nil {
		mod = fi.ModTime()
	}
	if tk.reg != nil && path == tk.path && mod.Equal(tk.modTime) {
		return
	}
	cfg, err := config.LoadUserConfig()
	if err != nil || cfg == nil {
		cfg = config.DefaultConfig()
	}
	leader := cfg.Keybindings.LeaderKey
	if leader == "" {
		leader = "ctrl+b"
	}
	tk.reg = config.NewKeybindRegistry(cfg)
	tk.leader = normKey(leader)
	tk.literal = keyBytes(tk.leader)
	tk.whichKey = cfg.Appearance.WhichKeyEnabled == nil || *cfg.Appearance.WhichKeyEnabled
	tk.palette = append(paletteChords(cfg.Keybindings.Global["command_palette"], false),
		paletteChords(cfg.Keybindings.GUI["command_palette"], true)...)
	tk.path, tk.modTime = path, mod
}

// normKey spells a key as the registry compares it: a single character as
// it is, a chord in lower case.
func normKey(k string) string {
	k = strings.TrimSpace(k)
	if utf8.RuneCountInString(k) == 1 {
		return k
	}
	return strings.ToLower(k)
}

// keyBytes is what a key sends to a program: a Control chord its control
// byte, an Alt chord Escape then the key.
func keyBytes(k string) []byte {
	switch {
	case strings.HasPrefix(k, "ctrl+"):
		rest := strings.TrimPrefix(k, "ctrl+")
		switch rest {
		case "space", "@", "2":
			return []byte{0}
		case "[":
			return []byte{0x1b}
		case "\\":
			return []byte{0x1c}
		case "]":
			return []byte{0x1d}
		}
		if len(rest) == 1 && rest[0] >= 'a' && rest[0] <= 'z' {
			return []byte{rest[0] & 0x1f}
		}
	case strings.HasPrefix(k, "alt+"):
		if b := keyBytes(strings.TrimPrefix(k, "alt+")); b != nil {
			return append([]byte{0x1b}, b...)
		}
	case utf8.RuneCountInString(k) == 1:
		return []byte(k)
	}
	return nil
}

// keyNamesTK are the names tuios gives the keys that type no text.
var keyNamesTK = map[ui.Key]string{
	ui.KeyEnter: "enter", ui.KeyEscape: "esc", ui.KeyBackspace: "backspace", ui.KeyTab: "tab",
	ui.KeySpace: "space", ui.KeyDelete: "delete", ui.KeyInsert: "insert", ui.KeyHome: "home",
	ui.KeyEnd: "end", ui.KeyPageUp: "pgup", ui.KeyPageDown: "pgdown", ui.KeyLeft: "left",
	ui.KeyRight: "right", ui.KeyUp: "up", ui.KeyDown: "down",
}

// usChars are the characters of the keys that type them on a US layout,
// unshifted and shifted, for a key press that comes without its text.
var usChars = map[ui.Key][2]string{
	ui.KeyMinus: {"-", "_"}, ui.KeyEqual: {"=", "+"}, ui.KeyComma: {",", "<"}, ui.KeyPeriod: {".", ">"},
	ui.KeySlash: {"/", "?"}, ui.KeySemicolon: {";", ":"}, ui.KeyQuote: {"'", "\""},
	ui.KeyBracketLeft: {"[", "{"}, ui.KeyBracketRight: {"]", "}"}, ui.KeyBackslash: {"\\", "|"},
	ui.KeyBackquote: {"`", "~"}, ui.Key0: {"0", ")"}, ui.Key1: {"1", "!"}, ui.Key2: {"2", "@"},
	ui.Key3: {"3", "#"}, ui.Key4: {"4", "$"}, ui.Key5: {"5", "%"}, ui.Key6: {"6", "^"},
	ui.Key7: {"7", "&"}, ui.Key8: {"8", "*"}, ui.Key9: {"9", "("},
}

// keyString spells a key press as tuios's keymap does: "ctrl+b", "R",
// "shift+tab", "|". It reports false for a key it has no name for.
func keyString(mods ui.Modifiers, key ui.Key) (string, bool) {
	var base string
	shift := mods&ui.Shift != 0
	chord := mods&(ui.Ctrl|ui.Alt|ui.Super) != 0
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		base = string(rune('a' + int(key-ui.KeyA)))
		if shift && !chord {
			return strings.ToUpper(base), true
		}
	case usChars[key] != [2]string{}:
		c := usChars[key]
		if shift && !chord {
			return c[1], true
		}
		base = c[0]
	case keyNamesTK[key] != "":
		base = keyNamesTK[key]
	case key >= ui.KeyF1 && key <= ui.KeyF12:
		base = "f" + strconv.Itoa(int(key-ui.KeyF1)+1)
	default:
		return "", false
	}
	var b strings.Builder
	if mods&ui.Ctrl != 0 {
		b.WriteString("ctrl+")
	}
	if mods&ui.Alt != 0 {
		b.WriteString("alt+")
	}
	if shift {
		b.WriteString("shift+")
	}
	if mods&ui.Super != 0 {
		b.WriteString("super+")
	}
	b.WriteString(base)
	return b.String(), true
}

// typesText reports whether a key press, with these modifiers, types text
// that the terminal writes, which then comes to take: a printable key with
// Shift at most.
func typesText(mods ui.Modifiers, key ui.Key) bool {
	if mods&^ui.Shift != 0 {
		return false
	}
	return key >= ui.KeyA && key <= ui.KeyZ || usChars[key] != [2]string{}
}

// typedKey reads the bytes a terminal writes for one key that typed text,
// as the keymap spells it.
func typedKey(p []byte) (string, bool) {
	r, n := utf8.DecodeRune(p)
	if n != len(p) || r == utf8.RuneError {
		return "", false
	}
	switch {
	case r == ' ':
		return "space", true
	case r < 0x20 || r == 0x7f:
		return "", false
	}
	return string(r), true
}

// live reports whether the prefix takes keys: armed, and inside the
// repeat window when in one.
func (tk *tuiosKeys) live() bool {
	if !tk.armed.Load() {
		return false
	}
	if r := tk.repeat.Load(); r != 0 && time.Now().UnixNano() > r {
		tk.armed.Store(false)
		tk.repeat.Store(0)
		return false
	}
	return true
}

// keysOn reports whether tuios keys work: the setting, and a keymap read.
func (a *App) keysOn() bool { return a.set.TuiosKeys }

// keyDown sees a key press in a pane's terminal before the terminal does.
// It reports whether the prefix took it.
func (a *App) keyDown(pane string, mods ui.Modifiers, key ui.Key) bool {
	if !a.keysOn() {
		return false
	}
	tk := &a.tk
	k, ok := keyString(mods, key)
	if !tk.live() {
		if !ok {
			return false
		}
		a.loadKeys()
		if k != tk.leader {
			return false
		}
		a.arm(pane)
		return true
	}
	if typesText(mods, key) {
		// Its text comes to gated.Write, which takes it.
		return false
	}
	if !ok {
		return tk.repeat.Load() == 0 // a key with no name in tuios: swallowed, as the TUI does
	}
	return a.stepKey(k)
}

// arm starts the prefix for a pane's terminal, "" for none.
func (a *App) arm(pane string) {
	tk := &a.tk
	tk.mu.Lock()
	tk.menu, tk.since, tk.pane = "", time.Now(), pane
	tk.repeat.Store(0)
	tk.armed.Store(true)
	tk.mu.Unlock()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// disarm ends the prefix.
func (a *App) disarm() {
	tk := &a.tk
	tk.mu.Lock()
	tk.armed.Store(false)
	tk.repeat.Store(0)
	tk.menu = ""
	tk.mu.Unlock()
}

// takeTyped is gated.Write's: text a terminal writes while the prefix is
// armed is the prefix's next key. It reports whether it took the bytes.
func (a *App) takeTyped(p []byte) bool {
	if !a.tk.live() {
		return false
	}
	k, ok := typedKey(p)
	if !ok {
		return false
	}
	return a.stepKey(k)
}

// subMenus are the actions that open a sub-prefix, and the menu each opens.
var subMenus = map[string]string{
	"prefix_workspace": "workspace",
	"prefix_window":    "window",
	"prefix_layout":    "layout",
	"prefix_minimize":  "minimize",
}

// stepKey takes the prefix's next key: it opens a sub-prefix, sends the
// leader to the pane, runs an action, or cancels. Any goroutine may call
// it; what it runs, runs on the main thread. It reports whether it took
// the key: in a repeat window, a key of no repeating action ends the
// prefix and goes on to the pane.
func (a *App) stepKey(k string) bool {
	tk := &a.tk
	tk.mu.Lock()
	if !tk.live() {
		tk.mu.Unlock()
		return false
	}
	menu, pane := tk.menu, tk.pane
	var action string
	switch menu {
	case "":
		action = tk.reg.GetPrefixAction(k)
	case "workspace":
		action = tk.reg.GetWorkspacePrefixAction(k)
	case "window":
		action = tk.reg.GetWindowPrefixAction(k)
	case "layout":
		action = tk.reg.GetLayoutPrefixAction(k)
	case "minimize":
		action = tk.reg.GetMinimizePrefixAction(k)
	}
	repeats := menu == "" && action != "" && tk.reg.PrefixRepeats(action)
	if tk.repeat.Load() != 0 && !repeats {
		tk.armed.Store(false)
		tk.repeat.Store(0)
		tk.mu.Unlock()
		return false
	}
	leaderAgain := menu == "" && action == "" && k == tk.leader
	switch sub, ok := subMenus[action]; {
	case ok:
		tk.menu, tk.since = sub, time.Now()
	case repeats:
		// Stays armed for a moment, so a run of arrows costs one prefix,
		// as in the TUI.
		tk.repeat.Store(time.Now().Add(repeatWindow).UnixNano())
	default:
		tk.armed.Store(false)
		tk.menu = ""
	}
	tk.mu.Unlock()
	a.update(func() {
		switch {
		case leaderAgain:
			// The leader two times sends it to the program, as tmux does.
			if t := a.termFor(pane); t != nil && len(tk.literal) > 0 {
				t.Send(tk.literal)
			}
		case action != "" && subMenus[action] == "":
			a.runKeyAction(action)
		}
		if a.win != nil {
			a.win.Invalidate()
		}
	})
	return true
}

// nativeActions are the keymap's actions guios runs on its own path, and
// daemonActions those it asks the daemon to run (run-command): the TUI
// runs them when one shows the session. The which-key card lists these
// alone; the rest are the TUI's own screens (copy mode, tape, scrollback).
var nativeActions = map[string]bool{
	"prefix_new_window": true, "window_prefix_new": true,
	"prefix_close_window": true, "window_prefix_close": true,
	"prefix_next_window": true, "window_prefix_next": true,
	"prefix_prev_window": true, "window_prefix_prev": true,
	"prefix_select_0": true, "prefix_fullscreen": true,
	"terminal_focus_left": true, "terminal_focus_right": true, "terminal_focus_up": true, "terminal_focus_down": true,
	"prefix_split_horizontal": true, "prefix_split_vertical": true, "split_horizontal": true, "split_vertical": true,
	"prev_session": true, "next_session": true, "prefix_session_switcher": true, "goto_picker": true, "choose_tree": true,
	"prefix_close_session": true, "prefix_command_palette": true, "prefix_settings": true, "prefix_keybinds": true,
	"prefix_file_search": true, "prefix_review": true, "prefix_inbox": true,
	"prefix_next_attention": true, "prefix_mail": true,
	"workspace_prefix_switch_1": true, "workspace_prefix_move_1": true, "new_workspace": true,
	"prev_workspace": true, "next_workspace": true,
	"prefix_workspace": true, "prefix_window": true, "prefix_layout": true, "prefix_minimize": true,
	"workspace_prefix_cancel": true, "window_prefix_cancel": true, "layout_prefix_cancel": true, "minimize_prefix_cancel": true,
	// The layouts, which guios lays out with tuios's engine (layoutKeys).
	"prefix_toggle_tiling": true, "toggle_tiling": true, "window_prefix_tiling": true, "cycle_layout": true,
	"prefix_rotate_split": true, "rotate_split": true, "prefix_equalize_splits": true, "equalize_splits": true,
	"window_prefix_float": true,
	"snap_corner_1":       true, "snap_corner_2": true, "snap_corner_3": true, "snap_corner_4": true,
	"snap_left": true, "snap_right": true, "snap_fullscreen": true, "unsnap": true,
	"cycle_master_position": true, "swap_with_master": true, "focus_master": true, "add_master": true, "remove_master": true,
	"scroll_focus_left": true, "scroll_focus_right": true, "scroll_move_left": true, "scroll_move_right": true,
	"scroll_cycle_width": true, "scroll_maximize": true, "scroll_consume": true, "scroll_expel": true,
}

var daemonActions = map[string]bool{
	"resize_width_50": true, "resize_height_50": true,
	"minimize_prefix_focused": true, "minimize_prefix_restore_1": true, "minimize_prefix_restore_all": true,
	"close_workspace": true, "last_workspace": true,
}

// layoutKeys are the keymap's layout actions as guios's own.
var layoutKeys = map[string]string{
	"prefix_toggle_tiling": "layout.tiling", "toggle_tiling": "layout.tiling", "window_prefix_tiling": "layout.tiling",
	"cycle_layout":        "layout.next",
	"prefix_rotate_split": "layout.rotate", "rotate_split": "layout.rotate",
	"prefix_equalize_splits": "layout.equalize", "equalize_splits": "layout.equalize",
	"window_prefix_float":   "layout.float",
	"cycle_master_position": "layout.master.position", "add_master": "layout.master.add", "remove_master": "layout.master.remove",
	"swap_with_master": "layout.master.swap", "focus_master": "layout.master.focus",
	"scroll_cycle_width": "layout.scroll.width", "scroll_maximize": "layout.scroll.max",
	"scroll_consume": "layout.scroll.consume", "scroll_expel": "layout.scroll.expel",
	"scroll_move_left": "layout.scroll.moveLeft", "scroll_move_right": "layout.scroll.moveRight",
	"snap_fullscreen": "layout.snap.full", "unsnap": "layout.snap.unsnap",
}

// snapCorners are the corner snaps by their keymap action.
var snapCorners = map[string]string{
	"snap_corner_1": "top-left", "snap_corner_2": "top-right", "snap_corner_3": "bottom-left", "snap_corner_4": "bottom-right",
}

// digitOf is the number a numbered action ends in, as in prefix_select_3.
func digitOf(action, prefix string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimPrefix(action, prefix))
	return n, err == nil && strings.HasPrefix(action, prefix)
}

// runKeyAction runs a keymap action, on the main thread.
func (a *App) runKeyAction(action string) {
	s := a.session(a.selected)
	if n, ok := digitOf(action, "prefix_select_"); ok {
		a.selectPaneN(s, n)
		return
	}
	if n, ok := digitOf(action, "workspace_prefix_switch_"); ok {
		a.showWorkspace(s, n)
		return
	}
	if n, ok := digitOf(action, "switch_workspace_"); ok {
		a.showWorkspace(s, n)
		return
	}
	if n, ok := digitOf(action, "workspace_prefix_move_"); ok {
		a.movePaneTo(s, "", n)
		return
	}
	switch action {
	case "prefix_new_window", "window_prefix_new", "new_window":
		a.splitPane(s, "", "")
	case "prefix_close_window", "window_prefix_close", "close_window":
		a.closeFocused()
	case "prefix_next_window", "window_prefix_next", "next_window", "terminal_next_window":
		a.cyclePane(1)
	case "prefix_prev_window", "window_prefix_prev", "prev_window", "terminal_prev_window":
		a.cyclePane(-1)
	case "terminal_focus_left", "terminal_focus_right", "terminal_focus_up", "terminal_focus_down":
		a.focusDirection(strings.TrimPrefix(action, "terminal_focus_"))
	case "prefix_fullscreen", "toggle_zoom":
		a.zoomPane(s, "")
	case "prefix_split_horizontal", "split_horizontal":
		a.splitPane(s, "", "down")
	case "prefix_split_vertical", "split_vertical":
		a.splitPane(s, "", "right")
	case "prev_session":
		a.selectOffset(-1)
	case "next_session":
		a.selectOffset(1)
	case "prefix_session_switcher", "goto_picker", "choose_tree", "prefix_command_palette":
		a.openPalette()
	case "new_worktree":
		a.newWorktreeSession()
	case "prefix_close_session":
		a.closeSession(a.selected, true)
	case "prefix_settings":
		a.openSettings("")
	case "prefix_keybinds":
		a.openSettings("keyboard")
	case "prefix_toggle_sidebar":
		// Not on the card: its row there is the TUI's spotlight and other
		// rail too.
		a.toggleSidebar()
	case "prefix_file_search":
		a.toggleSide(sideFiles)
	case "prefix_review":
		a.toggleSide(sideChanges)
	case "prefix_inbox", "prefix_mail":
		a.toggleCockpit()
	case "prefix_next_attention":
		a.jumpToAttention()
	case "new_workspace":
		a.newWorkspace(s)
	case "next_workspace":
		a.stepWorkspace(s, 1)
	case "prev_workspace":
		a.stepWorkspace(s, -1)
	case "prefix_exit_mode", "workspace_prefix_cancel", "window_prefix_cancel", "layout_prefix_cancel", "minimize_prefix_cancel":
	case "scroll_focus_left", "scroll_focus_right":
		// The strip's columns, as the TUI's scroll focus keys move.
		a.arrange(s, "scroll."+strings.TrimPrefix(action, "scroll_focus_"), nil)
	case "snap_left", "snap_right":
		// As the TUI's: a snap with tiling off, else a move of the focus.
		if s != nil && s.LayoutMode == "floating" {
			a.run("layout.snap."+strings.TrimPrefix(action, "snap_"), nil)
		} else {
			a.focusDirection(strings.TrimPrefix(action, "snap_"))
		}
	case "snap_corner_1", "snap_corner_2", "snap_corner_3", "snap_corner_4":
		if s != nil && s.LayoutMode != "floating" {
			a.toast(locales.LayoutSnapNeedsFloating())
			break
		}
		a.arrange(s, "snap", func(p *proto.ArrangeParams) { p.Mode = snapCorners[action] })
	default:
		if id, ok := layoutKeys[action]; ok {
			a.run(id, nil)
			break
		}
		if s == nil {
			return
		}
		a.call(s.ID, "command", proto.CommandParams{Session: s.ID, Command: action}, func(err error) {
			switch {
			case errors.Is(err, client.ErrNeedsTUI):
				a.toast(locales.KeysNeedTui())
			case err != nil:
				a.toast(err.Error())
			}
		})
	}
	a.focusTerminal()
}

// selectPaneN puts the user in a session's nth pane, as the TUI's
// prefix_select_N: 1 the first, 0 the tenth.
func (a *App) selectPaneN(s *proto.Session, n int) {
	if s == nil {
		return
	}
	if n == 0 {
		n = 10
	}
	if n > len(s.Panes) {
		return
	}
	id := s.Panes[n-1].ID
	a.setFocus(s, id)
	a.paneRef(id).Focus()
}

// keysShortcut arms the prefix where no terminal has the keyboard, as in a
// workspace with no pane: the card then takes the next keys.
func (a *App) keysShortcut(c *ui.Context) {
	if !a.keysOn() || a.tk.live() {
		return
	}
	if s := a.session(a.selected); s != nil && len(s.Panes) > 0 {
		return // a pane's terminal has the leader (keyDown)
	}
	a.loadKeys()
	mods, key, ok := leaderChord(a.tk.leader)
	if ok && c.Shortcut(mods, key) {
		a.arm("")
	}
}

// leaderChord reads a leader such as "ctrl+b" as a key and its modifiers.
func leaderChord(k string) (ui.Modifiers, ui.Key, bool) {
	parts := strings.Split(k, "+")
	var mods ui.Modifiers
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "ctrl":
			mods |= ui.Ctrl
		case "alt", "opt":
			mods |= ui.Alt
		case "shift":
			mods |= ui.Shift
		case "super", "cmd":
			mods |= ui.Super
		}
	}
	last := parts[len(parts)-1]
	for key := ui.KeyA; key <= ui.KeyZ; key++ {
		if s, _ := keyString(0, key); s == last {
			return mods, key, true
		}
	}
	for key, name := range keyNamesTK {
		if name == last {
			return mods, key, true
		}
	}
	for key, c := range usChars {
		if c[0] == last {
			return mods, key, true
		}
	}
	return 0, 0, false
}

// keysTitle is the card's title for a menu.
func keysTitle(menu string) string {
	switch menu {
	case "workspace":
		return locales.KeysMenuWorkspace()
	case "window":
		return locales.KeysMenuWindow()
	case "layout":
		return locales.KeysMenuLayout()
	case "minimize":
		return locales.KeysMenuMinimize()
	}
	return locales.KeysMenuPrefix()
}

// keysCard is the which-key card: the keys of the menu the prefix is in,
// as the TUI shows them, at the window's bottom right.
func (a *App) keysCard(c *ui.Context, pal *palette) {
	tk := &a.tk
	if !tk.live() || tk.reg == nil || tk.repeat.Load() != 0 {
		return
	}
	tk.mu.Lock()
	menu, since, pane := tk.menu, tk.since, tk.pane
	tk.mu.Unlock()
	viaCard := pane == ""
	if !viaCard && !tk.whichKey {
		return
	}
	shown := viaCard || menu != "" || c.Now().Sub(since) >= whichKeyDelay
	if !shown {
		c.After(whichKeyDelay - c.Now().Sub(since))
	}
	var groups []config.KeybindingGroup
	for _, g := range config.PrefixMenuGroups(tk.reg, menu, config.MenuState{Daemon: true, Minimized: -1}) {
		g.Bindings = slices.DeleteFunc(g.Bindings, func(b config.Keybinding) bool {
			return !nativeActions[b.Action] && !daemonActions[b.Action]
		})
		if len(g.Bindings) > 0 {
			groups = append(groups, g)
		}
	}
	card := ui.Column(c.Key("tuios-keys")).Absolute().Right(cardGap+12).Bottom(cardGap+12).
		Padding(14, 16).Gap(10).Radius(12).Background(pal.popover).Label(keysTitle(menu))
	floating(card, pal)
	if !shown {
		card.Opacity(0)
	}
	if viaCard {
		// No terminal has the keyboard: the card takes it, and the keys.
		card.Focusable().FocusRing(false).AutoFocus().HandleInput(func(ev ui.InputEvent) bool {
			switch ev.Kind {
			case ui.InputKeyDown:
				if k, ok := keyString(ev.Mods, ev.Key); ok && !typesText(ev.Mods, ev.Key) {
					a.stepKey(k)
				}
				if !a.tk.armed.Load() {
					a.defer_(a.focusTerminal)
				}
				return true
			case ui.InputText:
				if k, ok := typedKey([]byte(ev.Text)); ok {
					a.stepKey(k)
				}
				if !a.tk.armed.Load() {
					a.defer_(a.focusTerminal)
				}
				return true
			}
			return false
		})
		card.TextCaret(ui.Rect{})
	}
	card.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			kbd(c, pal, chordLabel(tk.leader))
			ui.Text(c, keysTitle(menu)).FontSize(13).FontWeight(650).TextColor(pal.text)
		})
		cols := 1
		if len(groups) > 2 {
			cols = 3
		}
		ui.Row(c).Gap(22).AlignItems(ui.Start).Children(func() {
			for col := range cols {
				ui.Column(c.Key("col" + strconv.Itoa(col))).Gap(10).Children(func() {
					for i, g := range groups {
						if i%cols != col {
							continue
						}
						ui.Column(c.Key("g" + strconv.Itoa(i))).Gap(3).Children(func() {
							if g.Title != "" {
								ui.Text(c, g.Title).FontSize(11).FontWeight(600).TextColor(pal.textFaint).Margin(0, 0, 2, 0)
							}
							for j, b := range g.Bindings {
								ui.Row(c.Key("b" + strconv.Itoa(j))).Gap(8).AlignItems(ui.Center).Children(func() {
									ui.Row(c).Width(58).Shrink(0).Children(func() { kbd(c, pal, b.Key) })
									desc := b.Description
									if b.Submenu {
										desc += " …"
									}
									ui.Text(c, desc).FontSize(12).TextColor(pal.textMuted).SingleLine()
								})
							}
						})
					}
				})
			}
		})
		ui.Text(c, locales.KeysEscape()).FontSize(11).TextColor(pal.textFaint)
	})
}

// chordLabel shows a chord as guios's menus do: "Ctrl+B".
func chordLabel(k string) string {
	l := config.KeyLabel(k)
	if i := strings.LastIndexByte(l, '+'); i > 0 && i == len(l)-2 {
		l = l[:i+1] + strings.ToUpper(l[i+1:])
	}
	return l
}
