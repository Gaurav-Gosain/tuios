package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
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

// installer holds what one tuios plugins install command shares between
// plugins: the folders, config.toml, the flags and the signal handler. It
// exists while the install lock is held.
type installer struct {
	cmd   *cobra.Command
	out   io.Writer
	dirs  herdrplugin.Dirs
	cfg   config.PluginsConfig
	o     installOpts
	sig   *signals
	state *herdrplugin.State
	// configPath is config.toml.
	configPath string
}

// newInstaller takes the install lock, then reads config.toml and the state
// file: one tuios plugins command at a time builds or switches a plugin.
// The kernel drops the lock when the process ends, so a killed install
// never blocks the next one. Fetch folders a killed install left are
// removed here.
func newInstaller(cmd *cobra.Command, o installOpts) (*installer, func(), error) {
	configPath, err := pluginsConfigPath()
	if err != nil {
		return nil, nil, err
	}
	in := &installer{cmd: cmd, out: cmd.OutOrStdout(), configPath: configPath, o: o, sig: trapSignals()}
	managed := herdrplugin.DefaultDirs(configPath, nil).ManagedDir()
	release, err := herdrplugin.HoldLock(filepath.Join(managed, herdrplugin.InstallLockName))
	if err != nil {
		return nil, nil, err
	}
	if in.cfg, err = config.PluginsInFile(configPath); err == nil {
		in.dirs = herdrplugin.DefaultDirs(configPath, in.cfg.Dirs)
		in.state, err = herdrplugin.ReadState(in.dirs.StatePath())
	}
	if err != nil {
		release()
		return nil, nil, err
	}
	old, _ := filepath.Glob(filepath.Join(managed, herdrplugin.FetchPrefix+"*"))
	for _, d := range old {
		_ = os.RemoveAll(d)
	}
	return in, release, nil
}

// fetch gets ref of url into a new fetch folder and loads the manifest. The
// caller removes the folder: finish moves it into place before the build,
// so after a good install nothing is left to remove.
func (in *installer) fetch(url, ref, subdir string) (string, *herdrplugin.LockEntry, *herdrplugin.Plugin, error) {
	work, err := os.MkdirTemp(in.dirs.ManagedDir(), herdrplugin.FetchPrefix)
	if err != nil {
		return "", nil, nil, err
	}
	e := &herdrplugin.LockEntry{Format: "herdr", Source: url, Ref: ref, Subdir: subdir}
	if e.Commit, e.Tree, err = fetchInto(work, url, ref, subdir); err != nil {
		return work, nil, nil, err
	}
	p, err := loadInside(filepath.Join(work, filepath.FromSlash(subdir)), work)
	return work, e, p, err
}

