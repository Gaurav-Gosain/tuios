package app

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

type fileOpenSystemMsg struct {
	Watch tea.Cmd
	Prog  string
	Name  string
	Note  string
	Level string
	Copy  bool
}

type fileOpenFailedMsg struct {
	Prog   string
	Name   string
	Detail string
}

// SidebarFileOpenSystem opens the row under the files cursor, or the row the
// files menu was opened on, with a program on the desktop.
func (m *OS) SidebarFileOpenSystem() tea.Cmd {
	if !m.filesOn() {
		return nil
	}
	dir, e, ok := m.fileActionEntry()
	if !ok {
		m.ShowNotification("Put the cursor on a file first.", "info", m.Settings.NotificationDuration)
		return nil
	}
	if note := m.fileOpenSystemRefusal(); note != "" {
		m.ShowNotification(note, "warning", m.Settings.NotificationDuration)
		return nil
	}
	path := filepath.Join(dir, e.Name)
	if host, _ := m.farOpenHost(); host != "" {
		ask := farOpenAsk{host: host, path: path, name: e.Name, dir: e.Dir}
		m.offerFarOpen(ask, farOpenOfferText(ask))
		return nil
	}
	var rules config.FilesConfig
	if m.UserConfig != nil {
		rules = m.UserConfig.Files
	}
	return func() tea.Msg { return startFileOpener(path, rules) }
}

func (m *OS) fileOpenSystemRefusal() string {
	host, reach := m.farOpenHost()
	if m.IsRemoteClient() {
		return "tuios did not open the file. This client is remote." + m.fileOpenNextStep(host != "" || !reach)
	}
	if !reach {
		return "tuios cannot open a file from a host that you reach through another host." + m.fileOpenNextStep(false)
	}
	if m.FileViewSpoofed() {
		return fileSpoofRefusal
	}
	return ""
}

func (m *OS) fileOpenNextStep(far bool) string {
	if m.KeybindRegistry == nil {
		return ""
	}
	key := func(action string) string {
		if keys := m.KeybindRegistry.Keys(config.TableSidebarFiles, action); len(keys) > 0 {
			return keys[0]
		}
		return ""
	}
	look, send := key("file_quick_look"), key("file_send")
	switch {
	case far && look != "" && send != "":
		return " Press " + look + " to preview it, or " + send + " to send it here."
	case look != "":
		return " Press " + look + " to preview it."
	case far && send != "":
		return " Press " + send + " to send it here."
	}
	return ""
}

func startFileOpener(path string, rules config.FilesConfig) fileOpenSystemMsg {
	name := sanitizeLinkText(filepath.Base(path))
	info, err := os.Stat(path)
	if err != nil {
		return fileOpenSystemMsg{Note: "That file is gone. tuios did not open it.", Level: "warning"}
	}
	argv := rules.OpenArgv(path, info.IsDir())
	if argv == nil {
		if argv, err = desktopOpenerArgv(path); err != nil {
			return fileOpenSystemMsg{
				Note:  "This machine has no desktop to open the file. Add a rule to [files.open] in the config.",
				Level: "info",
			}
		}
	}
	prog := sanitizeLinkText(filepath.Base(argv[0]))
	watch, err := startLinkOpener(argv, path)
	if err != nil {
		return fileOpenSystemMsg{Note: "tuios could not start " + prog + ". Check that it is installed.", Level: "error"}
	}
	return fileOpenSystemMsg{
		Prog: prog,
		Name: name,
		Watch: func() tea.Msg {
			if failed, ok := watch().(linkOpenFailedMsg); ok {
				return fileOpenFailedMsg{Prog: prog, Name: name, Detail: failed.Detail}
			}
			return nil
		},
	}
}

func (m *OS) handleFileOpenSystem(msg fileOpenSystemMsg) tea.Cmd {
	if msg.Watch == nil {
		m.ShowNotification(msg.Note, msg.Level, m.Settings.NotificationDuration)
		return nil
	}
	if msg.Copy {
		m.ShowNotification("Opened a copy of "+msg.Name+" with "+msg.Prog+". Changes stay here.",
			"success", m.Settings.NotificationDuration)
		return msg.Watch
	}
	m.ShowNotification("Opened "+msg.Name+" with "+msg.Prog+".", "success", m.Settings.NotificationDuration)
	return msg.Watch
}

func (m *OS) handleFileOpenFailed(msg fileOpenFailedMsg) {
	m.LogError("%s failed to open %s: %s", msg.Prog, msg.Name, strings.TrimSpace(msg.Detail))
	m.ShowNotification(msg.Prog+" did not open "+msg.Name+". The log has the reason.",
		"error", m.Settings.NotificationDuration)
}
