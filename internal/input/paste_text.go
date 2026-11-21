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

func pasteLine(content string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.Join(strings.Fields(content), " "))
}

func textFieldPaste(o *app.OS) func(string) tea.Cmd {
	switch {
	case o.ShowSessionClose, o.DaemonRestartOpen(), o.HintsOpen(), o.PaneLabelsOpen(),
		o.ShowQuitMenu, o.ContextMenuActive():
		return nil
	case o.MachineSheetOpen():
		return typed(o.MachineSheetType)
	case o.PathPickerOpen():
		return o.PathPickerType
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
	case o.PluginApprovalOpen(), o.IntegrationAskOpen():
		return nil
	case o.CopyDialogOpen():
		return typed(o.CopyDialogPaste)
	case o.TransfersOwnsKeys(), o.ShowTapeReview:
		return nil
	case o.SidebarFocused && !o.PanelOwnsKeyboard():
		return nil
	case o.ShowTapeManager:
		if o.TapeManager == nil || o.TapeManager.Mode != app.TapeManagerNaming {
			return nil
		}
		return typed(o.TapeNamePaste)
	case o.ShowHelp:
		if !o.HelpSearchMode {
			return nil
		}
		return typed(func(s string) {
			if o.HelpSearchPaste(s) {
				o.HelpScrollOffset = 0
			}
		})
	}
	if paste, ok := settingsFieldPaste(o); ok {
		return paste
	}
	switch {
	case o.GotoPickerOpen():
		return listPickerPaste(o)
	case o.ShowAgentMail, o.ShowInbox:
		return nil
	case o.NavigatorOpen():
		if !o.NavigatorSearching() {
			return nil
		}
		return typed(o.NavigatorPaste)
	case o.BufferChooserOpen():
		return nil
	case o.ShowCommandPalette:
		return listPickerPaste(o)
	case o.ShowLauncher:
		return listPickerPaste(o)
	case o.AnyOverlayOpen(), o.ShowLogs, o.ShowCacheStats:
		return nil
	case o.ShowScrollbackBrowser:
		return scrollbackSearchPaste(o)
	}
	return copyModeSearchPaste(o)
}

func settingsFieldPaste(o *app.OS) (paste func(string) tea.Cmd, ok bool) {
	switch {
	case o.ThemeEditorOpen():
		if _, typing := o.ThemeEditorTyping(); typing {
			return typed(o.ThemeEditorTypePaste), true
		}
		return nil, true
	case o.ShowThemePicker:
		return listPickerPaste(o), true
	case o.PresetPickerOpen():
		return nil, true
	case o.ShowGlyphPicker:
		return listPickerPaste(o), true
	case o.ShowEffectPicker:
		return listPickerPaste(o), true
	case o.ShowSectionEditor, o.ShowDockEditor:
		return nil, true
	case o.ShowKeybindManager:
		if o.KeybindArmed() || o.KeybindTab != app.KeybindTabBindings {
			return nil, true
		}
		return typed(o.KeybindQueryPaste), true
	case o.ShowSettings:
		switch {
		case o.SettingsEditActive():
			return typed(o.SettingsEditAppend), true
		case o.SettingsSearchOpen():
			return typed(o.SettingsSearchPaste), true
		}
		return nil, true
	case o.ShowLayoutPicker:
		if o.LayoutPickerMode == "save" {
			return typed(o.LayoutSavePaste), true
		}
		return listPickerPaste(o), true
	case o.ShowHostPicker:
		return listPickerPaste(o), true
	}
	return nil, false
}

func listPickerPaste(o *app.OS) func(string) tea.Cmd {
	return func(s string) tea.Cmd {
		cmd, _ := o.ListPickerPaste(s)
		return cmd
	}
}

func scrollbackSearchPaste(o *app.OS) func(string) tea.Cmd {
	browser := o.ScrollbackBrowser
	switch {
	case browser == nil:
		return nil
	case browser.SearchActive:
		return typed(browser.SearchPaste)
	case !browser.ShowHelp && browser.OutputMode && browser.Vim != nil && browser.Vim.Mode == scrollback.VimSearch:
		vim := browser.Vim
		return typed(func(s string) {
			vim.Search.Insert(s)
			vim.EnsureVisible()
		})
	}
	return nil
}

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

func pasteKeyIntoTextField(msg tea.KeyPressMsg, o *app.OS) (tea.Cmd, bool) {
	if o.ProcessingRemoteKeys {
		return nil, false
	}
	if msg.String() != "ctrl+v" && sectionAction(msg, o, config.TableTerminalMode) != terminalPasteHost {
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
