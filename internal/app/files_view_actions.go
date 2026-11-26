package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

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
	}
	return nil
}