// finish shows the plan and asks. Then it builds into a new build folder
// and commits: it writes the state file, then plugins.lock when the entry
// changed, and makes the daemon run the new folder. Before the state
// write, a failure or a kill leaves the old folder running, and the new
// folder is garbage that the next install removes. A kill between the two
// writes leaves the state a commit ahead of the lock, and Discover then
// runs the previous folder, which the state keeps. fetched is the fetch
// folder, "" for a local folder. e.Approved, when set, is what the person
// approved before.
func (in *installer) finish(e herdrplugin.LockEntry, p *herdrplugin.Plugin, fetched string) error {
	out := in.out
	hash := herdrplugin.TrustHash(e.Source, p, e.Grants)
	changed := e.Approved != "" && e.Approved != hash
	rel := ""
	if fetched != "" {
		rel = herdrplugin.NewDir(e)
	}
	fmt.Fprint(out, installPlan(p, e, in.dirs.PluginRoot(e, rel), in.o))
	tty := term.IsTerminal(int(os.Stdin.Fd()))
	if changed {
		fmt.Fprintln(out, "\nThe plugin manifest is different from the approved one in plugins.lock. tuios asks again, also with --yes.")
		if !tty {
			return errors.New("there is no terminal to ask in. Run the command in a terminal")
		}
	}
	ok, err := lookPrompt{yes: in.o.yes && !changed, tty: tty, in: os.Stdin}.confirm(out, "Continue?")
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("the plugin is not installed. You did not approve it")
	}
	// From here a signal cancels the build instead of ending tuios.
	in.sig.busy(true)
	defer in.sig.busy(false)
	dir := ""
	if fetched != "" {
		// The fetch folder becomes the build folder before the build, and
		// it never moves again.
		dir = in.dirs.BuildDir(rel)
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return err
		}
		if err := herdrplugin.Rename(fetched, dir); err != nil {
			return err
		}
	}
	fail := func(err error) error {
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
		return err
	}
	root := in.dirs.PluginRoot(e, rel)
	if p, err = loadInside(root, dir); err != nil {
		return fail(err)
	}
	built := !slices.ContainsFunc(p.Build, func(b herdrplugin.Command) bool { return p.Supported(b.Platforms, "build") == nil })
	if !in.o.noBuild && !built {
		if err := runPluginBuild(in.sig.ctx, in.cmd, p, in.dirs); err != nil {
			return fail(fmt.Errorf("%w. The plugin is not installed", err))
		}
		// A build step can write the manifest. What runs must be what the
		// person approved.
		if p, err = loadInside(root, dir); err != nil {
			return fail(err)
		}
		if herdrplugin.TrustHash(e.Source, p, e.Grants) != hash {
			return fail(errors.New("the build changed the plugin manifest. The plugin is not installed"))
		}
		built = true
	}
	e.Approved = hash
	if dir != "" {
		st := in.state.Find(e.ID)
		if st == nil {
			in.state.Plugins = append(in.state.Plugins, herdrplugin.StateEntry{ID: e.ID})
			st = &in.state.Plugins[len(in.state.Plugins)-1]
		}
		if st.Dir != "" {
			st.PrevCommit, st.PrevDir = st.Commit, st.Dir
		}
		st.Commit, st.Dir, st.Built = e.Commit, rel, built
		if err := herdrplugin.WriteState(in.dirs.StatePath(), in.state); err != nil {
			return fail(err)
		}
	}
	lock, err := herdrplugin.ReadLock(in.dirs.LockPath())
	if err != nil {
		return err
	}
	if x := lock.Find(e.ID); x == nil || !reflect.DeepEqual(*x, e) {
		if err := herdrplugin.UpdateLock(in.dirs.LockPath(), func(l *herdrplugin.Lock) {
			if x := l.Find(e.ID); x != nil {
				*x = e
			} else {
				l.Plugins = append(l.Plugins, e)
			}
		}); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "Installed %s", e.ID)
	if e.Commit != "" {
		fmt.Fprintf(out, " at %.12s", e.Commit)
	}
	fmt.Fprintln(out, ". plugins.lock pins it.")
	in.restart(e.ID, !in.o.noEnable, "", root, rel)
	return nil
}

// restart makes the daemon run id from the folder the state file names
// now, with plugin.restart, which changes no config. A plugin the daemon
// does not run is enabled when enable is set. unless is a plugin folder:
// the daemon skips the restart when the plugin already runs from it. root
// is the plugin folder in rel. When nothing can run an older folder any
// more, the old build folders of id go, except rel and the one before it.
func (in *installer) restart(id string, enable bool, unless, root, rel string) {
	params := map[string]any{"plugin_id": id, "unless_root": unless}
	res, running, err := pluginCall("plugin.restart", params, 30*time.Second)
	code := herdrCode(err)
	if code == "plugin_disabled" && enable {
		if _, _, err = pluginCall("plugin.enable", params, 10*time.Second); err == nil {
			fmt.Fprintf(in.out, "The plugin %s is enabled. It runs with your rights.\n", id)
			params["unless_root"] = root
			res, _, err = pluginCall("plugin.restart", params, 30*time.Second)
			code = herdrCode(err)
		}
	}
	switch {
	case err == nil && !running:
		// No daemon: the next one starts the plugin from the new folder.
		if enable {
			if _, err := config.SetPluginEnabledInFile(in.configPath, id, true); err != nil {
				fmt.Fprintf(in.out, "tuios could not enable the plugin %s: %v.\n", id, err)
				return
			}
			fmt.Fprintf(in.out, "The plugin %s is enabled. It starts with the next tuios daemon.\n", id)
		}
	case err == nil:
		if res["restarted"] == true {
			fmt.Fprintf(in.out, "The plugin %s runs from the new folder.\n", id)
		}
	case code == "plugin_disabled":
		// Off, and it stays off. Its panes may still run, so its folders
		// stay until an install can ask about them.
		return
	case code == "plugin_not_found" || code == "unsupported":
		if enable {
			_, _ = config.SetPluginEnabledInFile(in.configPath, id, true)
		}
		fmt.Fprintf(in.out, "This tuios daemon is older than this command. Restart the tuios daemon to start the plugin %s.\n", id)
		return
	default:
		fmt.Fprintf(in.out, "tuios could not restart the plugin %s: %v. Restart the tuios daemon, or run this command in a terminal outside tuios.\n", id, err)
		return
	}
	if rel == "" {
		return
	}
	var panes []string
	if list, ok := res["pane_roots"].([]any); ok {
		for _, r := range list {
			if s, ok := r.(string); ok {
				panes = append(panes, s)
			}
		}
	}
	in.collect(id, panes)
}

