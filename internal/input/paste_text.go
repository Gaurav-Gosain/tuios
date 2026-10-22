package input

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/scrollback"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// pasteLine is a paste as one line of a one-line field: every run of white
// space, line breaks included, becomes one space, and control characters are
// dropped, so a pasted escape sequence cannot reach the field as one.
func pasteLine(content string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.Join(strings.Fields(content), " "))
}

// textFieldPaste returns how the one-line field that owns the keyboard takes a
// paste, or nil when no such field owns it. It serves the terminal's
// bracketed paste, the clipboard read the paste key asks for, and the paste
// key itself (see pasteKeyIntoTextField), so a field that takes typing takes
// a paste the same way.
//
// The cases follow routeKey's order: whatever would get a typed key gets the
// paste. An owner that takes no text answers nil, and the caller drops the
// paste rather than let it reach the pane under it.
func textFieldPaste(o *app.OS) func(string) tea.Cmd {
	switch {
	case o.ShowSessionClose, o.DaemonRestartOpen(), o.HintsOpen(), o.PaneLabelsOpen(),
		o.ShowQuitMenu, o.ContextMenuActive():
		return nil
	case o.ReviewOpen():
		if !o.ReviewEditing() {
			return nil
		}
		return typed(o.ReviewEditorType)
	case o.ShowAccentPicker:
		return typed(func(s string) {
			for _, r := range s {
				o.AccentPickerHexKey(r)
			}
		})
	case o.Renaming():
		return typed(o.RenameAppend)
	case o.FilePromptOpen():
		if o.FileConfirmOpen() {
			return nil
		}
		return typed(o.FilePromptType)
	case o.WorktreePromptOpen():
		return typed(o.WorktreePromptType)
	case o.PluginApprovalOpen():
		return nil
	case o.CopyDialogOpen():
		return typed(o.CopyDialogPaste)
	case o.TransfersOwnsKeys(), o.ShowTapeReview:
		return nil
	case o.SidebarFocused && !o.ShowHelp && !o.ShowCommandPalette && !o.ShowAgentMail && !o.ShowInbox &&
		!o.MessageViewOpen() && !o.ShowLogs:
		return nil
	case o.ShowTapeManager:
		if o.TapeManager == nil || o.TapeManager.Mode != app.TapeManagerNaming {
			return nil
		}
		// A tape's name is a file name of printable ASCII, as typed.
		return typed(func(s string) {
			o.TapeManager.NameBuffer += strings.Map(func(r rune) rune {
				if r < 32 || r > 126 {
					return -1
				}
				return r
			}, s)
		})
	case o.ShowHelp:
		if !o.HelpSearchMode {
			return nil
		}
		return typed(func(s string) {
			o.HelpSearchQuery += s
			o.HelpScrollOffset = 0
		})
	}
	if paste, ok := settingsFieldPaste(o); ok {
		return paste
	}
	switch {
	case o.ShowSessionSwitcher:
		if o.SessionSwitcherConfirmDelete != "" {
			return nil
		}
		return typed(func(s string) {
			o.SessionSwitcherQuery += s
			o.SessionSwitcherSelected = 0
			o.SessionSwitcherScroll = 0
		})
	case o.ShowAgentMail, o.ShowInbox:
		return nil
	case o.NavigatorOpen():
		if !o.NavigatorSearching() {
			return nil
		}
		return typed(func(s string) { o.NavigatorSetQuery(o.NavigatorQuery() + s) })
	case o.ShowWorkspaceSwitcher:
		return typed(func(s string) {
			o.WorkspaceSwitcherQuery += s
			o.WorkspaceSwitcherSelected = 0
			o.WorkspaceSwitcherScroll = 0
		})
	case o.BufferChooserOpen():
		return nil
	case o.ShowAggregateView:
		return typed(func(s string) {
			o.AggregateViewQuery += s
			o.AggregateViewSelected = 0
			o.AggregateViewScroll = 0
		})
	case o.ShowCommandPalette:
		return typed(func(s string) {
			o.CommandPaletteQuery += s
			o.CommandPaletteSelected = 0
			o.CommandPaletteScroll = 0
		})
	case o.ShowLauncher:
		return func(s string) tea.Cmd {
			o.LauncherQuery += s
			o.LauncherRefilter()
			return o.LauncherIconWork()
		}
	case o.AnyOverlayOpen(), o.ShowLogs, o.ShowCacheStats:
		return nil
	case o.ShowScrollbackBrowser:
		return scrollbackSearchPaste(o)
	}
	return copyModeSearchPaste(o)
}

