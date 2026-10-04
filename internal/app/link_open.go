package app

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/anmitsu/go-shlex"
)

// Which machine opens a link is the whole of this file, and it is the question
// the rest of the feature exists to be careful about.
//
// There are two machines. The panes, the daemon and the tuios client process
// all run on one of them; call it the pane machine. The person looking at the
// screen sits at the other; call it the viewer machine. On a local run they are
// the same machine and nothing is interesting. Under `tuios ssh` and
// `tuios-web` they are not: the client code runs on the server, so calling
// xdg-open there opens a browser on the server's console, in front of nobody.
// That is the mistake this file is written to not make.
//
// The two kinds of link fall on opposite sides of that split:
//
//   - A file:// link names a file on the pane machine. The client process is
//     always on the pane machine, in every deployment tuios has, so the path
//     can be stat'd here and opened here. A pane is the right place to open it
//     in, because a pane is a window onto exactly that machine.
//
//   - An http:// or https:// link has to be opened by a browser the person can
//     see, which lives on the viewer machine. tuios has no way to run anything
//     there: there is no escape sequence for "open this URL", and adding a
//     protocol for running commands on the viewer's box is a much larger thing
//     to own than a link. So a local client opens it and a remote one does not
//     pretend to. What a remote client can do is put the address on the
//     viewer's clipboard, because OSC 52 rides the same stream the frame does
//     and lands on the viewer machine, which is the whole point of it.

// linkFilePath returns the local filesystem path a file:// link names, and
// whether it names one. A file:// URL with a host that is not this machine is
// somebody else's filesystem and gets no special treatment.
func linkFilePath(rawURL string) (string, bool) {
	if !strings.HasPrefix(rawURL, "file://") {
		return "", false
	}
	return localCwdPath(rawURL)
}