// collect removes the build folders of id that the state file does not
// name as the live or the previous one, and that no open plugin pane runs
// from. A folder that cannot be removed, such as one with a file in use on
// Windows, stays until the next install.
func (in *installer) collect(id string, panes []string) {
	keep := map[string]bool{}
	if st := in.state.Find(id); st != nil {
		keep[path.Base(st.Dir)], keep[path.Base(st.PrevDir)] = true, true
	}
	pdir := in.dirs.PluginDir(id)
	ents, _ := os.ReadDir(pdir)
	for _, d := range ents {
		folder := filepath.Join(pdir, d.Name())
		if !d.IsDir() || keep[d.Name()] || slices.ContainsFunc(panes, func(r string) bool { return herdrplugin.Inside(r, folder) }) {
			continue
		}
		_ = os.RemoveAll(folder)
	}
}

// herdrCode is the code of a daemon error from pluginCall, "" for none.
func herdrCode(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if i := strings.LastIndexByte(msg, '('); i >= 0 && strings.HasSuffix(msg, ")") {
		return msg[i+1 : len(msg)-1]
	}
	return "error"
}

// signals handles SIGINT, SIGTERM and SIGHUP during an install. While no
// build runs, a signal ends tuios: the kernel drops the install lock, and
// the next install removes what this one left. During the build and the
// commit, it cancels ctx: that kills the build, and the install removes
// its new build folder.
type signals struct {
	ctx     context.Context
	mu      sync.Mutex
	working bool
}

func trapSignals() *signals {
	ctx, cancel := context.WithCancel(context.Background())
	s := &signals{ctx: ctx}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-ch
		cancel()
		s.mu.Lock()
		if !s.working {
			os.Exit(130)
		}
		s.mu.Unlock()
	}()
	return s
}

func (s *signals) busy(on bool) { s.mu.Lock(); s.working = on; s.mu.Unlock() }

// installPlugin is tuios plugins install SOURCE.
func installPlugin(cmd *cobra.Command, arg string, o installOpts) error {
	src, err := parsePluginSource(arg, o)
	if err != nil {
		return err
	}
	in, release, err := newInstaller(cmd, o)
	if err != nil {
		return err
	}
	defer release()
	var e *herdrplugin.LockEntry
	var p *herdrplugin.Plugin
	fetched := ""
	if src.local != "" {
		if p, err = loadInside(src.local, ""); err != nil {
			return err
		}
		e = &herdrplugin.LockEntry{Format: "herdr", Source: herdrplugin.LocalPrefix + p.PluginRoot}
	} else {
		fmt.Fprintf(in.out, "tuios gets %s.\n", shownSource(src.url))
		fetched, e, p, err = in.fetch(src.url, src.ref, src.subdir)
		defer os.RemoveAll(fetched)
		if err != nil {
			return err
		}
	}
	e.ID = p.PluginID
	if err := herdrplugin.CheckLockEntry(*e); err != nil {
		return err
	}
	lock, err := herdrplugin.ReadLock(in.dirs.LockPath())
	if err != nil {
		return err
	}
	old := lock.Find(e.ID)
	if err := checkInstallConflict(herdrplugin.Discover(in.dirs, in.cfg.Enabled), old, *e, o.replace); err != nil {
		return err
	}
	if old != nil && old.Source == e.Source && old.Commit == e.Commit && old.Subdir == e.Subdir && !e.Local() && in.ready(*old) {
		fmt.Fprintf(in.out, "The plugin %s is already installed at %.12s.\n", e.ID, e.Commit)
		return nil
	}
	return in.finish(*e, p, fetched)
}

