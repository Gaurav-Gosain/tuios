package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
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
	ref, subdir                 string
	yes, noEnable, noBuild, rep bool
}

func newPluginsInstallCommand() *cobra.Command {
	var o installOpts
	cmd := &cobra.Command{
		Use:   "install [SOURCE]",
		Short: "Install, build and enable a plugin from git or a folder",
		Long: `Install a plugin, build it and enable it.

SOURCE is one of these:
  ./dir, /dir, ~/dir     a folder on this machine. tuios uses it in place.
  owner/repo[/dir][@ref] a repository on github.com.
  URL[@ref]              a git URL: https://, ssh://, git@host:path or file://.

With no ref, tuios uses the default branch. tuios writes the commit to plugins.lock in the config folder. The checkout goes in the tuios data folder.

With no SOURCE, tuios installs each plugin in plugins.lock at its pinned commit. It does not enable them.

tuios shows the build commands and what the plugin runs, then asks. A plugin runs with your rights. You need git on your PATH.`,
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
	f.StringVar(&o.subdir, "subdir", "", "The plugin folder inside the repository")
	f.BoolVarP(&o.yes, "yes", "y", false, "Do not ask")
	f.BoolVar(&o.noEnable, "no-enable", false, "Install and build, but do not enable")
	f.BoolVar(&o.noBuild, "no-build", false, "Do not run the build commands")
	f.BoolVar(&o.rep, "replace", false, "Replace an installed plugin with the same id from another source")
	return cmd
}

// pluginSource is a parsed SOURCE argument.
type pluginSource struct {
	local  string // absolute folder, for a local install
	url    string // what git fetches
	ref    string
	subdir string
}

// shown is the source as the plan and list print it.
func (s pluginSource) shown() string {
	if s.local != "" {
		return s.local
	}
	return shownSource(s.url)
}

func shownSource(src string) string {
	src = strings.TrimPrefix(src, herdrplugin.LocalPrefix)
	return strings.TrimSuffix(strings.TrimPrefix(src, "https://"), ".git")
}

// lockSource is the source the lock file keeps.
func (s pluginSource) lockSource() string {
	if s.local != "" {
		return herdrplugin.LocalPrefix + s.local
	}
	return s.url
}

// parsePluginSource reads SOURCE. ref and subdir come from the flags and
// win over the ones in SOURCE.
func parsePluginSource(arg, ref, subdir string) (pluginSource, error) {
	var s pluginSource
	if arg == "" || strings.HasPrefix(arg, "-") || strings.ContainsAny(arg, "\x00\n\r") {
		return s, fmt.Errorf("%q is not a plugin source", arg)
	}
	_, statErr := os.Stat(expandTilde(arg))
	if strings.HasPrefix(arg, ".") || strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, "~") || statErr == nil {
		abs, err := filepath.Abs(expandTilde(arg))
		if err != nil {
			return s, err
		}
		s.local = abs
		return s, nil
	}
	// pathAt is where the path part starts, after a scheme and host or the
	// colon of git@host:path. An @ before it is a user name. The last @
	// after it starts the ref.
	pathAt := 0
	switch {
	case strings.Contains(arg, "://"):
		i := strings.Index(arg, "://") + 3
		if j := strings.IndexByte(arg[i:], '/'); j >= 0 {
			pathAt = i + j
		} else {
			pathAt = len(arg)
		}
	case strings.Contains(arg, ":"):
		pathAt = strings.IndexByte(arg, ':') + 1
	}
	body := arg
	if i := strings.LastIndexByte(arg[pathAt:], '@'); i >= 0 {
		body, s.ref = arg[:pathAt+i], arg[pathAt+i+1:]
	}
	if pathAt > 0 {
		s.url = body
	} else {
		parts := strings.Split(body, "/")
		if len(parts) < 2 {
			return s, fmt.Errorf("no plugin is named %s in the registry. Use owner/repo or a git URL", arg)
		}
		for _, p := range parts[:2] {
			if p == "" || p == "." || p == ".." || strings.ContainsAny(p, " \\:") {
				return s, fmt.Errorf("%q is not owner/repo", arg)
			}
		}
		s.url = "https://github.com/" + parts[0] + "/" + parts[1]
		s.subdir = strings.Join(parts[2:], "/")
	}
	if ref != "" {
		s.ref = ref
	}
	if subdir != "" {
		s.subdir = strings.Trim(subdir, "/")
	}
	if err := herdrplugin.CheckRef(s.ref); err != nil {
		return s, err
	}
	if err := herdrplugin.CheckSubdir(s.subdir); err != nil {
		return s, err
	}
	return s, nil
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// git runs the system git and returns its output. The error holds git's
// message.
func git(dir string, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	c := childproc.Command("git", args...)
	env := os.Environ()[:0:0]
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_DIR=") && !strings.HasPrefix(kv, "GIT_WORK_TREE=") && !strings.HasPrefix(kv, "GIT_INDEX_FILE=") {
			env = append(env, kv)
		}
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		env = append(env, "GIT_TERMINAL_PROMPT=0")
	}
	c.Env = env
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		msg = strings.TrimPrefix(msg, "fatal: ")
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func needGit() error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("tuios plugins install needs git, and git is not on your PATH. Install git, then try again")
	}
	return nil
}

