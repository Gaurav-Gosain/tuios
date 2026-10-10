package session

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/adrg/xdg"
)

// Where a file write that arrives over a link may land.
//
// The file verbs act on this machine for whoever calls them. The person's own
// clients may write anywhere the user may. A call that arrived over a link
// comes from another machine, and the link policy (link_policy.go) only says
// whether that machine may use the file verbs at all. A sender that lies, or
// a copy aimed at the wrong folder, must not be able to drop a key into
// ~/.ssh/authorized_keys or a line into ~/.bashrc: that is how a file copy
// becomes a login (croc CVE-2023-43619 is that bug).
//
// So every write over a link (open-file-stream write, file-commit,
// file-mkdir, file-rename, file-remove, file-abort) is held to two rules:
//
//   - It lands inside the home folder, or inside this daemon's drop folder.
//     The check is on the real path, after links, and the write itself goes
//     through an os.Root opened at that folder, so a link inside the folder
//     cannot carry the write out of it.
//   - It does not land in the deny list below: the folders that hold keys,
//     credentials, shell start files, login items, and tuios's own config and
//     state. The check is on the real path of the folder the write lands in,
//     and it ignores case, because the disks of macOS and Windows do.
//
// The deny check reads the real path before the write. A process on this
// machine that swaps a folder for a link between the check and the write
// could still steer a write within the home folder, but such a process
// already runs as the user and needs no link to do that.

// linkDenyHome is the deny list, as paths under the home folder. A write to
// one of them or to anything under it is refused.
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
	".config/git/credentials",
	"Library/LaunchAgents",
	"Library/Application Support/tuios",
	".git-credentials",
	".netrc",
	".aws",
	".kube",
	".docker",
	".pam_environment",
	".profile",
	".bash_profile",
	".bash_login",
	".bash_logout",
	".bashrc",
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
}

// linkDenyAbsolute is the deny list outside the home folder: the crontab
// spools. They are outside every root a link may write, so they are refused
// twice, which is the point.
var linkDenyAbsolute = []string{
	"/var/spool/cron",
	"/var/at/tabs",
	"/var/cron/tabs",
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

// pathUnder reports whether p is dir or inside it, with case ignored where
// the disk ignores it.
func pathUnder(p, dir string) bool {
	if foldPaths {
		p, dir = strings.ToLower(p), strings.ToLower(dir)
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

// linkWriteRoots are the folders a write over a link may land in, as real
// paths: the home folder, then the drop folder.
func (d *Daemon) linkWriteRoots() (home string, roots []string) {
	if h, err := os.UserHomeDir(); err == nil {
		home = realPath(filepath.Clean(h))
		roots = append(roots, home)
	}
	if drop, err := d.dropRoot(); err == nil {
		roots = append(roots, realPath(drop))
	}
	return home, roots
}

// linkDenied reports whether the real path p is in the deny list.
func linkDenied(p, home string) bool {
	if home != "" {
		for _, rel := range linkDenyHome {
			if pathUnder(p, filepath.Join(home, filepath.FromSlash(rel))) {
				return true
			}
		}
	}
	for _, dir := range linkDenyAbsolute {
		if pathUnder(p, filepath.FromSlash(dir)) {
			return true
		}
	}
	// tuios's own folders, wherever the XDG variables put them.
	for _, dir := range []string{xdg.ConfigHome, xdg.StateHome, xdg.DataHome} {
		if dir != "" && pathUnder(p, filepath.Join(realPath(dir), "tuios")) {
			return true
		}
	}
	return false
}

// errLinkWrite is a write over a link that the rules above refuse.
var errLinkWrite = errors.New("refused for a link")

// linkWriteFS is where a file verb on cs may write the given paths. For a
// call that did not come over a link it is the plain file system. For a link
// it is an os.Root at the folder that holds every path, and a path outside
// the roots or in the deny list is refused with the reason. The caller
// closes the fileFS.
func (d *Daemon) linkWriteFS(cs *connState, paths ...string) (fileFS, *verbError) {
	if cs == nil || !cs.viaLink {
		return fileFS{}, nil
	}
	home, roots := d.linkWriteRoots()
	peer := d.linkPolicy(cs).Peer
	if peer == "" {
		peer = "A linked machine"
	}
	chosen := ""
	for _, p := range paths {
		real := realParent(p)
		if linkDenied(real, home) || linkDenied(realPath(p), home) || linkDenied(p, home) {
			LogBasic("Link %s (peer %q) refused a file write to %s: the path is in the deny list", cs.clientID, peer, p)
			return fileFS{}, hintedVerbError(ErrVerbForbidden, peer+" cannot write "+echoName(tildePath(p, home))+" on this machine. tuios does not let a linked machine change keys, credentials, shell start files or the tuios config.", &VerbHint{
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
			LogBasic("Link %s (peer %q) refused a file write to %s: outside the home folder", cs.clientID, peer, p)
			return fileFS{}, hintedVerbError(ErrVerbForbidden, peer+" cannot write "+echoName(p)+" on this machine. A linked machine writes only in the home folder.", &VerbHint{
				Detail: "Nothing was written. Choose a folder under ~ on this machine.",
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
	r, err := os.OpenRoot(chosen)
	if err != nil {
		return fileFS{}, fileError("open", chosen, err)
	}
	return fileFS{root: r, dir: chosen}, nil
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
