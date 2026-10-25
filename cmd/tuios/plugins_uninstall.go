package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/spf13/cobra"
)

// tuios plugins uninstall: undo tuios plugins install. It disables the
// plugin (the daemon stops its processes and closes its panes), drops its
// plugins.lock entry, its state file entry and its approval, and removes its
// build folders under the managed folder. It never removes a folder it did
// not make: a local install keeps its folder, and a plugin from [plugins]
// dirs, the tuios plugins folder or herdr is refused.

func newPluginsUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall ID",
		Short: "Disable and remove a plugin that tuios plugins install installed",
		Long: `Disable and remove a plugin that tuios plugins install installed.

tuios stops the processes of the plugin and closes its panes. It removes the plugin from plugins.lock and removes its folder from the tuios data folder. It also removes the approval of the plugin on this machine.

ID is the plugin id. You can also give the source that you installed the plugin from.

tuios keeps the config and state folders of the plugin. A plugin installed from a folder on this machine keeps that folder. tuios does not remove a plugin that it did not install. For a linked folder, use tuios plugins unlink.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inPane() {
				return errFromPane("uninstall")
			}
			return uninstallPlugin(cmd.OutOrStdout(), args[0])
		},
	}
}

// uninstallPlugin is tuios plugins uninstall ARG.
func uninstallPlugin(out io.Writer, arg string) error {
	configPath, err := pluginsConfigPath()
	if err != nil {
		return err
	}
	cfg, err := config.PluginsInFile(configPath)
	if err != nil {
		return err
	}
	dirs := herdrplugin.DefaultDirs(configPath, cfg.Dirs)
	lock, err := herdrplugin.ReadLock(dirs.LockPath())
	if err != nil {
		return err
	}
	state, err := herdrplugin.ReadState(dirs.StatePath())
	if err != nil {
		return err
	}
	id, err := installedID(arg, lock, state, herdrplugin.Discover(dirs, cfg.Enabled))
	if err != nil {
		return err
	}

	release, err := herdrplugin.HoldLock(filepath.Join(dirs.ManagedDir(), herdrplugin.InstallLockName))
	if err != nil {
		return err
	}
	defer release()
	// Read again under the lock: an install may have run since.
	if lock, err = herdrplugin.ReadLock(dirs.LockPath()); err != nil {
		return err
	}
	if state, err = herdrplugin.ReadState(dirs.StatePath()); err != nil {
		return err
	}
	entry := lock.Find(id)
	// Check the folder before anything changes.
	folder, real, err := pluginCheckout(dirs, id)
	if err != nil {
		return err
	}

	if err := disableForUninstall(out, configPath, id); err != nil {
		return err
	}
	if entry != nil {
		if err := herdrplugin.UpdateLock(dirs.LockPath(), func(l *herdrplugin.Lock) {
			l.Plugins = slices.DeleteFunc(l.Plugins, func(e herdrplugin.LockEntry) bool { return e.ID == id })
		}); err != nil {
			return err
		}
		fmt.Fprintf(out, "The plugin %s is removed from plugins.lock.\n", id)
	}
	if state.Find(id) != nil {
		state.Plugins = slices.DeleteFunc(state.Plugins, func(e herdrplugin.StateEntry) bool { return e.ID == id })
		if err := herdrplugin.WriteState(dirs.StatePath(), state); err != nil {
			return err
		}
	}
	if err := herdrplugin.Forget(dirs.ApprovalsPath(), id); err != nil {
		return err
	}
	if entry != nil && entry.Local() {
		fmt.Fprintf(out, "tuios keeps the folder %s. You installed the plugin from it.\n", strings.TrimPrefix(entry.Source, herdrplugin.LocalPrefix))
	}
	// A local install has no folder under the managed folder, so real is
	// "" for it.
	if real != "" {
		if err := os.RemoveAll(real); err != nil {
			return fmt.Errorf("tuios could not remove all of %s: %w. Close the programs that use it, then run the command again", folder, err)
		}
		fmt.Fprintf(out, "The folder %s is removed.\n", folder)
	}
	var kept []string
	for _, d := range []string{dirs.ConfigDir(id), dirs.StateDir(id)} {
		if dirExists(d) {
			kept = append(kept, d)
		}
	}
	if len(kept) > 0 {
		fmt.Fprintf(out, "tuios keeps the config and state of the plugin in %s.\n", strings.Join(kept, " and "))
	}
	fmt.Fprintf(out, "The plugin %s is uninstalled.\n", id)
	return nil
}

// installedID finds the id of the installed plugin that arg names: its id,
// or the source it was installed from. A plugin that tuios plugins install
// did not install is refused with the command that removes it.
func installedID(arg string, lock *herdrplugin.Lock, state *herdrplugin.State, entries []herdrplugin.Entry) (string, error) {
	if lock.Find(arg) != nil {
		return arg, nil
	}
	if le, err := lockBySource(arg, lock); err != nil || le != nil {
		if err != nil {
			return "", err
		}
		return le.ID, nil
	}
	if e := herdrplugin.Find(entries, arg); e != nil {
		switch e.Origin {
		case herdrplugin.OriginConfig:
			return "", fmt.Errorf("tuios did not install the plugin %s. Its folder is in [plugins] dirs in config.toml. Nothing was changed. Run tuios plugins unlink %s", arg, arg)
		case herdrplugin.OriginTuios:
			return "", fmt.Errorf("tuios did not install the plugin %s. Its folder is in the tuios plugins folder: %s. Nothing was changed. Run tuios plugins disable %s, then remove the folder", arg, filepath.Dir(e.Path), arg)
		case herdrplugin.OriginHerdr, herdrplugin.OriginHerdrManaged:
			return "", fmt.Errorf("herdr installed the plugin %s, not tuios. Nothing was changed. Remove it with herdr. To stop it in tuios, run tuios plugins disable %s", arg, arg)
		}
	}
	// What a killed uninstall left: no lock entry, and a state entry or a
	// folder that only an install makes.
	if id, ok := herdrplugin.NormalizeID(arg); ok && id == arg && state.Find(id) != nil {
		return id, nil
	}
	return "", fmt.Errorf("plugins.lock has no plugin with the id or the source %s. Run tuios plugins list to see the ids", arg)
}

// lockBySource is the lock entry installed from arg, a SOURCE as tuios
// plugins install takes it. The ref in arg is ignored. A repository with no
// folder matches its one entry, if it has only one.
func lockBySource(arg string, lock *herdrplugin.Lock) (*herdrplugin.LockEntry, error) {
	src, err := parsePluginSource(arg, installOpts{})
	if err != nil {
		return nil, nil
	}
	want := src.url
	if src.local != "" {
		want = herdrplugin.LocalPrefix + src.local
	}
	var same []*herdrplugin.LockEntry
	for i := range lock.Plugins {
		le := &lock.Plugins[i]
		if !sameSource(le.Source, want) {
			continue
		}
		if src.local != "" || le.Subdir == src.subdir {
			return le, nil
		}
		same = append(same, le)
	}
	switch {
	case len(same) == 1 && src.subdir == "":
		return same[0], nil
	case len(same) > 1 && src.subdir == "":
		ids := make([]string, len(same))
		for i, le := range same {
			ids[i] = le.ID
		}
		return nil, fmt.Errorf("plugins.lock has more than one plugin from %s: %s. Give the plugin id", shownSource(want), strings.Join(ids, ", "))
	}
	return nil, nil
}

// sameSource compares two sources, with a trailing / or .git ignored.
func sameSource(a, b string) bool {
	norm := func(s string) string {
		return strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	}
	return norm(a) == norm(b)
}

// disableForUninstall turns the plugin off. A running daemon applies it at
// once: it stops the plugin's processes and closes its panes. With no
// daemon, config.toml is changed.
func disableForUninstall(out io.Writer, configPath, id string) error {
	res, running, err := pluginCall("plugin.disable", map[string]any{"plugin_id": id, "close_panes": true}, 30*time.Second)
	switch {
	case herdrCode(err) == "plugin_not_found":
		// The daemon does not list it, so it runs nothing of it.
		running = false
	case err != nil:
		return fmt.Errorf("tuios could not disable the plugin %s: %w. Nothing was removed", id, err)
	}
	if !running {
		if _, err := config.SetPluginEnabledInFile(configPath, id, false); err != nil {
			return err
		}
		fmt.Fprintf(out, "The plugin %s is disabled.\n", id)
		return nil
	}
	fmt.Fprintf(out, "The plugin %s is disabled. Its processes are stopped.\n", id)
	// herdrcli decodes numbers as json.Number.
	raw, ok := res["panes_closed"].(json.Number)
	n, err := raw.Int64()
	switch {
	case !ok || err != nil:
		fmt.Fprintf(out, "This tuios daemon is older than this command, and it does not close plugin panes. Close the panes of %s yourself.\n", id)
	case n == 1:
		fmt.Fprintln(out, "Its pane is closed.")
	case n > 1:
		fmt.Fprintf(out, "Its %d panes are closed.\n", n)
	}
	return nil
}

// pluginCheckout finds the folder that holds the build folders of id. real
// is that folder with its links resolved, and "" when there is none. It is
// an error unless real is the one folder for id directly in the managed
// folder, so the caller never removes anything outside it.
func pluginCheckout(dirs herdrplugin.Dirs, id string) (folder, real string, err error) {
	if n, ok := herdrplugin.NormalizeID(id); !ok || n != id {
		return "", "", fmt.Errorf("the plugin id %q is not valid", id)
	}
	folder = dirs.PluginDir(id)
	info, err := os.Lstat(folder)
	if errors.Is(err, os.ErrNotExist) {
		return folder, "", nil
	}
	if err != nil {
		return folder, "", err
	}
	refuse := fmt.Errorf("tuios does not remove %s, because it is not a plugin folder in %s. Nothing was changed. Remove it yourself", folder, dirs.ManagedDir())
	if !info.IsDir() {
		return folder, "", refuse
	}
	managed, err := filepath.EvalSymlinks(dirs.ManagedDir())
	if err != nil {
		return folder, "", err
	}
	if real, err = filepath.EvalSymlinks(folder); err != nil {
		return folder, "", err
	}
	if rel, err := filepath.Rel(managed, real); err != nil || rel != herdrplugin.PathComponent(id) || !filepath.IsLocal(rel) {
		return folder, "", refuse
	}
	return folder, real, nil
}