// settingsFieldPaste is textFieldPaste for Settings and the pickers over it,
// in routeSettingsOverlayKey's order. ok is false when none of them is open.
func settingsFieldPaste(o *app.OS) (paste func(string) tea.Cmd, ok bool) {
	switch {
	case o.ThemeEditorOpen():
		if value, typing := o.ThemeEditorTyping(); typing {
			return typed(func(s string) { o.ThemeEditorTyped(value + s) }), true
		}
		return nil, true
	case o.ShowThemePicker:
		return typed(func(s string) {
			o.ThemePickerQuery += s
			o.ThemePickerRefilter()
		}), true
	case o.PresetPickerOpen():
		return nil, true
	case o.ShowGlyphPicker:
		return typed(func(s string) {
			o.GlyphPickerQuery += s
			o.GlyphPickerRefilter()
		}), true
	case o.ShowEffectPicker:
		return o.EffectPickerType, true
	case o.ShowSectionEditor, o.ShowDockEditor:
		return nil, true
	case o.ShowKeybindManager:
		if o.KeybindArmed() || o.KeybindTab != app.KeybindTabBindings {
			return nil, true
		}
		return typed(func(s string) { o.KeybindSetQuery(o.KeybindQuery() + s) }), true
	case o.ShowSettings:
		switch {
		case o.SettingsEditActive() && o.SettingsEditingNumber():
			return typed(func(s string) {
				if isNumberEntry(s) {
					o.SettingsEditAppend(s)
				}
			}), true
		case o.SettingsEditActive():
			return typed(o.SettingsEditAppend), true
		case o.SettingsSearchOpen():
			return typed(func(s string) { o.SettingsSearchSetQuery(o.SettingsSearchQuery() + s) }), true
		}
		return nil, true
	case o.ShowLayoutPicker:
		return typed(func(s string) {
			o.LayoutPickerQuery += s
			o.LayoutPickerSelected = 0
			o.LayoutPickerScroll = 0
		}), true
	case o.ShowHostPicker:
		return typed(func(s string) {
			o.HostPickerQuery += s
			o.HostPickerSelected = app.FirstPickableHostRow(app.FilterHostPickerItems(o.HostPickerItems, o.HostPickerQuery))
		}), true
	}
	return nil, false
}

// scrollbackSearchPaste is the scrollback browser's search line, in either of
// its two places, or nil when neither is open.
func scrollbackSearchPaste(o *app.OS) func(string) tea.Cmd {
	browser, _ := o.ScrollbackBrowser.(*scrollback.Browser)
	switch {
	case browser == nil:
		return nil
	case browser.SearchActive:
		return typed(func(s string) { browser.SetSearch(browser.SearchQuery + s) })
	case !browser.ShowHelp && browser.OutputMode && browser.Vim != nil && browser.Vim.Mode == scrollback.VimSearch:
		vim := browser.Vim
		return typed(func(s string) {
			vim.SearchQuery += s
			vim.EnsureVisible()
		})
	}
	return nil
}

// copyModeSearchPaste is the focused pane's copy-mode search prompt, or nil
// when it is not open. The paste is typed through the prompt's own key
// handler as one key carrying the text: that handler reads the key's code, not
// its name, so no pasted word can stand for enter or esc there.
func copyModeSearchPaste(o *app.OS) func(string) tea.Cmd {
	w := o.GetFocusedWindow()
	if w == nil || w.CopyMode == nil || !w.CopyMode.Active || w.CopyMode.State != terminal.CopyModeSearch || o.MultiCopy.Has(w.ID) {
		return nil
	}
	return func(s string) tea.Cmd {
		fx := &copyModeEffects{}
		dispatchCopyModeKey(tea.KeyPressMsg{Code: tea.KeyExtended, Text: s}, "", w, fx, &o.Settings)
		_, cmd := fx.apply(o, w)
		return cmd
	}
}

// pasteKeyIntoTextField answers the paste key while a one-line field owns the
// keyboard: ctrl+v, as in the Inbox's editors, or a key bound to
// terminal_paste_host. It asks for the clipboard, and the answer arrives as a
// paste the field takes. A key from send-keys or a tape is not the person at
// the keyboard and may not read their clipboard, so it is left to the field.
func pasteKeyIntoTextField(msg tea.KeyPressMsg, o *app.OS) (tea.Cmd, bool) {
	if o.ProcessingRemoteKeys {
		return nil, false
	}
	if msg.String() != "ctrl+v" && sectionAction(msg, o, (*config.KeybindRegistry).GetTerminalModeAction) != terminalPasteHost {
		return nil, false
	}
	if textFieldPaste(o) == nil {
		return nil, false
	}
	return o.RequestHostPaste(), true
}

func typed(f func(string)) func(string) tea.Cmd {
	return func(s string) tea.Cmd {
		f(s)
		return nil
	}
}
