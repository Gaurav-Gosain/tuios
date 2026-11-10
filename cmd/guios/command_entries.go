package main

import (
	"encoding/json"
	"os/exec"
	"strings"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// The tuios config's [[keybindings.command]] entries (parity gap 18c), as
// the TUI runs them (internal/app/command_keys.go RunCommandBinding): a
// popup, a scratch pane, a pane of the layout, a shell line, or a plugin
// action. guios listed none of them and sent their keys to run-command,
// which only a TUI answers. Each is a palette row now and its key runs it.

// commandEntries are the config's command entries, or none while no keymap
// is read.
func (a *App) commandEntries() []config.CommandBinding {
	if a.tk.reg == nil || a.tk.reg.GetConfig() == nil {
		return nil
	}
	return a.tk.reg.GetConfig().Keybindings.Commands()
}

// commandEntryItems are the palette rows of the command entries, named by their
// description, with their key.
func (a *App) commandEntryItems() []paletteItem {
	var out []paletteItem
	for _, c := range a.commandEntries() {
		out = append(out, paletteItem{title: c.Label(), detail: c.Command, keys: [2]string{"", a.prefixKeys(c.Action())},
			words: "command " + c.ResolvedType() + " " + c.Command + " " + c.Description,
			run:   func() { a.runCommandEntry(c) }})
	}
	return out
}

// runCommandAction runs the command entry an action names
// (config.CommandActionPrefix), as its key does.
func (a *App) runCommandAction(action string) bool {
	if a.tk.reg == nil || a.tk.reg.GetConfig() == nil {
		return false
	}
	c, ok := a.tk.reg.GetConfig().Keybindings.CommandFor(action)
	if !ok {
		return false
	}
	a.runCommandEntry(c)
	return true
}

// commandArgv is the line as the TUI runs it: sh -c, with the session, the
// focused pane and its folder in the environment (commandEnv), through
// env(1) so no shell quoting is needed.
func commandArgv(line, session, pane, dir string) []string {
	argv := []string{"env", "TUIOS_SESSION=" + session, "TUIOS_ACTIVE_PANE_CWD=" + dir}
	if pane != "" {
		argv = append(argv, "TUIOS_ACTIVE_PANE_ID="+pane)
	}
	return append(argv, "/bin/sh", "-c", line)
}

// runCommandEntry runs one command entry for the shown session.
func (a *App) runCommandEntry(c config.CommandBinding) {
	s := a.session(a.selected)
	if s == nil {
		a.toast(locales.LauncherNoSession())
		return
	}
	pane := a.focusOf(s)
	dir := a.workDir(pane)
	name := s.Name
	label := c.Label()
	line := strings.TrimSpace(c.Command)
	fail := func(_ json.RawMessage, err error) {
		if err != nil {
			a.toast(locales.CommandEntryFailed(label, err.Error()))
		}
	}
	switch c.ResolvedType() {
	case config.CommandTypePane:
		// A pane of the layout beside the focused one, as a new window
		// opens; it closes when the command exits.
		a.verbOn(s.ID, "new-window", map[string]any{"name": label, "command": commandArgv(line, name, pane, dir), "cwd": dir}, fail)
	case config.CommandTypeScratch:
		args := map[string]any{"name": label, "scratch": true, "scratch_name": c.ResolvedName(), "width": c.WidthSpec(), "height": c.HeightSpec(), "cwd": dir}
		if line != "" {
			args["command"] = commandArgv(line, name, pane, dir)
		}
		a.toggleEntryScratch(s, c.ResolvedName(), args, fail)
	case config.CommandTypeShell:
		// The line runs on this machine with no pane, as the TUI's shell
		// entries do, and its output goes nowhere.
		cmd := exec.Command("/bin/sh", "-c", line)
		cmd.Env = append(cmd.Environ(), "TUIOS_SESSION="+name, "TUIOS_ACTIVE_PANE_CWD="+dir, "TUIOS_ACTIVE_PANE_ID="+pane)
		if !a.hostOf(s.ID).remote() {
			cmd.Dir = dir
		}
		if err := cmd.Start(); err != nil {
			a.toast(locales.CommandEntryFailed(label, err.Error()))
			return
		}
		go func() { _ = cmd.Wait() }()
		a.toast(locales.CommandEntryRan(label))
	case config.CommandTypePluginAction:
		a.invokePluginAction(s, "", line, label)
	default: // popup
		// guios draws no floating box, and the daemon opens a popup only
		// for a TUI that draws it. Its nearest is the scratch dropdown: the
		// entry runs as a scratch group of its own, shown over the panes,
		// and goes when the command exits, as the popup does.
		group := "popup-" + c.ResolvedName()
		a.toggleEntryScratch(s, group, map[string]any{"name": label, "scratch": true, "scratch_name": group, "command": commandArgv(line, name, pane, dir),
			"width": c.WidthSpec(), "height": c.HeightSpec(), "cwd": dir}, fail)
	}
}

// toggleEntryScratch shows the scratch group name of a command entry, as
// the TUI's entry toggles it: hidden while it shows, focused while it runs
// hidden, and started with args while it does not run. The dropdown follows
// the daemon's focus (quickChanged), so a focus on the group shows it.
func (a *App) toggleEntryScratch(s *proto.Session, name string, args map[string]any, fail func(json.RawMessage, error)) {
	q := &a.quick
	p := namedScratch(s, name)
	if p != nil && p.ID == s.Focus {
		a.hideDrop()
		return
	}
	if !scratchFocused(s) {
		q.back = keysPane(a, s)
	}
	if p != nil {
		a.call(s.ID, "focus", proto.FocusParams{Session: s.ID, Pane: p.ID}, nil)
		return
	}
	a.verbOn(s.ID, "popup", args, fail)
}
