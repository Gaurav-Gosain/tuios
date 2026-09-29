package app

import (
	"path/filepath"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Search is bounded so a large or remote tree cannot keep the picker waiting.
// When the time is up, the results found so far are shown as partial.
const (
	fileSearchMaxDirs  = 1500
	fileSearchMaxFiles = 5000
	fileSearchBudget   = 2500 * time.Millisecond
)

var fileSearchSkipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "target": true}

type fileSearchMsg struct {
	Gen       uint64
	Root      string
	Origin    string
	Paths     []string
	Truncated bool
	Err       string
}

// OpenFileSearch searches below the focused pane's directory, or below the
// folder the sidebar shows when fromSidebar is set. It shows the results in the
// existing palette. The filesystem walk runs off the Update goroutine.
func (m *OS) OpenFileSearch(fromSidebar bool) tea.Cmd {
	if m.learnOff(learnNoteFiles) {
		return nil
	}
	window := m.GetFocusedWindow()
	var root, origin string
	if fromSidebar && m.filesOn() && m.filesView.Dir != "" {
		root, origin = m.filesView.Dir, m.filesView.Origin
		if origin == "" && window != nil {
			origin = window.ID
		}
	} else {
		if window == nil || paneDir(window) == "" {
			m.ShowNotification("No pane directory to search.", "info", m.Settings.NotificationDuration)
			return nil
		}
		root, origin = paneDir(window), window.ID
	}
	if m.FileViewSpoofed() {
		m.ShowNotification(fileSpoofRefusal, "warning", m.Settings.NotificationDuration)
		return nil
	}
	client, host := m.DaemonClient, m.AttachedHost
	m.fileSearch = true
	m.fileSearchGen++
	m.fileSearchScanning = true
	m.fileSearchTruncated = false
	m.fileSearchErr = ""
	m.fileSearchCancel = new(atomic.Bool)
	m.OpenCommandPalette()
	gen := m.fileSearchGen
	cancel := m.fileSearchCancel
	return func() tea.Msg {
		return scanSidebarFiles(root, origin, client, host, gen, cancel)
	}
}

func scanSidebarFiles(root, origin string, client *session.TUIClient, host string, gen uint64, cancel *atomic.Bool) fileSearchMsg {
	msg := fileSearchMsg{Gen: gen, Root: root, Origin: origin}
	queue := []string{root}
	deadline := time.Now().Add(fileSearchBudget)
	for scanned := 0; len(queue) > 0 && len(msg.Paths) < fileSearchMaxFiles && scanned < fileSearchMaxDirs && !cancel.Load() && time.Now().Before(deadline); scanned++ {
		dir := queue[0]
		queue = queue[1:]
		var entries []fileEntry
		var capped bool
		if client != nil {
			listing, err := client.ReadDir(origin, dir, fileViewMaxEntries, true)
			if err != nil || listing.Err != "" {
				if dir == root {
					msg.Err = "tuios could not search that folder."
					return msg
				}
				msg.Truncated = true
				continue
			}
			capped = listing.Capped
			for _, e := range listing.Entries {
				entries = append(entries, fileEntry{Name: e.Name, Dir: e.IsDir})
			}
		} else {
			if host != "" {
				msg.Err = "tuios can not search files on that machine."
				return msg
			}
			items, more, err := readDirFunc(dir, fileViewMaxEntries)
			if err != nil {
				if dir == root {
					msg.Err = session.DirReadError(err)
					return msg
				}
				msg.Truncated = true
				continue
			}
			capped = more
			for _, e := range items {
				entries = append(entries, fileEntry{Name: e.Name(), Dir: e.IsDir()})
			}
		}
		msg.Truncated = msg.Truncated || capped
		for _, e := range entries {
			path := filepath.Join(dir, e.Name)
			if e.Dir {
				if !fileSearchSkipDirs[e.Name] {
					if len(queue) < fileSearchMaxDirs {
						queue = append(queue, path)
					} else {
						msg.Truncated = true
					}
				}
				continue
			}
			msg.Paths = append(msg.Paths, path)
			if len(msg.Paths) == fileSearchMaxFiles {
				msg.Truncated = true
				break
			}
		}
	}
	if len(queue) > 0 {
		msg.Truncated = true
	}
	return msg
}

func (m *OS) handleFileSearch(msg fileSearchMsg) {
	if !m.fileSearch || !m.ShowCommandPalette || msg.Gen != m.fileSearchGen {
		return
	}
	m.fileSearchScanning = false
	m.fileSearchTruncated = msg.Truncated
	m.fileSearchErr = msg.Err
	items := make([]CommandPaletteItem, 0, len(msg.Paths))
	for _, path := range msg.Paths {
		path := path
		rel, _ := filepath.Rel(msg.Root, path)
		items = append(items, CommandPaletteItem{
			Name: rel, Category: "Files",
			Action: func(m *OS) (*OS, tea.Cmd) {
				m.EnterSidebarFocus()
				m.filesView.Show = 1
				cmd := m.requestFileList(filepath.Dir(path), msg.Origin, true)
				m.followFileRow(filepath.Base(path))
				return m, cmd
			},
		})
	}
	m.PaletteItems = items
	m.CommandPaletteSelected = 0
	m.CommandPaletteScroll = 0
}
