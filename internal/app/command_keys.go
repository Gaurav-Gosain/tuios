package app

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Command keybindings, the [[keybindings.command]] entries. See
// config/command_keys.go for the entry and how its key reaches the
// dispatcher. This file runs one.
//
// Every type runs the command with sh -c, so a user can write pipes, globs and
// quotes as they would at a prompt, and a fish or nu login shell does not
// change what the line means. The command starts in the focused pane's folder
// (the scratch terminal's rule: a folder on this machine, else home), with
// four variables beside the pane's own environment:
//
//	TUIOS_SESSION           the session the key was pressed in
//	TUIOS_SOCKET            the daemon's socket (a pane has it already)
//	TUIOS_ACTIVE_PANE_ID    the pane that had the focus
//	TUIOS_ACTIVE_PANE_CWD   the folder the command starts in
//
// Where it runs:
//   - scratch and popup ask the daemon on this machine for a popup, so they
//     refuse in a session on another machine, as the scratch terminal does.
//   - pane asks the session's own daemon for the window, so in a session on
//     another machine the command runs on that machine.
//   - shell runs from this client, on the machine the client runs on. For the
//     SSH and web servers that is the server.
//
// Only a key press or the command palette runs an entry. Nothing a pane can
// do reaches the dispatcher without the respond grant: send-keys writes into
// a pane, not into tuios, and run-command refuses key presses from a pane
// without it (refuseTapeTyping). Reloading config.toml rebuilds the key map
// and runs nothing.

// CommandRanMsg reports how a command entry ended, for the entries whose end
// the client waits for: a shell entry, and the call that opens a popup.
type CommandRanMsg struct {
	Label string
	Err   error
}

// commandShellRunner runs a shell entry. Tests replace it.
var commandShellRunner = runCommandShell

// commandPopupOpener opens a popup entry through the daemon. Tests replace it.
var commandPopupOpener = openCommandPopup

// RunCommandBinding runs the [[keybindings.command]] entry behind action.
func (m *OS) RunCommandBinding(action string) tea.Cmd {
	if m.UserConfig == nil {
		return nil
	}
	c, ok := m.UserConfig.Keybindings.CommandFor(action)
	if !ok {
		return nil
	}
	dir := m.scratchDir()
	if m.AttachedHost != "" {
		// Every pane is on the session's machine, so the folder is the
		// focused pane's own path there, and this machine's home means
		// nothing.
		dir = m.remotePaneDir()
	}
	env := m.commandEnv(dir)
	argv := commandArgv(c.Command, env)

	switch c.ResolvedType() {
	case config.CommandTypeScratch:
		return m.toggleScratch(m.commandScratchSpec(c, argv))

	case config.CommandTypePopup:
		if m.IsDaemonSession && m.DaemonClient != nil {
			if m.AttachedHost != "" {
				m.ShowNotification("A popup command works only in a session on this machine.", "warning", m.Settings.NotificationDuration)
				return nil
			}
			req := commandPopupRequest{
				Session: m.SessionName, Title: c.Label(), Command: argv, Dir: dir,
				Width: c.WidthSpec(), Height: c.HeightSpec(), Workspace: m.CurrentWorkspace,
			}
			label := c.Label()
			return func() tea.Msg { return CommandRanMsg{Label: label, Err: commandPopupOpener(req)} }
		}
		if w := m.newLocalPopup(dir, c.Label(), c.WidthSpec(), c.HeightSpec(), argv); w != nil {
			m.FocusWindow(len(m.Windows) - 1)
			m.EnterTerminalMode()
			m.MarkAllDirty()
		}
		return nil

	case config.CommandTypePane:
		// A pane of the layout, next to the focused one, the way a new window
		// opens: the user can keep it, move it or zoom it, and it closes when
		// the command exits. The keyboard goes to it.
		m.AddWindowIn(dir, c.Label(), argv...)
		if m.IsDaemonSession && m.DaemonClient != nil {
			m.pendingStartTerminalMode = true
		} else {
			m.EnterTerminalMode()
		}
		return nil

	case config.CommandTypeShell:
		label := c.Label()
		return func() tea.Msg { return CommandRanMsg{Label: label, Err: commandShellRunner(argv, dir)} }
	}
	return nil
}

// handleCommandRan shows a failed command on the dock.
func (m *OS) handleCommandRan(msg CommandRanMsg) {
	if msg.Err == nil {
		return
	}
	m.ShowNotification(fmt.Sprintf("The command %s failed: %v", msg.Label, msg.Err), "error", m.Settings.NotificationDuration)
}

// commandScratchSpec is the scratch spec of a scratch entry.
func (m *OS) commandScratchSpec(c config.CommandBinding, argv []string) scratchSpec {
	return scratchSpec{
		Name: c.ResolvedName(), Title: c.Label(), Command: argv,
		Width: c.WidthSpec(), Height: c.HeightSpec(),
	}
}