// resolveRef asks the remote what ref points to. It returns the commit, the
// full ref name to fetch, and the ref to keep in the lock file. It runs
// before anything is written, so an offline install changes nothing.
func resolveRef(url, ref string) (commit, fetch, keep string, err error) {
	if herdrplugin.IsCommit(ref) {
		if _, err := git("", "ls-remote", "--", url, "HEAD"); err != nil {
			return "", "", "", fmt.Errorf("cannot reach %s. Check the network and the URL: %w", shownSource(url), err)
		}
		return ref, ref, ref, nil
	}
	args := []string{"ls-remote", "--", url, ref}
	if ref == "" {
		args = []string{"ls-remote", "--symref", "--", url, "HEAD"}
	}
	out, err := git("", args...)
	if err != nil {
		return "", "", "", fmt.Errorf("cannot reach %s. Check the network and the URL: %w", shownSource(url), err)
	}
	refs := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		sha, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if target, isSym := strings.CutPrefix(sha, "ref: "); isSym && name == "HEAD" {
			keep = strings.TrimPrefix(target, "refs/heads/")
			fetch = target
			continue
		}
		refs[name] = sha
	}
	if ref == "" {
		if fetch == "" {
			fetch, keep = "HEAD", "HEAD"
		}
		if c := refs["HEAD"]; herdrplugin.IsCommit(c) {
			return c, fetch, keep, nil
		}
		return "", "", "", fmt.Errorf("%s has no default branch. Give a ref with @REF", shownSource(url))
	}
	for _, name := range []string{"refs/tags/" + ref + "^{}", "refs/tags/" + ref, "refs/heads/" + ref, ref} {
		if c := refs[name]; herdrplugin.IsCommit(c) {
			return c, strings.TrimSuffix(name, "^{}"), ref, nil
		}
	}
	return "", "", "", fmt.Errorf("%s has no branch or tag named %s. Use a branch, a tag or a full commit", shownSource(url), ref)
}

// checkout puts commit of url into dir, a new folder. fetch is a ref name
// or the commit. A shallow fetch comes first. A host that refuses it gets a
// full fetch.
func checkout(dir, url, fetch, commit string) error {
	if _, err := git("", "init", "-q", dir); err != nil {
		return err
	}
	if _, err := git(dir, "remote", "add", "origin", url); err != nil {
		return err
	}
	has := func() bool {
		_, err := git(dir, "rev-parse", "-q", "--verify", commit+"^{commit}")
		return err == nil
	}
	_, ferr := git(dir, "fetch", "-q", "--depth", "1", "--no-tags", "origin", fetch)
	if !has() {
		if _, err := git(dir, "fetch", "-q", "--no-tags", "origin", "+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*"); err != nil && ferr == nil {
			ferr = err
		}
	}
	if !has() {
		if ferr != nil {
			return ferr
		}
		return fmt.Errorf("the commit %.12s is not on %s. Its branch can have moved: try again", commit, shownSource(url))
	}
	_, err := git(dir, "-c", "advice.detachedHead=false", "checkout", "-q", "--detach", commit)
	return err
}

