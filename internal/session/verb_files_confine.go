package session

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/adrg/xdg"
	"golang.org/x/text/unicode/norm"
)

// Where a file read or write that arrives over a link may land.
//
// The file verbs act on this machine for whoever calls them. The person's own
// clients may read and write anywhere the user may. A call that arrived over a
// link comes from another machine, and the link policy (link_policy.go) only
// says whether that machine may use the file verbs at all. A sender that lies,
// or a copy aimed at the wrong folder, must not be able to drop a key into
// ~/.ssh/authorized_keys or a line into ~/.bashrc: that is how a file copy
// becomes a login (croc CVE-2023-43619 is that bug). Nor may it read the keys
// out.
//
// So a write over a link (open-file-stream write, file-commit, file-mkdir,
// file-rename, file-remove, file-abort) is held to three rules:
//
//   - It lands inside one of the write roots: the folders files_roots names
//     for the machine, or the receive folder (config.DefaultFilesRoot) when
//     files_roots says nothing, plus this daemon's drop folder. The check is
//     on the real path, after links, and the write itself goes through an
//     os.Root at that folder, so a link inside the folder cannot carry the
//     write out of it.
//   - It does not land in the deny list below, nor does the deny list sit
//     under it: the folders that hold keys, credentials, shell start files,
//     login items, and tuios's own config and state. A write to a folder that
//     holds one of them, such as a rename of ~/.config, is refused too, so a
//     link cannot carry a denied folder out and back. The check is on the
//     real path, and it folds case and width the way macOS and Windows disks
//     do.
//   - The deny list holds inside every root, so files_roots = ["~"] still
//     keeps a write out of ~/.ssh.
//
// A read over a link (file-read, file-hash, file-walk, open-file-stream read)
// is held to the read deny list: keys, credentials and tuios's own state. A
// read elsewhere in the home folder is allowed, so a copy out works.
//
// The deny check reads the real path before the call. A process on this
// machine that swaps a folder for a link between the check and the write could
// still steer a write within a root, but such a process already runs as the
// user and needs no link to do that.

// linkDenyHome is the write deny list, as paths under the home folder. A write
// to one of them, to anything under it, or to a folder that holds it, is
// refused.
var linkDenyHome = []string{
	".ssh",
	".gnupg",
	".config/tuios",
	".local/state/tuios",
	".local/share/tuios",
	".config/systemd",
	".config/autostart",
	".config/environment.d",
	".config/fish",
	".config/git",
	"Library/LaunchAgents",
	"Library/LaunchDaemons",
	"Library/Application Support/tuios",
	".git-credentials",
	".netrc",
	".aws",
	".azure",
	".kube",
	".docker",
	".gnupg",
	".password-store",
	".claude",
	".codex",
	".config/gh",
	".config/gcloud",
	".config/tuios-web",
	".pam_environment",
	".profile",
	".bash_profile",
	".bash_login",
	".bash_logout",
	".bashrc",
	".bashrc.d",
	".zshenv",
	".zprofile",
	".zshrc",
	".zlogin",
	".zlogout",
	".cshrc",
	".tcshrc",
	".kshrc",
	".mkshrc",
	".xprofile",
	".xsession",
	".xinitrc",
	".config/zsh",
	".gitconfig",
	".inputrc",
}

// linkDenyAbsolute is the write deny list outside the home folder: the crontab
// spools. They are outside every root a link may write, so they are refused
// twice, which is the point.
var linkDenyAbsolute = []string{
	"/var/spool/cron",
	"/var/at/tabs",
	"/var/cron/tabs",
}

// linkReadDenyHome is the read deny list, as paths under the home folder. A
// read of one of these, over a link, is refused, with an exception for the
// public keys in ~/.ssh.
var linkReadDenyHome = []string{
	".ssh",
	".gnupg",
	".aws",
	".azure",
	".config/gcloud",
	".kube",
	".docker/config.json",
	".netrc",
	".git-credentials",
	".config/gh/hosts.yml",
	".password-store",
	"Library/Keychains",
	".config/tuios",
	".local/state/tuios",
	".local/share/tuios",
	"Library/Application Support/tuios",
	".claude/.credentials.json",
	".config/claude/.credentials.json",
	".claude.json",
	".codex/auth.json",
}

// fileFS is where the file verbs write. A zero fileFS is the plain file
// system, for the person's own calls. With root set, every path must be under
// dir, the real path the root was opened at, and every operation goes
// through the root.
type fileFS struct {
	root *os.Root
	dir  string
}

// rel is path relative to the root.
func (f fileFS) rel(path string) (string, error) {
	r, err := filepath.Rel(f.dir, path)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", &fs.PathError{Op: "write", Path: path, Err: fs.ErrPermission}
	}
	return r, nil
}