// commandEnv is the variables a command entry runs with.
func (m *OS) commandEnv(dir string) map[string]string {
	env := map[string]string{
		"TUIOS_SESSION":         m.SessionName,
		"TUIOS_ACTIVE_PANE_CWD": dir,
	}
	if w := m.GetFocusedWindow(); w != nil {
		env["TUIOS_ACTIVE_PANE_ID"] = w.ID
	}
	// The socket is this machine's. A pane on another machine has its own
	// daemon's socket set already.
	if m.IsDaemonSession && m.AttachedHost == "" {
		if path, err := session.GetSocketPath(); err == nil {
			env[session.SocketEnv] = path
		}
	}
	return env
}

// remotePaneDir is the focused pane's folder in a session on another
// machine: the path the shell there reported (OSC 7), whatever host it
// names, or "".
func (m *OS) remotePaneDir() string {
	w := m.GetFocusedWindow()
	if w == nil {
		return ""
	}
	return remoteFolder(w.Cwd)
}

// remoteFolder is the path an OSC 7 report names, whatever host it names, or
// "". Nothing checks it: the folder is on the session's machine, not this one.
func remoteFolder(raw string) string {
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme == "file" {
		return u.Path
	}
	if filepath.IsAbs(raw) {
		return raw
	}
	return ""
}

// commandArgv is the argv that runs a command line through sh -c with env
// set. An empty line is an empty argv, which runs the user's shell. The
// variables go through env(1) so the daemon, which spawns the pane, needs no
// new field to carry them.
func commandArgv(line string, env map[string]string) []string {
	return commandArgvFor(runtime.GOOS, line, env)
}

// commandArgvFor is commandArgv for the given GOOS, so a test can read the
// Windows form.
func commandArgvFor(goos, line string, env map[string]string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if goos == "windows" {
		// cmd has no env(1). set "K=V" sets one variable, and && runs the
		// next part only after it.
		var b strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&b, "set \"%s=%s\" && ", k, env[k])
		}
		b.WriteString(line)
		return []string{"cmd", "/C", b.String()}
	}
	argv := []string{"env"}
	for _, k := range keys {
		argv = append(argv, k+"="+env[k])
	}
	return append(argv, "sh", "-c", line)
}

// runCommandShell runs a shell entry with no window and waits for it. Its
// output goes nowhere. A start failure or a non-zero exit is the error.
func runCommandShell(argv []string, dir string) error {
	if len(argv) == 0 {
		return errors.New("the command is empty")
	}
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 - the user's own config.toml names the command
	cmd.Dir = dir
	// Nil, not io.Discard: io.Discard makes Go copy from a pipe, and a child
	// the command put in the background holds the pipe open, so Run waited
	// for that child too. Nil is /dev/null, which nobody waits on.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.Env = os.Environ()
	// A child that still holds an output after the command exits is let go
	// after this long.
	cmd.WaitDelay = time.Second
	// A session of its own: no controlling terminal, so sudo or a write to
	// /dev/tty cannot draw on the tuios screen.
	detachFromTerminal(cmd)
	return cmd.Run()
}

// commandPopupRequest is what the popup call needs, copied off the model.
type commandPopupRequest struct {
	Session, Title string
	Command        []string
	Dir            string
	Width, Height  string
	Workspace      int
}

// openCommandPopup asks the daemon for a popup that closes when the command
// exits, as tuios popup does.
func openCommandPopup(req commandPopupRequest) error {
	c, err := session.DialVerbClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	params := map[string]any{
		"session":   req.Session,
		"name":      req.Title,
		"command":   req.Command,
		"width":     req.Width,
		"height":    req.Height,
		"workspace": req.Workspace,
	}
	if req.Dir != "" {
		params["cwd"] = req.Dir
	}
	_, err = c.Call("popup", params)
	return err
}

// paletteCategoryCommands is the palette category of the command entries.
const paletteCategoryCommands = "Commands"

// commandPaletteItems is one palette row per command entry, named by its
// description, with its key as the shortcut.
func (m *OS) commandPaletteItems() []CommandPaletteItem {
	if m.UserConfig == nil {
		return nil
	}
	cmds := m.UserConfig.Keybindings.Commands()
	// A shadowed entry's key runs something else, so the row shows no key.
	dead := map[string]bool{}
	if m.KeybindRegistry != nil {
		for _, b := range m.KeybindRegistry.Bindings() {
			if b.Section == config.SectionCommand && b.Shadowed {
				dead[b.Action] = true
			}
		}
	}
	items := make([]CommandPaletteItem, 0, len(cmds))
	for _, c := range cmds {
		action := c.Action()
		shortcut := ""
		if !dead[action] {
			shortcut = commandShortcut(c)
		}
		items = append(items, CommandPaletteItem{
			Name:     c.Label(),
			Shortcut: shortcut,
			Category: paletteCategoryCommands,
			Action: func(m *OS) (*OS, tea.Cmd) {
				return m, m.RunCommandBinding(action)
			},
		})
	}
	return items
}

// commandShortcut is an entry's key as the palette spells the built-in keys:
// prefix+ and then the key in its canonical form.
func commandShortcut(c config.CommandBinding) string {
	key := config.CanonicalKey(c.BareKey())
	if c.Section() == config.SectionPrefixMode {
		return "prefix+" + key
	}
	return key
}