// ready reports whether le is built on this machine. Then it makes the
// daemon run the plugin from that folder, if it runs it from another one.
func (in *installer) ready(le herdrplugin.LockEntry) bool {
	rel, built, ok := in.state.Live(le)
	if !ok || !built || !dirExists(in.dirs.BuildDir(rel)) {
		return false
	}
	root := in.dirs.PluginRoot(le, rel)
	in.restart(le.ID, !in.o.noEnable, root, root, rel)
	return true
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
		f("%s  %s", head, shownArgv(c.Command...))
		if p.Supported(c.Platforms, "build") != nil {
			f("   (not here: %s only)", strings.Join(p.EffectivePlatforms(c.Platforms), ", "))
		}
		f("\n")
		head = ""
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
func runPluginBuild(ctx context.Context, cmd *cobra.Command, p *herdrplugin.Plugin, dirs herdrplugin.Dirs) error {
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
		bctx, cancel := context.WithTimeout(ctx, buildTimeout)
		c := exec.Command(prog, b.Command[1:]...) //nolint:gosec // the person approved this build
		c.Dir = p.PluginRoot
		c.Stdout, c.Stderr = both, io.MultiWriter(errw, log)
		c.Env = buildEnv(os.Environ())
		c.WaitDelay = 5 * time.Second
		err := herdrplugin.RunTree(bctx, c)
		if errors.Is(bctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("it ran for more than %v", buildTimeout)
		}
		cancel()
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
	o.noEnable = true
	in, release, err := newInstaller(cmd, o)
	if err != nil {
		return err
	}
	defer release()
	lock, err := herdrplugin.ReadLock(in.dirs.LockPath())
	if err != nil {
		return err
	}
	if len(lock.Plugins) == 0 {
		fmt.Fprintf(in.out, "%s lists no plugins. Run tuios plugins install SOURCE to install one.\n", in.dirs.LockPath())
		return nil
	}
	var failed []string
	for _, e := range lock.Plugins {
		if in.sig.ctx.Err() == nil {
			err = in.installPinned(e)
		}
		if err != nil || in.sig.ctx.Err() != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", e.ID, err)
			failed = append(failed, e.ID)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("these plugins are not installed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// installPinned builds one lock file entry on this machine, in a new build
// folder, unless it is built already.
func (in *installer) installPinned(e herdrplugin.LockEntry) error {
	if err := herdrplugin.CheckLockEntry(e); err != nil {
		return err
	}
	if e.Local() {
		fmt.Fprintf(in.out, "%s uses the folder %s. tuios does not get it.\n", e.ID, in.dirs.PluginRoot(e, ""))
		return nil
	}
	if in.ready(e) {
		fmt.Fprintf(in.out, "%s is installed at %.12s.\n", e.ID, e.Commit)
		return nil
	}
	fmt.Fprintf(in.out, "tuios gets %s at %.12s.\n", shownSource(e.Source), e.Commit)
	fetched, got, p, err := in.fetch(e.Source, e.Commit, e.Subdir)
	defer os.RemoveAll(fetched)
	if err != nil {
		return fmt.Errorf("%w. If the pinned commit is gone, install the plugin again from its source", err)
	}
	if p.PluginID != e.ID {
		return fmt.Errorf("the commit names the plugin %s, not %s", p.PluginID, e.ID)
	}
	e.Tree = got.Tree
	return in.finish(e, p, fetched)
}
