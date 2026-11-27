package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// filesDividerStep is how far < and > move the divider, in percent of the
// width.
const filesDividerStep = 5

// FilesAction runs one action of [keybindings.files], for a key or a click
// on its hint.
func (m *OS) FilesAction(act string) tea.Cmd {
	switch act {
	case config.ActionFilesDown:
		m.FilesMove(1)
	case config.ActionFilesUp:
		m.FilesMove(-1)
	case config.ActionFilesPageDown:
		m.FilesPage(true)
	case config.ActionFilesPageUp:
		m.FilesPage(false)
	case config.ActionFilesTop:
		m.FilesEdge(false)
	case config.ActionFilesBottom:
		m.FilesEdge(true)
	case config.ActionFilesOpen:
		return m.FilesOpen()
	case config.ActionFilesParent:
		return m.FilesParent()
	case config.ActionFilesHome:
		return m.FilesHome()
	case config.ActionFilesGoTo:
		m.FilesStartPrompt(true)
	case config.ActionFilesFilter:
		m.FilesStartPrompt(false)
	case config.ActionFilesFind:
		return m.FilesFind()
	case config.ActionFilesFill:
		if m.QuickLookOpen() {
			m.QuickLookFullSize()
		}
	case config.ActionFilesHidden:
		m.FilesToggleHidden()
	case config.ActionFilesSort:
		m.FilesSort(false)
	case config.ActionFilesSortReverse:
		m.FilesSort(true)
	case config.ActionFilesEdit:
		return m.FilesEdit()
	case config.ActionFilesOpenSystem:
		return m.FilesOpenSystem()
	case config.ActionFilesInsertPath:
		m.FilesInsertPath()
	case config.ActionFilesCopyPath:
		return m.FilesCopyPath()
	case config.ActionFilesClose:
		m.CloseFilesView()
	case config.ActionFilesTwoSides:
		return m.FilesTwoSides()
	case config.ActionFilesOtherSide:
		m.FilesOtherSide()
	case config.ActionFilesPrevTab:
		m.FilesNextTab(-1)
	case config.ActionFilesNextTab:
		m.FilesNextTab(1)
	case config.ActionFilesNewTab:
		return m.FilesNewTab()
	case config.ActionFilesCloseTab:
		m.FilesCloseTab()
	case config.ActionFilesNarrow:
		m.FilesDivider(-filesDividerStep)
	case config.ActionFilesWiden:
		m.FilesDivider(filesDividerStep)
	case config.ActionFilesZoom:
		m.FilesZoom()
	case config.ActionFilesReview:
		return m.FilesReview()
	default:
		if n, ok := strings.CutPrefix(act, "files_tab_"); ok && len(n) == 1 && n[0] >= '1' && n[0] <= '9' {
			m.FilesGoTab(int(n[0] - '1'))
		}
	}
	return nil
}
