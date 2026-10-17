package session

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pathroot"
	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
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
// is held to the read deny list: keys, credentials, /proc, the daemon's
// socket folder and tuios's own state. A read elsewhere is allowed, so a copy
// out works. A read does not go through a root, so on Windows a path must also
// be a plain drive letter path (plainLinkPath).
//
// The deny check reads the real path before the call. A process on this
// machine that swaps a folder for a link between the check and the write could
// still steer a write within a root, but such a process already runs as the
// user and needs no link to do that. The link itself cannot make a link: no
// verb creates one, a copy skips them, and a rename of a link whose target is
// denied, or holds a denied folder, is refused. What is left is a link that
// was already inside a root, pointing at a denied place that is also inside
// it (files_roots = ["~"]), inside a folder the link renames between another
// call's check and its open. That needs a root the person widened on purpose,
// and a link the person made.

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
	// The Windows start folder and PowerShell profiles.
	"AppData/Roaming/Microsoft/Windows/Start Menu/Programs/Startup",
	"Documents/PowerShell",
	"Documents/WindowsPowerShell",
}

// linkDenyAbsolute is the write deny list outside the home folder: the crontab
// spools. They are outside every root a link may write, so they are refused
// twice, which is the point.
var linkDenyAbsolute = []string{
	"/var/spool/cron",
	"/var/at/tabs",
	"/var/cron/tabs",
	"/proc",
}