func (f fileFS) OpenFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	if f.root == nil {
		return os.OpenFile(path, flag, perm)
	}
	r, err := f.rel(path)
	if err != nil {
		return nil, err
	}
	return f.root.OpenFile(r, flag, perm)
}

func (f fileFS) Lstat(path string) (fs.FileInfo, error) {
	if f.root == nil {
		return os.Lstat(path)
	}
	r, err := f.rel(path)
	if err != nil {
		return nil, err
	}
	return f.root.Lstat(r)
}

func (f fileFS) Stat(path string) (fs.FileInfo, error) {
	if f.root == nil {
		return os.Stat(path)
	}
	r, err := f.rel(path)
	if err != nil {
		return nil, err
	}
	return f.root.Stat(r)
}

func (f fileFS) Chmod(path string, mode os.FileMode) error {
	if f.root == nil {
		return os.Chmod(path, mode)
	}
	r, err := f.rel(path)
	if err != nil {
		return err
	}
	return f.root.Chmod(r, mode)
}

func (f fileFS) Chtimes(path string, at, mt time.Time) error {
	if f.root == nil {
		return os.Chtimes(path, at, mt)
	}
	r, err := f.rel(path)
	if err != nil {
		return err
	}
	return f.root.Chtimes(r, at, mt)
}

func (f fileFS) Rename(from, to string) error {
	if f.root == nil {
		return os.Rename(from, to)
	}
	a, err := f.rel(from)
	if err != nil {
		return err
	}
	b, err := f.rel(to)
	if err != nil {
		return err
	}
	return f.root.Rename(a, b)
}

func (f fileFS) Remove(path string) error {
	if f.root == nil {
		return os.Remove(path)
	}
	r, err := f.rel(path)
	if err != nil {
		return err
	}
	return f.root.Remove(r)
}

func (f fileFS) RemoveAll(path string) error {
	if f.root == nil {
		return os.RemoveAll(path)
	}
	r, err := f.rel(path)
	if err != nil {
		return err
	}
	return f.root.RemoveAll(r)
}

func (f fileFS) MkdirAll(path string, perm os.FileMode) error {
	if f.root == nil {
		return os.MkdirAll(path, perm)
	}
	r, err := f.rel(path)
	if err != nil {
		return err
	}
	if r == "." {
		return nil
	}
	return f.root.MkdirAll(r, perm)
}

// Close releases the root. A file opened through it stays open.
func (f fileFS) Close() {
	if f.root != nil {
		_ = f.root.Close()
	}
}

// foldPaths reports whether this system's disks ignore case by default.
var foldPaths = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

// foldKey is p with each component folded the way a case-insensitive disk
// compares it: normalised to NFD, then each rune mapped to the smallest in its
// Unicode simple-fold cycle, and the ignorable code points a disk drops
// removed. It is used only to compare two paths, never to open one. Without it
// ".Ssh", ".ſsh" (long s) and a soft hyphen in ".s<ad>sh" each open ".ssh" on
// APFS while strings.ToLower leaves them apart.
func foldKey(p string) string {
	p = norm.NFD.String(p)
	var b strings.Builder
	b.Grow(len(p))
	for _, r := range p {
		switch r {
		case 0x00AD, 0xFEFF, 0x200B, 0x200C, 0x200D:
			// Soft hyphen, BOM, zero-width space and the zero-width
			// non-joiner and joiner, as numbers so the source holds no
			// invisible character: a disk drops them.
			continue
		}
		b.WriteRune(foldRune(r))
	}
	return b.String()
}

// foldRune is the smallest rune in r's Unicode simple-fold cycle, so every
// rune a disk treats as equal maps to one key.
func foldRune(r rune) rune {
	m := r
	for c := unicode.SimpleFold(r); c != r; c = unicode.SimpleFold(c) {
		if c < m {
			m = c
		}
	}
	return m
}

// pathUnder reports whether p is dir or inside it, folding case and width
// where the disk ignores them.
func pathUnder(p, dir string) bool {
	if foldPaths {
		p, dir = foldKey(p), foldKey(dir)
	}
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// realPath is p with every link in the part of it that exists resolved. The
// part that does not exist yet, such as the folders a mkdir makes, is joined
// on as given.
func realPath(p string) string {
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return r
			}
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// realParent is p with the links in its folder resolved and its last name as
// given: the place a write to p lands. A rename onto a link replaces the
// link, so the last name is not followed.
func realParent(p string) string {
	dir, base := filepath.Split(p)
	if base == "" {
		return realPath(p)
	}
	return filepath.Join(realPath(filepath.Clean(dir)), base)
}

// linkRootPaths are the real write roots a policy allows, without the drop
// folder: the real paths of files_roots, or the receive folder when it names
// none. It is used to compare two policies and to list them for the person.
func linkRootPaths(policy config.LinkPolicy) []string {
	raw := policy.FilesRoots
	if raw == nil {
		if def := config.DefaultFilesRoot(); def != "" {
			raw = []string{def}
		}
	}
	home, _ := os.UserHomeDir()
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		p := r
		switch {
		case p == "~":
			p = home
		case strings.HasPrefix(p, "~/"):
			p = filepath.Join(home, p[2:])
		}
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		real := realPath(filepath.Clean(p))
		if !slices.Contains(out, real) {
			out = append(out, real)
		}
	}
	return out
}