// OpenLink performs the default action for a link.
//
// It never fails silently. Every branch that cannot do the obvious thing says
// what it did instead, because a click that appears to do nothing is worse than
// one that says it copied an address.
func (m *OS) OpenLink(rawURL string) tea.Cmd {
	if rawURL == "" {
		return nil
	}
	// A control byte or a space has no place in an address. The emulator
	// strips control bytes from OSC 8 already; this also covers a URL that
	// reached here some other way, and refuses rather than repairs it.
	if !linkTextClean(rawURL) {
		m.ShowNotification("tuios did not open the link. Its address has control characters or spaces.",
			"warning", m.Settings.NotificationDuration)
		return nil
	}

	if path, ok := linkFilePath(rawURL); ok {
		return m.openLocalPath(path, rawURL)
	}

	// A file:// link that linkFilePath turned down names a file on another
	// machine (file://otherhost/path). That file is not here to open, and
	// handing the address to the desktop would open whatever this machine
	// has at the same path.
	if strings.HasPrefix(strings.ToLower(rawURL), "file:") {
		m.ShowNotification("That file link names another machine. tuios did not open it. The address is on your clipboard.",
			"warning", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}

	// The scheme decides whether this is handed to the desktop at all. See
	// linkOpenableScheme.
	if !linkOpenableScheme(rawURL) {
		m.ShowNotification("tuios can not open that kind of link. The address is on your clipboard.",
			"warning", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}

	// Not a local file, so it needs the viewer's own machine.
	if m.IsRemoteClient() {
		m.ShowNotification("Copied the link. A remote client can not open it for you.",
			"info", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}
	argv, err := linkOpenerArgv(m.Settings.LinkOpener, rawURL)
	if errors.Is(err, errNoDesktop) {
		m.ShowNotification("Copied the link. This machine has no desktop to open it. Use the link click of your terminal.",
			"info", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}
	if err != nil {
		m.LogError("Could not read the link opener: %v", err)
		m.ShowNotification("tuios can not read the link opener command. Change appearance.link_opener.",
			"error", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}
	watch, err := startLinkOpener(argv, rawURL)
	if err != nil {
		m.LogError("Failed to open link %s with %s: %v", rawURL, argv[0], err)
		m.ShowNotification("Could not start "+sanitizeLinkText(argv[0])+". The link is on your clipboard.",
			"error", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}
	m.ShowNotification("Opening "+linkHostLabel(rawURL)+".", "success", m.Settings.NotificationDuration)
	return watch
}

// linkTextClean reports whether an address holds no control byte and no
// space.
func linkTextClean(s string) bool {
	for _, r := range s {
		if r <= 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false
		}
	}
	return true
}

// linkHostLabel names where a link goes, for the message that confirms it.
// The host is what tells a user whether the click went where they meant, and
// for a link whose text differs from its target it is the second place the
// target is shown, after the hover label.
func linkHostLabel(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return sanitizeLinkText(u.Host)
	}
	return "the link"
}

// A link's address is attacker-controlled. Any program running in a pane can
// print an OSC 8 escape carrying any URI at all, and unlike a bare URL, which
// this file's scanner only ever produces from three schemes, a marked link's
// address never passed through anything that looked at it.
//
// What is on the other end of openInOSViewer is xdg-open, `open`, or the
// Windows protocol handler, and all three resolve a scheme to whatever
// application the desktop registered for it. So an unfiltered address is a way
// for a program in a pane to start an arbitrary registered application, with an
// argument it chose, from one click on text whose visible label it also chose.
// The label above the run names the real target, which is what makes this a
// click the user consents to rather than one they are tricked into; the list
// below is what stops the consent from being worth more than it looks.
//
// Nothing is executed through a shell either way: openInOSViewer builds an argv
// and never a command line, so a semicolon or a backtick in an address is one
// more character of one argument. The risk this list closes is the scheme, not
// the quoting.
//
// The set is the one a terminal is actually asked for. file:// is absent
// because it never reaches here: OpenLink routes it above, to a stat and a pane
// on the machine the file is on. An application's own scheme is refused and
// copied instead, which loses nothing a paste cannot do.
var linkOpenSchemes = []string{"http", "https", "mailto", "ftp", "ftps"}

// linkOpenableScheme reports whether rawURL may be handed to the desktop's own
// handler. An address with no scheme at all is refused: there is nothing for a
// handler to resolve, and guessing http for it would be inventing a target the
// program never wrote.
func linkOpenableScheme(rawURL string) bool {
	scheme, _, ok := strings.Cut(rawURL, ":")
	if !ok || scheme == "" {
		return false
	}
	return slices.Contains(linkOpenSchemes, strings.ToLower(scheme))
}

// openLocalPath opens a path that lives on the pane machine.
//
// A file opens in a pane running the user's editor, which is right on every
// deployment for the same reason the path could be stat'd at all: a pane runs
// on the machine the file is on. A directory is handed to the rail's file view,
// which is the surface that already knows how to show one.
//
// A path that is gone is reported rather than guessed at. A file:// link in a
// pane's scrollback can be minutes old, and the file it named may have been
// built over or deleted since.
func (m *OS) openLocalPath(path, rawURL string) tea.Cmd {
	info, err := os.Stat(path)
	if err != nil {
		m.ShowNotification("That file is gone. The link is on your clipboard.",
			"warning", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}
	if info.IsDir() {
		return m.openDirectoryLink(path)
	}

	argv, err := editorArgv(m.Settings.SidebarEditor)
	if err != nil {
		m.LogError("Could not read the editor command: %v", err)
		m.ShowNotification(editorCommandNote, "warning", m.Settings.NotificationDuration)
		return nil
	}
	m.AddWindow(filepath.Base(path), append(argv, path)...)
	m.ShowNotification("Opened the file in a new pane.", "success", m.Settings.NotificationDuration)
	return nil
}

// openDirectoryLink shows a directory in the rail's files section, which is the
// one surface in tuios that already knows how to list one. The rail has to be
// on and expanded for that to be somewhere the user can see, so a rail that is
// not falls back to the clipboard rather than putting a section nobody can see
// into a folder nobody asked for.
func (m *OS) openDirectoryLink(path string) tea.Cmd {
	if m.OpenFileView(path) {
		// The listing is read off this goroutine, so opening the section hands
		// back the command that reads it. It is parked with the rail's other
		// pending commands rather than returned here, because the caller of a
		// link is not always a path that returns one.
		return m.TakeSidebarCmd()
	}
	m.ShowNotification("Copied the folder path. Turn the sidebar on to browse it.",
		"info", m.Settings.NotificationDuration)
	return tea.SetClipboard(path)
}

const editorCommandNote = "tuios can not read the File editor command. Change it in the sidebar settings."

// editorArgv is the argv the editor pane runs. The File editor setting comes
// first and $EDITOR, $VISUAL and vi follow. It is split the way a shell splits
// words, so "code --wait" arrives as two arguments and a quoted path with a
// space in it stays one. No shell runs it. Every path that opens a file in an
// editor pane goes through here, so they all honour the setting and its quotes.
func editorArgv(setting string) ([]string, error) {
	editor := strings.TrimSpace(setting)
	if editor == "" {
		editor = linkEditor()
	}
	argv, err := shlex.Split(editor, true)
	if err == nil && len(argv) == 0 {
		err = errors.New("the editor command is empty")
	}
	return argv, err
}

// linkEditor is the editor command when the File editor setting is empty:
// $EDITOR, then $VISUAL, then vi, in the order every other tool uses.
func linkEditor() string {
	for _, key := range []string{"EDITOR", "VISUAL"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return "vi"
}