// installPlugin is tuios plugins install SOURCE.
func installPlugin(cmd *cobra.Command, arg string, o installOpts) error {
	out := cmd.OutOrStdout()
	src, err := parsePluginSource(arg, o.ref, o.subdir)
	if err != nil {
		return err
	}
	configPath, err := pluginsConfigPath()
	if err != nil {
		return err
	}
	cfg, err := config.PluginsInFile(configPath)
	if err != nil {
		return err
	}
	dirs := herdrplugin.DefaultDirs(configPath, cfg.Dirs)
	entry := herdrplugin.LockEntry{Format: "herdr", Source: src.lockSource(), Installed: time.Now().UTC().Truncate(time.Second)}

	var root, staging string
	if src.local != "" {
		root = src.local
	} else {
		if err := needGit(); err != nil {
			return err
		}
		commit, fetch, keep, err := resolveRef(src.url, src.ref)
		if err != nil {
			return err
		}
		entry.Ref, entry.Commit, entry.Subdir = keep, commit, src.subdir
		if err := os.MkdirAll(dirs.ManagedDir(), 0o700); err != nil {
			return err
		}
		staging = filepath.Join(dirs.ManagedDir(), fmt.Sprintf(".staging-%d", os.Getpid()))
		_ = os.RemoveAll(staging)
		defer func() {
			if staging != "" {
				_ = os.RemoveAll(staging)
			}
		}()
		fmt.Fprintf(out, "Fetching %s at %.12s.\n", src.shown(), commit)
		if err := checkout(staging, src.url, fetch, commit); err != nil {
			return err
		}
		treeOf := commit + "^{tree}"
		if src.subdir != "" {
			treeOf = commit + ":" + src.subdir
		}
		if entry.Tree, err = git(staging, "rev-parse", "--verify", treeOf); err != nil {
			return fmt.Errorf("the repository has no folder %s at %.12s", src.subdir, commit)
		}
		root = filepath.Join(staging, filepath.FromSlash(src.subdir))
	}

	p, perr := herdrplugin.Load(root)
	if perr != nil {
		return fmt.Errorf("the plugin cannot be read: %s: %s", perr.Code, perr.Msg)
	}
	entry.ID = p.PluginID
	if src.local != "" {
		src.local = p.PluginRoot
		entry.Source = src.lockSource()
	}
	if err := herdrplugin.CheckLockEntry(entry); err != nil {
		return err
	}
	entries := herdrplugin.Discover(dirs, cfg.Enabled)
	lock, err := herdrplugin.ReadLock(dirs.LockPath())
	if err != nil {
		return err
	}
	old := lock.Find(entry.ID)
	if err := checkInstallConflict(entries, old, entry, o.rep); err != nil {
		return err
	}
	if old != nil && old.Source == entry.Source && old.Commit == entry.Commit && old.Subdir == entry.Subdir && old.Built && !entry.Local() && dirExists(dirs.PluginRoot(*old)) {
		fmt.Fprintf(out, "The plugin %s is already installed at %.12s.\n", entry.ID, entry.Commit)
		return enableInstalled(out, configPath, entry.ID, o, false)
	}

	final := dirs.PluginRoot(entry)
	printInstallPlan(out, p, src, entry, final, o)
	question := "Install, build and enable?"
	switch {
	case o.noEnable && o.noBuild:
		question = "Install?"
	case o.noEnable:
		question = "Install and build?"
	case o.noBuild:
		question = "Install and enable?"
	}
	ok, err := lookPrompt{yes: o.yes, tty: term.IsTerminal(int(os.Stdin.Fd())), in: os.Stdin}.confirm(out, question)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(out, "Nothing was installed.")
		return nil
	}

	var checkoutDir string
	if staging != "" {
		checkoutDir = dirs.CheckoutDir(entry)
		_ = os.RemoveAll(checkoutDir)
		if err := os.MkdirAll(filepath.Dir(checkoutDir), 0o700); err != nil {
			return err
		}
		if err := os.Rename(staging, checkoutDir); err != nil {
			return err
		}
		staging = ""
		if p, perr = herdrplugin.Load(final); perr != nil {
			_ = os.RemoveAll(checkoutDir)
			return fmt.Errorf("the plugin cannot be read: %s: %s", perr.Code, perr.Msg)
		}
	}
	entry.Approved = herdrplugin.TrustHash(entry.Source, p, entry.Grants)
	if !o.noBuild {
		if err := runPluginBuild(cmd, p, dirs); err != nil {
			if checkoutDir != "" {
				_ = os.RemoveAll(checkoutDir)
			}
			return fmt.Errorf("%w. Nothing was installed", err)
		}
		entry.Built = true
	}
	if err := herdrplugin.UpdateLock(dirs.LockPath(), func(l *herdrplugin.Lock) error {
		l.Put(entry)
		return nil
	}); err != nil {
		if checkoutDir != "" {
			_ = os.RemoveAll(checkoutDir)
		}
		return err
	}
	if old != nil && !old.Local() && old.Commit != entry.Commit {
		// The previous checkout stays until update learns to keep two
		// folders. Only a replaced source frees its folder now.
		if old.Source != entry.Source {
			_ = os.RemoveAll(dirs.CheckoutDir(*old))
		}
	}
	at := ""
	if entry.Commit != "" {
		at = fmt.Sprintf(" at %.12s", entry.Commit)
	}
	fmt.Fprintf(out, "Installed %s%s. plugins.lock pins it.\n", entry.ID, at)
	return enableInstalled(out, configPath, entry.ID, o, true)
}