// linkWriteRoots are the folders a write over a link from the machine with
// this policy may land in, as real paths: the policy's roots, then the drop
// folder. home is the real home folder, for the deny list and a message.
func (d *Daemon) linkWriteRoots(policy config.LinkPolicy) (home string, roots []string) {
	if h, err := os.UserHomeDir(); err == nil {
		home = realPath(filepath.Clean(h))
	}
	roots = append(roots, linkRootPaths(policy)...)
	if drop, err := d.dropRoot(); err == nil {
		roots = append(roots, realPath(drop))
	}
	return home, roots
}

// denyContains reports whether the real path p is at, inside, or an ancestor
// of any folder in list, folding case. An ancestor is caught so a link cannot
// rename a folder that holds a denied one out and back.
func denyContains(p string, list []string) bool {
	for _, d := range list {
		// The entry as named and its real path: a dotfile kept as a link
		// into a plain folder (stow, chezmoi, yadm) is denied at its target.
		for _, c := range []string{d, realPath(d)} {
			if pathUnder(p, c) || pathUnder(c, p) {
				return true
			}
		}
	}
	return false
}

// linkDenied reports whether the real path p is in, under, or an ancestor of
// the write deny list.
func linkDenied(p, home string) bool {
	var list []string
	if home != "" {
		for _, rel := range linkDenyHome {
			list = append(list, filepath.Join(home, filepath.FromSlash(rel)))
		}
	}
	for _, dir := range linkDenyAbsolute {
		list = append(list, filepath.FromSlash(dir))
	}
	// tuios's own folders, wherever the XDG variables put them.
	for _, dir := range []string{xdg.ConfigHome, xdg.StateHome, xdg.DataHome} {
		if dir != "" {
			list = append(list, filepath.Join(realPath(dir), "tuios"))
		}
	}
	return denyContains(p, list)
}

// linkReadDenied reports whether the real path p is in or under the read deny
// list. A public key in a denied .ssh folder is allowed.
func linkReadDenied(p, home string) bool {
	if home != "" && pathUnder(p, filepath.Join(home, ".ssh")) && strings.HasSuffix(strings.ToLower(p), ".pub") {
		return false
	}
	var list []string
	if home != "" {
		for _, rel := range linkReadDenyHome {
			list = append(list, filepath.Join(home, filepath.FromSlash(rel)))
		}
	}
	for _, dir := range []string{xdg.ConfigHome, xdg.StateHome, xdg.DataHome} {
		if dir != "" {
			list = append(list, filepath.Join(realPath(dir), "tuios"))
		}
	}
	for _, d := range list {
		if pathUnder(p, d) || pathUnder(p, realPath(d)) {
			return true
		}
	}
	return false
}

// checkLinkRead refuses a read of path that arrived over a link and lands in
// the read deny list. A call that did not come over a link passes.
func (d *Daemon) checkLinkRead(cs *connState, path string) *verbError {
	if cs == nil || !cs.viaLink {
		return nil
	}
	home := ""
	if h, err := os.UserHomeDir(); err == nil {
		home = realPath(filepath.Clean(h))
	}
	real := realPath(path)
	if linkReadDenied(real, home) || linkReadDenied(path, home) {
		peer := d.linkPolicy(cs).Peer
		if peer == "" {
			peer = "A linked machine"
		}
		LogBasic("Link %s (peer %q) refused a file read of %s: the path is in the read deny list", cs.clientID, peer, path)
		return hintedVerbError(ErrVerbForbidden, peer+" cannot read "+echoName(tildePath(path, home))+" on this machine. tuios does not let a linked machine read keys or credentials.", &VerbHint{
			Detail: "Nothing was read.",
		})
	}
	return nil
}