// linkReadDenyAbsolute is the read deny list outside the home folder. /proc
// holds every process's environment and memory as files a plain read returns:
// /proc/self/environ and /proc/self/mem are the daemon's own, and the panes'
// shells are its children.
var linkReadDenyAbsolute = []string{
	"/proc",
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
	".npmrc",
	".pypirc",
	".gem/credentials",
	".cargo/credentials",
	".cargo/credentials.toml",
	".vault-token",
	".pgpass",
	".my.cnf",
	".terraform.d/credentials.tfrc.json",
	".config/rclone",
	".config/hub",
	".config/op",
	".local/share/keyrings",
	// Shell and program histories: a command line often holds a token.
	".bash_history",
	".zsh_history",
	".zhistory",
	".histfile",
	".sh_history",
	".local/share/fish/fish_history",
	".python_history",
	".node_repl_history",
	".psql_history",
	".mysql_history",
	".sqlite_history",
	".lesshst",
	".viminfo",
	".local/state/nvim/shada",
	// Browser profiles: cookies are signed-in sessions.
	".mozilla",
	".config/google-chrome",
	".config/chromium",
	".config/BraveSoftware",
	".config/microsoft-edge",
	"Library/Application Support/Google/Chrome",
	"Library/Application Support/Firefox",
	"Library/Application Support/BraveSoftware",
	"Library/Cookies",
	"Library/Safari",
	// Password managers and the tokens of other tools.
	".config/Bitwarden",
	".config/1Password",
	".config/github-copilot",
	// Windows credentials and the DPAPI keys that open them.
	"AppData/Roaming/Microsoft/Credentials",
	"AppData/Local/Microsoft/Credentials",
	"AppData/Roaming/Microsoft/Protect",
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
	r, ok := pathroot.Within(f.dir, path)
	if !ok {
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
//
//go:noinline
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
	if runtime.GOOS == "windows" {
		p = trimWindowsNames(p)
	}
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

// trimWindowsNames is p with the dots and spaces at the end of each name
// removed. Windows drops them when it opens a path, and so does os.Root, so
// ".ssh." and ".ssh " open ".ssh".
func trimWindowsNames(p string) string {
	parts := strings.Split(p, string(filepath.Separator))
	for i, part := range parts {
		if part == "." || part == ".." {
			continue
		}
		if t := strings.TrimRight(part, ". "); t != "" {
			parts[i] = t
		}
	}
	return strings.Join(parts, string(filepath.Separator))
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

// denyUnder is pathUnder for a deny list: it folds case and width on every
// system. A Linux disk can ignore case too (an ext4 folder with casefold, a
// vfat or exFAT stick), and a wider deny match only refuses more.
func denyUnder(p, dir string) bool {
	p, dir = foldKey(p), foldKey(dir)
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// realPath is p with every link in the part of it that exists resolved. The
// part that does not exist yet, such as the folders a mkdir makes, is joined
// on as given. On macOS a path through /System/Volumes/Data is given by its
// short name (trimDataVolume).
func realPath(p string) string {
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			r = trimDataVolume(r)
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
	return linkPathsOf(raw)
}

// linkPathsOf is the real paths of roots as a config names them: absolute or
// starting with ~. Others are left out.
func linkPathsOf(raw []string) []string {
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

// linkPrivate is the daemon's own folders a link must not touch beside the
// deny lists: sock is the real folder of the daemon's socket, which holds the
// mail stash, the pid file and the sockets, and drop is the drop folder inside
// it, which a link may use.
type linkPrivate struct {
	sock, drop string
}

// linkPrivateDirs is the daemon's linkPrivate.
func (d *Daemon) linkPrivateDirs() linkPrivate {
	if d == nil || d.manager == nil {
		return linkPrivate{}
	}
	return linkPrivateFor(d.manager.SocketPath())
}

// linkPrivateFor is the linkPrivate of a daemon listening on socketPath.
func linkPrivateFor(socketPath string) linkPrivate {
	if socketPath == "" {
		return linkPrivate{}
	}
	sock := realPath(filepath.Clean(filepath.Dir(socketPath)))
	lp := linkPrivate{sock: sock, drop: filepath.Join(sock, "drop")}
	// A named server's socket folder is <default's>/servers/<name>. The
	// default's folder holds every server's, so it is the one denied, and a
	// link to one server cannot reach another's sockets or stash.
	if serverscope.Name() != "" {
		lp.sock = filepath.Dir(filepath.Dir(sock))
	}
	return lp
}

// denyKeys is a deny list made ready to compare: each entry as named and its
// real path, folded once (foldKey). Folding every entry again for every path
// cost a folder copy over a link most of its time: tens of entries, each
// resolved through its links and folded rune by rune, for each file.
type denyKeys []denyKey

// denyKey is one folded entry, and the same with a separator at its end,
// which a path inside it starts with.
type denyKey struct{ key, dir string }

func newDenyKey(p string) denyKey {
	k := foldKey(p)
	return denyKey{key: k, dir: strings.TrimSuffix(k, string(filepath.Separator)) + string(filepath.Separator)}
}

func makeDenyKeys(list []string) denyKeys {
	out := make(denyKeys, 0, 2*len(list))
	for _, d := range list {
		out = append(out, newDenyKey(d), newDenyKey(realPath(d)))
	}
	return out
}

// under reports whether the folded path a is b or inside it.
func (a denyKey) under(b denyKey) bool {
	return a.key == b.key || strings.HasPrefix(a.key, b.dir)
}

// holds reports whether p is at, inside, or an ancestor of an entry. An
// ancestor is caught so a link cannot rename a folder that holds a denied one
// out and back.
func (k denyKeys) holds(p string) bool {
	fp := newDenyKey(p)
	for _, c := range k {
		if fp.under(c) || c.under(fp) {
			return true
		}
	}
	return false
}

// covers reports whether p is at or inside an entry.
func (k denyKeys) covers(p string) bool {
	fp := newDenyKey(p)
	for _, c := range k {
		if fp.under(c) {
			return true
		}
	}
	return false
}

// denyKeysTTL is how long a prepared deny list is used. Its entries change
// only when the person moves a dotfile link on this machine; a linked machine
// has no verb that makes a link.
const denyKeysTTL = 2 * time.Second

var denyKeysCache struct {
	sync.Mutex
	m map[string]denyKeysEntry
}

type denyKeysEntry struct {
	at   time.Time
	keys denyKeys
}

// cachedDenyKeys is the prepared deny list for key, built by list when it is
// not cached or older than denyKeysTTL.
func cachedDenyKeys(key string, list func() []string) denyKeys {
	denyKeysCache.Lock()
	e, ok := denyKeysCache.m[key]
	denyKeysCache.Unlock()
	if ok && time.Since(e.at) < denyKeysTTL {
		return e.keys
	}
	keys := makeDenyKeys(list())
	denyKeysCache.Lock()
	if denyKeysCache.m == nil {
		denyKeysCache.m = map[string]denyKeysEntry{}
	}
	denyKeysCache.m[key] = denyKeysEntry{at: time.Now(), keys: keys}
	denyKeysCache.Unlock()
	return keys
}

// linkDenied reports whether the real path p is in, under, or an ancestor of
// the write deny list.
func linkDenied(p, home string, lp linkPrivate) bool {
	withSock := lp.sock != "" && (lp.drop == "" || !denyUnder(p, lp.drop))
	key := "write\x00" + home + "\x00" + lp.sock + "\x00" + strconv.FormatBool(withSock)
	return cachedDenyKeys(key, func() []string {
		var list []string
		if home != "" {
			for _, rel := range linkDenyHome {
				list = append(list, filepath.Join(home, filepath.FromSlash(rel)))
			}
		}
		for _, dir := range linkDenyAbsolute {
			list = append(list, filepath.FromSlash(dir))
		}
		list = append(list, tuiosOwnDirs()...)
		if withSock {
			list = append(list, lp.sock)
		}
		return list
	}).holds(p)
}

// tuiosOwnDirs is tuios's own folders, wherever the XDG variables put them.
// The state folders are those of every server, not only the current one's, so
// a link to one server cannot reach another server's files.
func tuiosOwnDirs() []string {
	var list []string
	for _, dir := range []string{xdg.ConfigHome, serverscope.BaseStateHome(), xdg.DataHome} {
		if dir != "" {
			list = append(list, filepath.Join(realPath(dir), "tuios"))
		}
	}
	if base := serverscope.BaseStateHome(); base != "" {
		list = append(list, filepath.Join(realPath(base), serverscope.StateDirName))
	}
	return list
}

// linkReadDenied reports whether the real path p is in or under the read deny
// list. A public key in a denied .ssh folder is allowed.
func linkReadDenied(p, home string, lp linkPrivate) bool {
	if home != "" && denyUnder(p, filepath.Join(home, ".ssh")) && strings.HasSuffix(strings.ToLower(p), ".pub") {
		return false
	}
	withSock := lp.sock != "" && (lp.drop == "" || !denyUnder(p, lp.drop))
	key := "read\x00" + home + "\x00" + lp.sock + "\x00" + strconv.FormatBool(withSock)
	return cachedDenyKeys(key, func() []string {
		var list []string
		if home != "" {
			for _, rel := range linkReadDenyHome {
				list = append(list, filepath.Join(home, filepath.FromSlash(rel)))
			}
		}
		list = append(list, tuiosOwnDirs()...)
		for _, dir := range linkReadDenyAbsolute {
			list = append(list, filepath.FromSlash(dir))
		}
		if withSock {
			list = append(list, lp.sock)
		}
		return list
	}).covers(p)
}

// plainLinkPath reports whether p is a path form the deny lists can judge. On
// Windows that is a drive letter path with no other colon: a \\?\ or \\.\
// prefix, a UNC share such as \\localhost\C$, and a stream name such as
// "id_ed25519::$DATA" each open a file the deny list knows by another name.
// os.Root refuses those forms for a write, but a read does not go through a
// root. Every path is plain elsewhere: a colon is a valid name there.
func plainLinkPath(p string) bool {
	if runtime.GOOS != "windows" {
		return true
	}
	vol := filepath.VolumeName(p)
	if len(vol) != 2 || vol[1] != ':' {
		return false
	}
	return !strings.ContainsAny(p[len(vol):], ":?*")
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
	lp := d.linkPrivateDirs()
	policy := d.linkPolicy(cs)
	if policy.FilesReadRoots != nil && plainLinkPath(path) {
		_, writeRoots := d.linkWriteRoots(policy)
		roots := append(linkPathsOf(policy.FilesReadRoots), writeRoots...)
		if !slices.ContainsFunc(roots, func(r string) bool { return pathUnder(real, r) }) {
			LogBasic("Link %s (peer %q) refused a file read of %s: outside the read roots", cs.clientID, policy.Peer, path)
			return d.linkReadRootsError(policy, home, path)
		}
	}
	if !plainLinkPath(path) || !plainLinkPath(real) || linkReadDenied(real, home, lp) || linkReadDenied(path, home, lp) {
		peer := policy.Peer
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

// openLinkRead opens a regular file to read it for cs. For a call over a
// link the path is judged before the open, and the file that was opened is
// judged again by the path the system has for it now. A check on the path
// alone races a rename: a folder that holds a link to ~/.ssh can be moved in
// under the checked name between the check and the open. The file must also
// have one name only. A hard link to a key in a root reads as the key, and
// no path check sees it. err is the open's own error, verr a refusal.
func (d *Daemon) openLinkRead(cs *connState, path string) (f *os.File, fi fs.FileInfo, verr *verbError, err error) {
	if verr := d.checkLinkRead(cs, path); verr != nil {
		return nil, nil, verr, nil
	}
	f, fi, err = openRegular(path)
	if err != nil || cs == nil || !cs.viaLink {
		return f, fi, nil, err
	}
	if verr := d.checkOpenedForLink(cs, f, path); verr != nil {
		_ = f.Close()
		return nil, nil, verr, nil
	}
	return f, fi, nil, nil
}

// checkOpenedForLink judges a file opened for a read over a link: by the
// path the system has for it now, and by its number of names.
func (d *Daemon) checkOpenedForLink(cs *connState, f *os.File, path string) *verbError {
	if real, ok := fdPath(f); ok {
		if verr := d.checkLinkRead(cs, real); verr != nil {
			return verr
		}
	}
	if fi, err := f.Stat(); err == nil && fi.Mode().IsRegular() {
		if n, err := fileLinks(f); err == nil && n > 1 {
			policy := d.linkPolicy(cs)
			LogBasic("Link %s (peer %q) refused a file read of %s: the file has %d names", cs.clientID, policy.Peer, path, n)
			return hintedVerbError(ErrVerbForbidden, peerName(policy.Peer)+" cannot read "+echoName(path)+". The file has other names (hard links), so tuios cannot tell what it is, and a linked machine does not read it.", &VerbHint{
				Detail: "Nothing was read. To share it, copy it to a new file first.",
			})
		}
	}
	return nil
}

// linkWalkRoot is the folder a walk over a link lists: the path the system
// has for the folder once it is open, judged by the read rules, so a rename
// cannot swap another folder in under the checked name.
func (d *Daemon) linkWalkRoot(cs *connState, path string) (string, *verbError) {
	if verr := d.checkLinkRead(cs, path); verr != nil {
		return "", verr
	}
	if cs == nil || !cs.viaLink {
		return path, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return path, nil
	}
	defer func() { _ = f.Close() }()
	real, ok := fdPath(f)
	if !ok {
		return path, nil
	}
	if verr := d.checkLinkRead(cs, real); verr != nil {
		return "", verr
	}
	return real, nil
}

// linkReadRootsError refuses a read outside files_read_roots and says how to
// allow it.
func (d *Daemon) linkReadRootsError(policy config.LinkPolicy, home, path string) *verbError {
	here := d.hostedPaneHostName()
	if here == "" {
		here = "this machine"
	}
	table := `[hosts."*"]`
	if policy.Peer != "" {
		table = "[hosts." + policy.Peer + "]"
	}
	shown := []string{}
	for _, r := range linkPathsOf(policy.FilesReadRoots) {
		shown = append(shown, tildePath(r, home))
	}
	where := "nothing but the folders it may write"
	if len(shown) > 0 {
		where = strings.Join(shown, ", ") + " and the folders it may write"
	}
	want := echoName(tildePath(path, home))
	return hintedVerbError(ErrVerbForbidden, peerName(policy.Peer)+" cannot read "+want+". "+here+" lets other machines read only "+where+". To allow "+want+", add it to files_read_roots in "+table+" in the config on "+here+".", &VerbHint{
		Detail: "Nothing was read.",
	})
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
	lp := d.linkPrivateDirs()
	chosen := ""
	for _, p := range paths {
		real := realParent(p)
		if !plainLinkPath(p) || linkDenied(real, home, lp) || linkDenied(realPath(p), home, lp) || linkDenied(p, home, lp) {
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
		if drop, err := d.dropRoot(); err == nil && pathUnder(real, realPath(drop)) {
			// In the drop folder a link writes only inside a drop that
			// file-drop-dir made: never a file or a folder of its own at
			// the top, which pruneDrops would not see.
			if rel, err := filepath.Rel(realPath(drop), real); err != nil || !strings.Contains(rel, string(filepath.Separator)) {
				LogBasic("Link %s (peer %q) refused a file write to %s: the top of the drop folder", cs.clientID, peer, p)
				return fileFS{}, hintedVerbError(ErrVerbForbidden, peer+" cannot write "+echoName(tildePath(real, home))+". In the drop folder, files go only into a drop that file-drop-dir makes.", &VerbHint{
					Detail: "Nothing was written.",
				})
			}
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

// receiveFolder is the folder a copy to this machine lands in when it names
// no folder: for a link, the first folder of its write roots (files_roots, or
// the receive folder when files_roots says nothing); for the person, the
// receive folder, config.DefaultFilesRoot.
func (d *Daemon) receiveFolder(cs *connState) (string, *verbError) {
	var roots []string
	if cs != nil && cs.viaLink {
		roots = linkRootPaths(d.linkPolicy(cs))
	} else if def := config.DefaultFilesRoot(); def != "" {
		roots = []string{def}
	}
	if len(roots) == 0 {
		return "", newVerbError(ErrVerbForbidden, "this machine has no folder that another machine may write to. Set files_roots in the config here.")
	}
	return roots[0], nil
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
