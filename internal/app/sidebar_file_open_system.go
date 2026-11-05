package app

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// file_open_system: the rail's files section hands the row's file or folder to
// a program on the desktop, the first [[files.open]] rule that matches or the
// system opener (xdg-open, open, wslview, the Windows handler).
//
// It reuses the link opener's two halves: desktopOpenerArgv picks the program
// and startLinkOpener starts it and watches it for a failure. It does not use
// appearance.link_opener or $BROWSER. Both name a browser, and a remote
// editor's $BROWSER helper opens the page on the person's machine, which has
// no copy of a file on this one.
//
// Which machine the file is on is the question, as it is for a link (see
// link_open.go). The opener runs where this client process runs. So it runs
// only when the listing is of this machine and the person sits at it:
//
//   - A listing of another machine (a far pane, or a session attached through
//     a host) names a path that is not here. Opening the same path here would
//     open some other file, or nothing.
//   - A remote client (tuios ssh, tuios-web) runs on the server, and a program
//     started there opens in front of nobody.
//
// Each refusal says what to do instead: quick look reads a far file over the
// link, and send copies it to this machine.

// fileOpenSystemMsg is the opener's start, done off the update goroutine
// because it stats the path. Exactly one of Watch and Note is set.
type fileOpenSystemMsg struct {
	Watch tea.Cmd
	Prog  string
	Name  string
	Note  string
	Level string
}

// fileOpenFailedMsg reports an opener that exited with an error inside the
// watch window.
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
	var rules config.FilesConfig
	if m.UserConfig != nil {
		rules = m.UserConfig.Files
	}
	return func() tea.Msg { return startFileOpener(path, rules) }
}

// fileOpenSystemRefusal is why the opener must not run for this listing, or ""
// when it may.
func (m *OS) fileOpenSystemRefusal() string {
	host := m.filesView.Host
	if host == "" {
		host = m.AttachedHost
	}
	if host != "" {
		return "tuios did not open the file. It is on " + sanitizeLinkText(host) + "." + m.fileOpenNextStep(true)
	}
	if m.IsRemoteClient() {
		return "tuios did not open the file. This client is remote." + m.fileOpenNextStep(false)
	}
	if m.FileViewSpoofed() {
		return fileSpoofRefusal
	}
	return ""
}

// fileOpenNextStep names the keys that still work on the row, as they are
// bound: quick look, and for a file on another machine, send.
func (m *OS) fileOpenNextStep(far bool) string {
	if m.KeybindRegistry == nil {
		return ""
	}
	key := func(action string) string {
		if keys := m.KeybindRegistry.GetSidebarFilesKeys(action); len(keys) > 0 {
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

// startFileOpener finds the program for path and starts it.
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

// handleFileOpenSystem says what the start did and hands back the watch.
func (m *OS) handleFileOpenSystem(msg fileOpenSystemMsg) tea.Cmd {
	if msg.Watch == nil {
		m.ShowNotification(msg.Note, msg.Level, m.Settings.NotificationDuration)
		return nil
	}
	m.ShowNotification("Opened "+msg.Name+" with "+msg.Prog+".", "success", m.Settings.NotificationDuration)
	return msg.Watch
}

// handleFileOpenFailed tells the user the opener failed after it started.
func (m *OS) handleFileOpenFailed(msg fileOpenFailedMsg) {
	m.LogError("%s failed to open %s: %s", msg.Prog, msg.Name, strings.TrimSpace(msg.Detail))
	m.ShowNotification(msg.Prog+" did not open "+msg.Name+". The log has the reason.",
		"error", m.Settings.NotificationDuration)
}