// linkWriteFS is where a file verb on cs may write the given paths. For a
// call that did not come over a link it is the plain file system. For a link
// it is an os.Root at the folder that holds every path, and a path outside the
// roots or in the deny list is refused with the reason. The caller closes the
// fileFS.
func (d *Daemon) linkWriteFS(cs *connState, paths ...string) (fileFS, *verbError) {
	if cs == nil || !cs.viaLink {
		return fileFS{}, nil
	}
	policy := d.linkPolicy(cs)
	home, roots := d.linkWriteRoots(policy)
	peer := policy.Peer
	if peer == "" {
		peer = "A linked machine"
	}
	table := `[hosts."*"]`
	if policy.Peer != "" {
		table = "[hosts." + policy.Peer + "]"
	}
	chosen := ""
	for _, p := range paths {
		real := realParent(p)
		if linkDenied(real, home) || linkDenied(realPath(p), home) || linkDenied(p, home) {
			LogBasic("Link %s (peer %q) refused a file write to %s: the path is in the deny list", cs.clientID, peer, p)
			return fileFS{}, hintedVerbError(ErrVerbForbidden, peer+" cannot write "+echoName(tildePath(real, home))+" on this machine. tuios does not let a linked machine change keys, credentials, shell start files or the tuios config.", &VerbHint{
				Detail: "Nothing was written. To change this file, copy it to this machine yourself, then move it into place.",
			})
		}
		root := ""
		for _, r := range roots {
			if pathUnder(real, r) {
				root = r
				break
			}
		}
		if root == "" {
			LogBasic("Link %s (peer %q) refused a file write to %s: outside the write roots", cs.clientID, peer, p)
			return fileFS{}, hintedVerbError(ErrVerbForbidden, peer+" cannot write "+echoName(tildePath(real, home))+". "+d.linkRootsMessage(home, linkRootPaths(policy), filepath.Dir(real), table), &VerbHint{
				Detail: "Nothing was written.",
			})
		}
		if chosen != "" && chosen != root {
			return fileFS{}, newVerbError(ErrVerbCrossDevice, "the paths are in two folders that a link writes apart, so tuios does not move between them")
		}
		chosen = root
	}
	if chosen == "" {
		return fileFS{}, nil
	}
	if err := os.MkdirAll(chosen, 0o755); err != nil { //nolint:gosec // the receive folder; a link needs x to write under it
		return fileFS{}, fileError("open", chosen, err)
	}
	r, err := os.OpenRoot(chosen)
	if err != nil {
		return fileFS{}, fileError("open", chosen, err)
	}
	return fileFS{root: r, dir: chosen}, nil
}

// linkRootsMessage says where a link may write and how to allow more, in
// plain words: "arch-btw lets other machines write only to ~/Downloads/tuios.
// To allow ~/dev, add it to files_roots in [hosts.laptop] in the config on
// arch-btw." The drop folder is left out: it is for dropped files only.
func (d *Daemon) linkRootsMessage(home string, roots []string, dir, table string) string {
	here := d.hostedPaneHostName()
	if here == "" {
		here = "this machine"
	}
	shown := make([]string, 0, len(roots))
	for _, r := range roots {
		shown = append(shown, tildePath(r, home))
	}
	where := "nowhere but the drop folder"
	switch len(shown) {
	case 0:
	case 1:
		where = "only to " + shown[0]
	default:
		where = "only to " + strings.Join(shown[:len(shown)-1], ", ") + " and " + shown[len(shown)-1]
	}
	want := tildePath(dir, home)
	return here + " lets other machines write " + where + ". To allow " + want + ", add it to files_roots in " + table + " in the config on " + here + "."
}

// confine turns a path a link gave into the real path its write goes to, so
// the root can name it. A path that is not under a link's roots stays as it
// is, and the root then refuses it.
func (f fileFS) confine(p string) string {
	if f.root == nil {
		return p
	}
	real := realParent(p)
	if foldPaths && pathUnder(real, f.dir) && !strings.HasPrefix(real, f.dir) {
		// The same folder written with other case: the root needs its own
		// spelling for the part above the path.
		real = f.dir + real[len(f.dir):]
	}
	return real
}

// confineDir is confine for a folder that the operation goes into rather
// than acts on, so a link at its last name is followed too: an os.Root
// refuses a link that names an absolute path, even one inside the root.
func (f fileFS) confineDir(p string) string {
	if f.root == nil {
		return p
	}
	real := realPath(p)
	if foldPaths && pathUnder(real, f.dir) && !strings.HasPrefix(real, f.dir) {
		real = f.dir + real[len(f.dir):]
	}
	return real
}

// tildePath writes p under home as ~/..., for a message.
func tildePath(p, home string) string {
	if home != "" && pathUnder(p, home) && len(p) >= len(home) {
		rest := strings.TrimPrefix(p[len(home):], string(filepath.Separator))
		if rest == "" {
			return "~"
		}
		return "~/" + filepath.ToSlash(rest)
	}
	return p
}