// enableInstalled enables id unless --no-enable says not to.
func enableInstalled(out io.Writer, configPath, id string, o installOpts, fresh bool) error {
	if o.noEnable {
		if fresh {
			fmt.Fprintf(out, "It is off. Run tuios plugins enable %s to run it.\n", id)
		}
		return nil
	}
	_, running, err := pluginCall("plugin.enable", map[string]any{"plugin_id": id}, 10*time.Second)
	if err != nil {
		return err
	}
	if !running {
		if _, err := config.SetPluginEnabledInFile(configPath, id, true); err != nil {
			return err
		}
		fmt.Fprintf(out, "The plugin %s is enabled. It starts with the next tuios daemon.\n", id)
		return nil
	}
	fmt.Fprintf(out, "The plugin %s is enabled. It runs with your rights.\n", id)
	return nil
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

// printInstallPlan shows what the person approves: facts only.
func printInstallPlan(w io.Writer, p *herdrplugin.Plugin, src pluginSource, e herdrplugin.LockEntry, folder string, o installOpts) {
	fmt.Fprintf(w, "\nInstall %s %s (herdr plugin)\n", p.PluginID, p.Version)
	from := src.shown()
	if e.Commit != "" {
		from = fmt.Sprintf("%s @ %s = %.12s", from, e.Ref, e.Commit)
		if e.Subdir != "" {
			from += ", folder " + e.Subdir
		}
	}
	fmt.Fprintf(w, "  from    %s\n", from)
	fmt.Fprintf(w, "  folder  %s\n", folder)
	var build []string
	for _, b := range p.Build {
		if p.Supported(b.Platforms, "build") == nil {
			build = append(build, strings.Join(b.Command, " "))
		}
	}
	switch {
	case o.noBuild && len(build) > 0:
		fmt.Fprintf(w, "\nBuild commands (not run now, because of --no-build):\n  %s\n", strings.Join(build, "\n  "))
	case len(build) > 0:
		fmt.Fprintf(w, "\nBuild commands (run now, in that folder, with your rights):\n  %s\n", strings.Join(build, "\n  "))
	}
	when := "Runs when enabled"
	if o.noEnable {
		when = "Runs after you enable it"
	}
	fmt.Fprintf(w, "\n%s, outside every pane, with your rights:\n", when)
	n := 0
	for _, s := range p.Startup {
		fmt.Fprintf(w, "  startup  %s\n", strings.Join(s.Command, " "))
		n++
	}
	list := func(label string, names []string) {
		if len(names) > 0 {
			fmt.Fprintf(w, "  %-8s %s (%d)\n", label, strings.Join(names, ", "), len(names))
			n++
		}
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
	list("hooks", hooks)
	list("actions", actions)
	list("panes", panes)
	list("links", links)
	if n == 0 {
		fmt.Fprintln(w, "  nothing")
	}
	fmt.Fprintln(w, "\nIt can call tuios with the default pane grants.")
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
	ran := 0
	for _, b := range p.Build {
		if p.Supported(b.Platforms, "build") != nil {
			continue
		}
		line := fmt.Sprintf("Running in %s: %s\n", p.PluginRoot, strings.Join(b.Command, " "))
		fmt.Fprint(out, line)
		fmt.Fprint(log, line)
		prog, perr := herdrplugin.ResolveProgram(b.Command[0], p.PluginRoot)
		if perr != nil {
			fmt.Fprintln(log, perr.Msg)
			return fmt.Errorf("the build failed: %s. See %s", perr.Msg, logPath)
		}
		ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
		c := exec.CommandContext(ctx, prog, b.Command[1:]...) //nolint:gosec // the person approved this build
		c.Dir = p.PluginRoot
		c.Stdout, c.Stderr = io.MultiWriter(out, log), io.MultiWriter(errw, log)
		c.Env = buildEnv(os.Environ())
		c.WaitDelay = 5 * time.Second
		err := c.Run()
		timedOut := ctx.Err() != nil
		cancel()
		if err != nil {
			if timedOut {
				err = fmt.Errorf("it ran for more than %v", buildTimeout)
			}
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
	out := cmd.OutOrStdout()
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
	if len(lock.Plugins) == 0 {
		fmt.Fprintf(out, "%s lists no plugins. Run tuios plugins install SOURCE to install one.\n", dirs.LockPath())
		return nil
	}
	var failed []string
	for _, e := range lock.Plugins {
		if err := installPinned(cmd, dirs, e, o); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", e.ID, err)
			failed = append(failed, e.ID)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("these plugins are not installed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// installPinned puts one lock file entry on this machine.
func installPinned(cmd *cobra.Command, dirs herdrplugin.Dirs, e herdrplugin.LockEntry, o installOpts) error {
	out := cmd.OutOrStdout()
	if err := herdrplugin.CheckLockEntry(e); err != nil {
		return err
	}
	root := dirs.PluginRoot(e)
	if e.Local() {
		fmt.Fprintf(out, "%s uses the folder %s. tuios does not fetch it.\n", e.ID, root)
		return nil
	}
	if e.Built && dirExists(root) {
		fmt.Fprintf(out, "%s is installed at %.12s.\n", e.ID, e.Commit)
		return nil
	}
	if err := needGit(); err != nil {
		return err
	}
	dir := dirs.CheckoutDir(e)
	if !dirExists(root) {
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return err
		}
		fmt.Fprintf(out, "Fetching %s at %.12s.\n", shownSource(e.Source), e.Commit)
		if err := checkout(dir, e.Source, e.Commit, e.Commit); err != nil {
			_ = os.RemoveAll(dir)
			return fmt.Errorf("%w. If the pinned commit is gone from %s, install the plugin again from its source", err, shownSource(e.Source))
		}
	}
	fail := func(err error) error {
		_ = os.RemoveAll(dir)
		return err
	}
	p, perr := herdrplugin.Load(root)
	if perr != nil {
		return fail(fmt.Errorf("the plugin cannot be read: %s: %s", perr.Code, perr.Msg))
	}
	if p.PluginID != e.ID {
		return fail(fmt.Errorf("the commit names the plugin %s, not %s", p.PluginID, e.ID))
	}
	src := pluginSource{url: e.Source, ref: e.Ref, subdir: e.Subdir}
	printInstallPlan(out, p, src, e, root, installOpts{noEnable: true, noBuild: o.noBuild})
	yes := o.yes
	if herdrplugin.TrustHash(e.Source, p, e.Grants) != e.Approved {
		fmt.Fprintln(out, "\nThis is not what plugins.lock says you approved. tuios asks even with --yes.")
		yes = false
	}
	ok, err := lookPrompt{yes: yes, tty: term.IsTerminal(int(os.Stdin.Fd())), in: os.Stdin}.confirm(out, "Install and build?")
	if err != nil {
		return fail(err)
	}
	if !ok {
		return fail(errors.New("you said no"))
	}
	if !o.noBuild {
		if err := runPluginBuild(cmd, p, dirs); err != nil {
			return fail(err)
		}
	}
	return herdrplugin.UpdateLock(dirs.LockPath(), func(l *herdrplugin.Lock) error {
		if x := l.Find(e.ID); x != nil && x.Commit == e.Commit {
			x.Built = !o.noBuild
			x.Approved = herdrplugin.TrustHash(e.Source, p, e.Grants)
		}
		return nil
	})
}
