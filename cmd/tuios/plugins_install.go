package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// tuios plugins install: fetch a plugin with the system git, pin the commit
// in plugins.lock, build it in this process and enable it. The daemon never
// fetches and never builds. It reads the lock file and the checkouts, the
// managed origin of herdrplugin.Discover. See internal/herdrplugin/lock.go.

// buildTimeout bounds one build command.
const buildTimeout = 10 * time.Minute

type installOpts struct {
	ref, subdir                     string
	yes, noEnable, noBuild, replace bool
}

func newPluginsInstallCommand() *cobra.Command {
	var o installOpts
	cmd := &cobra.Command{
		Use:   "install [SOURCE]",
		Short: "Install, build and enable a plugin from git or a folder",
		Long: `Install a plugin, build it and enable it.

SOURCE is owner/repo[/dir][@ref] on github.com, a git URL (https, ssh, git@host:path or file) with an optional @ref, or a folder on this machine. Without a ref, tuios uses the default branch. tuios writes the commit to plugins.lock in the config folder.

Without SOURCE, tuios installs each plugin in plugins.lock at its commit. It does not enable them.

tuios shows what the plugin runs and asks first. A plugin runs with your rights. You need git.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inPane() {
				return errFromPane("install")
			}
			if len(args) == 0 {
				return installFromLock(cmd, o)
			}
			return installPlugin(cmd, args[0], o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.ref, "ref", "", "The branch, tag or full commit to install. Same as SOURCE@REF")
	f.StringVar(&o.subdir, "subdir", "", "The plugin folder in the repository")
	f.BoolVarP(&o.yes, "yes", "y", false, "Install without the approval question")
	f.BoolVar(&o.noEnable, "no-enable", false, "Install and build, but do not enable")
	f.BoolVar(&o.noBuild, "no-build", false, "Do not run the build commands")
	f.BoolVar(&o.replace, "replace", false, "Replace an installed plugin with the same id from another source")
	return cmd
}

// pluginSource is a parsed SOURCE argument.
type pluginSource struct {
	local  string // absolute folder, for a local install
	url    string // what git fetches
	ref    string
	subdir string
}

// shownSource is a source as the plan and the list show it.
func shownSource(src string) string {
	if s, ok := strings.CutPrefix(src, "https://"); ok {
		return strings.TrimSuffix(s, ".git")
	}
	return strings.TrimPrefix(src, herdrplugin.LocalPrefix)
}

// parsePluginSource reads SOURCE. --ref and --subdir win over the ones in
// SOURCE.
func parsePluginSource(arg string, o installOpts) (pluginSource, error) {
	var s pluginSource
	p := herdrplugin.ExpandHome(arg)
	_, statErr := os.Stat(p)
	if strings.HasPrefix(arg, ".") || strings.HasPrefix(arg, "~") || strings.HasPrefix(arg, "/") || filepath.IsAbs(p) || statErr == nil {
		if o.ref != "" || o.subdir != "" {
			return s, errors.New("--ref and --subdir need a git source. tuios uses a folder as it is")
		}
		var err error
		s.local, err = filepath.Abs(p)
		return s, err
	}
	if err := herdrplugin.CheckSource(arg); err != nil {
		return s, err
	}
	// pathAt is where the path starts, after scheme://host or the colon of
	// git@host:path. An @ before it is a user name. The first @ after it
	// starts the ref, so a tag such as pkg@1.0 keeps its own @.
	pathAt := 0
	if i := strings.Index(arg, "://"); i >= 0 {
		pathAt = i + 3 + strings.IndexByte(arg[i+3:]+"/", '/')
	} else if i := strings.IndexByte(arg, ':'); i >= 0 {
		pathAt = i + 1
	}
	body := arg
	if i := strings.IndexByte(arg[pathAt:], '@'); i >= 0 {
		body, s.ref = arg[:pathAt+i], arg[pathAt+i+1:]
	}
	if pathAt > 0 {
		s.url = body
	} else {
		parts := strings.Split(body, "/")
		if len(parts) < 2 {
			return s, fmt.Errorf("no plugin is named %s in the registry. Use owner/repo or a git URL", arg)
		}
		for _, part := range parts[:2] {
			if part == "" || part == "." || part == ".." {
				return s, fmt.Errorf("%q is not owner/repo", arg)
			}
		}
		s.url = "https://github.com/" + parts[0] + "/" + parts[1]
		s.subdir = strings.Join(parts[2:], "/")
	}
	if o.ref != "" {
		s.ref = o.ref
	}
	if o.subdir != "" {
		s.subdir = strings.Trim(o.subdir, "/")
	}
	if err := herdrplugin.CheckRef(s.ref); err != nil {
		return s, err
	}
	return s, herdrplugin.CheckSubdir(s.subdir)
}

// git runs the system git. Only the transports a plugin source may name
// are allowed.
func git(dir string, args ...string) (string, error) {
	out, err := review.Git(context.Background(), dir, []string{"GIT_ALLOW_PROTOCOL=https:ssh:git:file"}, args...)
	if err != nil {
		if _, lerr := exec.LookPath("git"); lerr != nil {
			return "", errors.New("tuios plugins install needs git, and git is not on your PATH. Install git, then try again")
		}
		msg, _, _ := strings.Cut(err.Error(), "\n")
		return "", errors.New(msg)
	}
	return strings.TrimSpace(out), nil
}

// newStaging makes an empty folder in the managed folder to fetch into. It
// first removes staging folders older than an hour, which an install that
// was killed left behind.
func newStaging(dirs herdrplugin.Dirs) (string, error) {
	if err := os.MkdirAll(dirs.ManagedDir(), 0o700); err != nil {
		return "", err
	}
	old, _ := filepath.Glob(filepath.Join(dirs.ManagedDir(), ".staging-*"))
	for _, d := range old {
		if info, err := os.Stat(d); err == nil && time.Since(info.ModTime()) > time.Hour {
			_ = os.RemoveAll(d)
		}
	}
	return os.MkdirTemp(dirs.ManagedDir(), ".staging-")
}

// fetchInto fetches ref of url into dir, an empty folder, and checks out
// the commit. "" is the remote's default branch. It returns the commit and
// git's id of subdir at it, which must be a folder.
func fetchInto(dir, url, ref, subdir string) (commit, tree string, err error) {
	want := ref
	if want == "" {
		want = "HEAD"
	}
	rev := "FETCH_HEAD^{commit}"
	if herdrplugin.IsCommit(ref) {
		rev = ref + "^{commit}"
	}
	_, err = git("", "init", "-q", dir)
	if err == nil {
		_, err = git(dir, "remote", "add", "origin", url)
	}
	if err == nil {
		_, err = git(dir, "fetch", "-q", "--depth", "1", "--no-tags", "--", "origin", want)
		if err != nil && herdrplugin.IsCommit(ref) {
			// A host that refuses a fetch by commit gets a full fetch.
			_, err = git(dir, "fetch", "-q", "--no-tags", "--", "origin", "+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*")
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("tuios cannot get %s. Check the network, the URL and the ref: %w", shownSource(url), err)
	}
	if commit, err = git(dir, "rev-parse", "--verify", "-q", rev); err != nil {
		return "", "", fmt.Errorf("the commit %.12s is not on %s", ref, shownSource(url))
	}
	treeOf := commit + "^{tree}"
	if subdir != "" {
		treeOf = commit + ":" + subdir
		if t, _ := git(dir, "cat-file", "-t", treeOf); t != "tree" {
			return "", "", fmt.Errorf("the repository has no folder %s at %.12s", subdir, commit)
		}
	}
	if tree, err = git(dir, "rev-parse", "--verify", treeOf); err == nil {
		_, err = git(dir, "-c", "advice.detachedHead=false", "checkout", "-q", "--detach", commit)
	}
	return commit, tree, err
}

// loadInside loads the manifest in root. When checkout is not "", the
// manifest must be in it after its links are resolved, so a link cannot
// move the plugin, and its build, out of the repository.
func loadInside(root, checkout string) (*herdrplugin.Plugin, error) {
	p, perr := herdrplugin.Load(root)
	if perr != nil {
		return nil, fmt.Errorf("the plugin cannot be read: %s: %s", perr.Code, perr.Msg)
	}
	if checkout != "" && !herdrplugin.Inside(p.ManifestPath, checkout) {
		return nil, errors.New("the plugin manifest is a link to a file outside the repository. tuios does not install it")
	}
	return p, nil
}

// installJob is the part of an install that installPlugin and installPinned
// share: approve, build, write the lock entry, enable.
type installJob struct {
	o installOpts
	// work is the checkout the build runs in. "" for a local folder.
	work string
	// fetched is true when this run made work, so a failure removes it.
	fetched bool
	// restart stops the plugin before it is enabled, because it runs now.
	restart bool
	// drop is a checkout to remove after the install, from a source the
	// install replaced.
	drop string
}

// finish shows the plan, asks, builds, writes the lock entry and enables
// the plugin. e.Approved, when set, is what the person approved before.
func (j installJob) finish(cmd *cobra.Command, dirs herdrplugin.Dirs, configPath string, e herdrplugin.LockEntry, p *herdrplugin.Plugin) error {
	out := cmd.OutOrStdout()
	final := ""
	if !e.Local() {
		final = dirs.CheckoutDir(e)
		// Two installs of one plugin at one commit take turns.
		g, err := herdrplugin.Exclusive(final + ".lock")
		if err != nil {
			return err
		}
		defer func() { _ = g.Close(); _ = os.Remove(final + ".lock") }()
	}
	work := j.work
	fail := func(err error) error {
		if j.fetched {
			_ = os.RemoveAll(work)
		}
		return err
	}
	hash := herdrplugin.TrustHash(e.Source, p, e.Grants)
	changed := e.Approved != "" && e.Approved != hash
	fmt.Fprint(out, installPlan(p, e, dirs.PluginRoot(e), j.o))
	tty := term.IsTerminal(int(os.Stdin.Fd()))
	if changed {
		fmt.Fprintln(out, "\nThe plugin manifest is different from the approved one in plugins.lock. tuios asks again, also with --yes.")
		if !tty {
			return fail(errors.New("there is no terminal to ask in. Run the command in a terminal"))
		}
	}
	ok, err := lookPrompt{yes: j.o.yes && !changed, tty: tty, in: os.Stdin}.confirm(out, "Continue?")
	if err != nil {
		return fail(err)
	}
	if !ok {
		return fail(errors.New("the plugin is not installed. You did not approve it"))
	}
	if j.fetched && !dirExists(final) {
		// The build runs in the folder the plugin keeps, because a build
		// can write its absolute path into what it makes.
		if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
			return fail(err)
		}
		if err := os.Rename(work, final); err != nil {
			return fail(err)
		}
		work = final
	}
	if !j.o.noBuild {
		root := dirs.PluginRoot(e)
		if work != "" {
			root = filepath.Join(work, filepath.FromSlash(e.Subdir))
		}
		if p, err = loadInside(root, work); err != nil {
			return fail(err)
		}
		if err := runPluginBuild(cmd, p, dirs); err != nil {
			return fail(fmt.Errorf("%w. The plugin is not installed", err))
		}
		// A build step can write the manifest. What runs must be what the
		// person approved.
		if p, err = loadInside(root, work); err != nil {
			return fail(err)
		}
		if herdrplugin.TrustHash(e.Source, p, e.Grants) != hash {
			return fail(errors.New("the build changed the plugin manifest. The plugin is not installed"))
		}
		e.Built = true
	}
	if work != final {
		// Built beside a checkout that runs now: swap the two only after
		// the build worked.
		old := fmt.Sprintf("%s.old-%d", final, os.Getpid())
		if err := os.Rename(final, old); err != nil {
			return fail(err)
		}
		if err := os.Rename(work, final); err != nil {
			_ = os.Rename(old, final)
			return fail(err)
		}
		_ = os.RemoveAll(old)
		work = final
	}
	e.Approved = hash
	if err := herdrplugin.UpdateLock(dirs.LockPath(), func(l *herdrplugin.Lock) {
		if x := l.Find(e.ID); x != nil {
			*x = e
		} else {
			l.Plugins = append(l.Plugins, e)
		}
	}); err != nil {
		return fail(err)
	}
	if j.drop != "" && j.drop != final {
		_ = os.RemoveAll(j.drop)
	}
	fmt.Fprintf(out, "Installed %s", e.ID)
	if e.Commit != "" {
		fmt.Fprintf(out, " at %.12s", e.Commit)
	}
	fmt.Fprintln(out, ". plugins.lock pins it.")
	if j.o.noEnable {
		return nil
	}
	return enablePlugin(out, configPath, e.ID, j.restart)
}

// enablePlugin enables id through the daemon, else in config.toml. restart
// stops it first, so a plugin that runs starts again from the new folder.
func enablePlugin(out io.Writer, configPath, id string, restart bool) error {
	params := map[string]any{"plugin_id": id}
	if restart {
		_, _, _ = pluginCall("plugin.disable", params, 10*time.Second)
	}
	_, running, err := pluginCall("plugin.enable", params, 10*time.Second)
	old := err != nil && (strings.Contains(err.Error(), "(plugin_not_found)") || strings.Contains(err.Error(), "(unsupported)"))
	switch {
	case err != nil && !old:
		return err
	case running && !old:
		fmt.Fprintf(out, "The plugin %s is enabled. It runs with your rights.\n", id)
		return nil
	}
	// No daemon, or a daemon that does not find the plugin: an older one,
	// or one that runs no plugins.
	if _, err := config.SetPluginEnabledInFile(configPath, id, true); err != nil {
		return err
	}
	if old {
		fmt.Fprintf(out, "The plugin %s is enabled. Restart the tuios daemon to start the plugin.\n", id)
	} else {
		fmt.Fprintf(out, "The plugin %s is enabled. It starts with the next tuios daemon.\n", id)
	}
	return nil
}

// installEnv reads config.toml for the folders.
func installEnv() (herdrplugin.Dirs, config.PluginsConfig, string, error) {
	configPath, err := pluginsConfigPath()
	if err != nil {
		return herdrplugin.Dirs{}, config.PluginsConfig{}, "", err
	}
	cfg, err := config.PluginsInFile(configPath)
	return herdrplugin.DefaultDirs(configPath, cfg.Dirs), cfg, configPath, err
}

// installPlugin is tuios plugins install SOURCE.
func installPlugin(cmd *cobra.Command, arg string, o installOpts) error {
	out := cmd.OutOrStdout()
	src, err := parsePluginSource(arg, o)
	if err != nil {
		return err
	}
	dirs, cfg, configPath, err := installEnv()
	if err != nil {
		return err
	}
	e := herdrplugin.LockEntry{Format: "herdr", Installed: time.Now().UTC().Truncate(time.Second)}
	j := installJob{o: o}
	var p *herdrplugin.Plugin
	if src.local != "" {
		if p, err = loadInside(src.local, ""); err != nil {
			return err
		}
		e.Source = herdrplugin.LocalPrefix + p.PluginRoot
	} else {
		staging, err := newStaging(dirs)
		if err != nil {
			return err
		}
		defer os.RemoveAll(staging)
		fmt.Fprintf(out, "tuios gets %s.\n", shownSource(src.url))
		if e.Commit, e.Tree, err = fetchInto(staging, src.url, src.ref, src.subdir); err != nil {
			return err
		}
		if p, err = loadInside(filepath.Join(staging, filepath.FromSlash(src.subdir)), staging); err != nil {
			return err
		}
		e.Source, e.Ref, e.Subdir = src.url, src.ref, src.subdir
		j.work, j.fetched = staging, true
	}
	e.ID = p.PluginID
	if err := herdrplugin.CheckLockEntry(e); err != nil {
		return err
	}
	lock, err := herdrplugin.ReadLock(dirs.LockPath())
	if err != nil {
		return err
	}
	old := lock.Find(e.ID)
	if err := checkInstallConflict(herdrplugin.Discover(dirs, cfg.Enabled), old, e, o.replace); err != nil {
		return err
	}
	if old != nil {
		if old.Source == e.Source && old.Commit == e.Commit && old.Subdir == e.Subdir && old.Built && !e.Local() && dirExists(dirs.PluginRoot(*old)) {
			fmt.Fprintf(out, "The plugin %s is already installed at %.12s.\n", e.ID, e.Commit)
			if o.noEnable {
				return nil
			}
			return enablePlugin(out, configPath, e.ID, false)
		}
		j.restart = slices.Contains(cfg.Enabled, e.ID)
		if !old.Local() {
			j.drop = dirs.CheckoutDir(*old)
		}
		if old.Source == e.Source && old.Subdir == e.Subdir {
			// The same source at a new commit: the previous checkout stays
			// until update learns to keep two folders.
			j.drop = ""
		}
	}
	return j.finish(cmd, dirs, configPath, e, p)
}

// checkInstallConflict refuses an install that another plugin with the same
// id would hide, or that replaces another source without --replace. A copy
// herdr installed is found after the lock file, so the install hides it.
func checkInstallConflict(entries []herdrplugin.Entry, old *herdrplugin.LockEntry, e herdrplugin.LockEntry, replace bool) error {
	for _, x := range entries {
		if x.ID != e.ID || (x.Origin != herdrplugin.OriginConfig && x.Origin != herdrplugin.OriginTuios) {
			continue
		}
		if e.Local() && herdrplugin.SamePath(filepath.Dir(x.Path), strings.TrimPrefix(e.Source, herdrplugin.LocalPrefix)) {
			continue
		}
		return fmt.Errorf("another plugin with the id %s is in %s. Unlink or remove it first", e.ID, filepath.Dir(x.Path))
	}
	if old != nil && (old.Source != e.Source || old.Subdir != e.Subdir) && !replace {
		return fmt.Errorf("the plugin %s is installed from %s. To install it from %s, add --replace", e.ID, shownSource(old.Source), shownSource(e.Source))
	}
	return nil
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// shownArgv is an argv as the plan shows it. An argument with a character
// that is not printable is quoted, so it cannot change what the terminal
// shows.
func shownArgv(argv ...string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = a
		if strings.ContainsFunc(a, func(r rune) bool { return !unicode.IsPrint(r) }) {
			out[i] = strconv.Quote(a)
		}
	}
	return strings.Join(out, " ")
}

// installPlan is what the person approves: facts only.
func installPlan(p *herdrplugin.Plugin, e herdrplugin.LockEntry, folder string, o installOpts) string {
	var b strings.Builder
	f := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	f("\nInstall %s %s (herdr plugin)\n  from    %s", p.PluginID, shownArgv(p.Version), shownSource(e.Source))
	if e.Commit != "" {
		ref := e.Ref
		if ref == "" {
			ref = "default branch"
		}
		f(" @ %s = %.12s", ref, e.Commit)
		if e.Subdir != "" {
			f(", folder %s", e.Subdir)
		}
	}
	f("\n  folder  %s\n", folder)
	head := "\nBuild commands (run now, in that folder, with your rights):\n"
	if o.noBuild {
		head = "\nBuild commands (not run now, because of --no-build):\n"
	}
	for _, c := range p.Build {
		if p.Supported(c.Platforms, "build") == nil {
			f("%s  %s\n", head, shownArgv(c.Command...))
			head = ""
		}
	}
	f("\nRuns when enabled, outside every pane, with your rights:\n")
	n := b.Len()
	for _, s := range p.Startup {
		f("  startup  %s\n", shownArgv(s.Command...))
	}
	var hooks, actions, panes, links []string
	for _, ev := range p.Events {
		hooks = append(hooks, ev.On)
	}
	for _, a := range p.Actions {
		actions = append(actions, a.ID)
	}
	for _, x := range p.Panes {
		panes = append(panes, x.ID+" ("+x.Placement+")")
	}
	for _, h := range p.LinkHandlers {
		links = append(links, h.ID)
	}
	for i, list := range [][]string{hooks, actions, panes, links} {
		if len(list) > 0 {
			f("  %-8s %s (%d)\n", [...]string{"hooks", "actions", "panes", "links"}[i], shownArgv(strings.Join(list, ", ")), len(list))
		}
	}
	if b.Len() == n {
		f("  nothing\n")
	}
	f("\nIt can call tuios with the default pane grants.\n")
	if !o.noEnable {
		f("tuios enables the plugin after the install.\n")
	}
	return b.String()
}

// runPluginBuild runs p's build commands for this platform in its folder,
// one at a time. Each command is printed first. Its output goes to the
// terminal and to build.log in the plugin's state folder.
func runPluginBuild(cmd *cobra.Command, p *herdrplugin.Plugin, dirs herdrplugin.Dirs) error {
	out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()
	logPath := filepath.Join(dirs.StateDir(p.PluginID), "build.log")
	var log io.Writer = io.Discard
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err == nil {
		if f, err := os.Create(logPath); err == nil { //nolint:gosec // the plugin's own state folder
			defer f.Close()
			log = f
		}
	}
	both := io.MultiWriter(out, log)
	ran := 0
	for _, b := range p.Build {
		if p.Supported(b.Platforms, "build") != nil {
			continue
		}
		fmt.Fprintf(both, "Running in %s: %s\n", p.PluginRoot, shownArgv(b.Command...))
		prog, perr := herdrplugin.ResolveProgram(b.Command[0], p.PluginRoot)
		if perr != nil {
			fmt.Fprintln(log, perr.Msg)
			return fmt.Errorf("the build failed: %s. See %s", perr.Msg, logPath)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		ctx, cancel := context.WithTimeout(ctx, buildTimeout)
		c := exec.CommandContext(ctx, prog, b.Command[1:]...) //nolint:gosec // the person approved this build
		c.Dir = p.PluginRoot
		c.Stdout, c.Stderr = both, io.MultiWriter(errw, log)
		c.Env = buildEnv(os.Environ())
		c.WaitDelay = 5 * time.Second
		buildGroup(c)
		err := c.Run()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("it ran for more than %v", buildTimeout)
		}
		cancel()
		stop()
		if err != nil {
			fmt.Fprintln(log, err)
			return fmt.Errorf("the build command failed: %v. See %s", err, logPath)
		}
		ran++
	}
	if ran == 0 {
		fmt.Fprintf(out, "The plugin %s has no build commands for this platform.\n", p.PluginID)
		return nil
	}
	fmt.Fprintf(out, "The plugin %s is built.\n", p.PluginID)
	return nil
}

// installFromLock is tuios plugins install with no SOURCE: each plugin in
// plugins.lock at its pinned commit. It enables nothing.
func installFromLock(cmd *cobra.Command, o installOpts) error {
	dirs, _, configPath, err := installEnv()
	if err != nil {
		return err
	}
	lock, err := herdrplugin.ReadLock(dirs.LockPath())
	if err != nil {
		return err
	}
	if len(lock.Plugins) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "%s lists no plugins. Run tuios plugins install SOURCE to install one.\n", dirs.LockPath())
		return nil
	}
	o.noEnable = true
	var failed []string
	for _, e := range lock.Plugins {
		if err := installPinned(cmd, dirs, configPath, e, o); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", e.ID, err)
			failed = append(failed, e.ID)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("these plugins are not installed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// installPinned puts one lock file entry on this machine. A checkout that
// is there already is built in place and never removed.
func installPinned(cmd *cobra.Command, dirs herdrplugin.Dirs, configPath string, e herdrplugin.LockEntry, o installOpts) error {
	out := cmd.OutOrStdout()
	if err := herdrplugin.CheckLockEntry(e); err != nil {
		return err
	}
	if e.Local() {
		fmt.Fprintf(out, "%s uses the folder %s. tuios does not get it.\n", e.ID, dirs.PluginRoot(e))
		return nil
	}
	if e.Built && dirExists(dirs.PluginRoot(e)) {
		fmt.Fprintf(out, "%s is installed at %.12s.\n", e.ID, e.Commit)
		return nil
	}
	j := installJob{o: o, work: dirs.CheckoutDir(e)}
	if !dirExists(j.work) {
		staging, err := newStaging(dirs)
		if err != nil {
			return err
		}
		defer os.RemoveAll(staging)
		fmt.Fprintf(out, "tuios gets %s at %.12s.\n", shownSource(e.Source), e.Commit)
		if _, _, err := fetchInto(staging, e.Source, e.Commit, e.Subdir); err != nil {
			return fmt.Errorf("%w. If the pinned commit is gone, install the plugin again from its source", err)
		}
		j.work, j.fetched = staging, true
	}
	p, err := loadInside(filepath.Join(j.work, filepath.FromSlash(e.Subdir)), j.work)
	if err == nil && p.PluginID != e.ID {
		err = fmt.Errorf("the commit names the plugin %s, not %s", p.PluginID, e.ID)
	}
	if err != nil {
		return err
	}
	return j.finish(cmd, dirs, configPath, e, p)
}
