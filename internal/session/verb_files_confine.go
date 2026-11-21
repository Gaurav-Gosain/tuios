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

var linkDenyCredentialHome = []string{
	".ssh",
	".gnupg",
	".config/tuios",
	".local/state/tuios",
	".local/share/tuios",
	"Library/Application Support/tuios",
	".config/systemd",
	".config/environment.d",
	".config/fish",
	".config/git",
	".config/zsh",
	".config/gcloud",
	".config/tuios-web",
	".git-credentials",
	".gitconfig",
	".netrc",
	".aws",
	".azure",
	".kube",
	".password-store",
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
	".inputrc",
	"Documents/PowerShell",
	"Documents/WindowsPowerShell",
	".claude.json",
	".config/sops/age",
	"Library/Application Support/sops/age",
	"AppData/Roaming/sops/age",
	".local/share/atuin",
	".config/atuin",
	".ollama/id_ed25519",
	".gemini/.env",
	".qwen/.env",
	".hermes/.env",
	"AppData/Local/hermes/.env",
}

var linkDenyWriteOnlyHome = []string{
	".config/autostart",
	"Library/LaunchAgents",
	"Library/LaunchDaemons",
	".docker",
	".claude",
	".codex",
	".config/gh",
	".gemini",
	".config/opencode",
	".config/amp",
	".copilot",
	".config/crush",
	".cursor",
	".config/devin",
	"AppData/Roaming/devin",
	".factory",
	".grok",
	".hermes",
	"AppData/Local/hermes",
	".config/kilo",
	".kimi-code",
	".pi/agent",
	".omp/agent",
	".qoder",
	".qwen",
	"AppData/Roaming/Microsoft/Windows/Start Menu/Programs/Startup",
}

var linkDenyHome = slices.Concat(linkDenyCredentialHome, linkDenyWriteOnlyHome)

var linkDenyAbsolute = []string{
	"/var/spool/cron",
	"/var/at/tabs",
	"/var/cron/tabs",
	"/proc",
}

var linkReadDenyAbsolute = []string{
	"/proc",
}

var linkReadDenyHome = slices.Concat(linkDenyCredentialHome, linkReadDenyOnlyHome)

var linkReadDenyOnlyHome = []string{
	".docker/config.json",
	".config/gh/hosts.yml",
	"Library/Keychains",
	".claude/.credentials.json",
	".config/claude/.credentials.json",
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
	".local/share/zsh/history",
	".local/state/zsh/history",
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
	"snap/firefox/common/.mozilla",
	"snap/chromium/common/chromium",
	"snap/thunderbird/common/.thunderbird",
	".var/app",
	".thunderbird",
	"Library/Thunderbird",
	".config/Code",
	"Library/Application Support/Code",
	".config/Cursor",
	"Library/Application Support/Cursor",
	"AppData/Roaming/Cursor",
	".config/Signal",
	"Library/Application Support/Signal",
	".config/discord",
	"Library/Application Support/discord",
	".config/Slack",
	"Library/Application Support/Slack",
	".local/share/TelegramDesktop",
	".bitcoin",
	".electrum",
	".ethereum",
	".monero",
	".litecoin",
	".config/solana",
	".config/Exodus",
	"Library/Application Support/Bitcoin",
	"Library/Application Support/Exodus",
	"Library/Application Support/Electrum",
	"AppData/Roaming/Bitcoin",
	"AppData/Roaming/Electrum",
	"AppData/Roaming/Exodus",
	".config/Bitwarden",
	".config/1Password",
	".config/github-copilot",
	".gemini/oauth_creds.json",
	".gemini/mcp-oauth-tokens.json",
	".local/share/opencode/auth.json",
	".local/share/opencode/mcp-auth.json",
	".qwen/oauth_creds.json",
	".local/share/kilo/auth.json",
	".local/share/kilo/mcp-auth.json",
	".pi/agent/auth.json",
	".omp/agent/agent.db",
	".omp/agent/agent.db-wal",
	".local/share/amp/secrets.json",
	".copilot/config.json",
	".cursor/mcp.json",
	".oci",
	".config/doctl",
	"Library/Application Support/doctl",
	"AppData/Roaming/doctl",
	".config/ngrok",
	"Library/Application Support/ngrok",
	"AppData/Local/ngrok",
	".ngrok2",
	"AppData/Roaming/Microsoft/Credentials",
	"AppData/Local/Microsoft/Credentials",
	"AppData/Roaming/Microsoft/Protect",
}

type fileFS struct {
	root *os.Root
	dir  string
	link bool
}

func (f fileFS) commitMode(perm uint32) os.FileMode {
	m := os.FileMode(perm & 0o777)
	if f.link {
		m &^= os.FileMode(processUmask) | 0o022
	}
	return m
}

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

//go:noinline
func (f fileFS) Close() {
	if f.root != nil {
		_ = f.root.Close()
	}
}

var foldPaths = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

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
			continue
		}
		b.WriteRune(foldRune(r))
	}
	return b.String()
}

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

func foldRune(r rune) rune {
	m := r
	for c := unicode.SimpleFold(r); c != r; c = unicode.SimpleFold(c) {
		if c < m {
			m = c
		}
	}
	return m
}

func pathUnder(p, dir string) bool {
	if foldPaths {
		p, dir = foldKey(p), foldKey(dir)
	}
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

func denyUnder(p, dir string) bool {
	p, dir = foldKey(p), foldKey(dir)
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

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

func realParent(p string) string {
	dir, base := filepath.Split(p)
	if base == "" {
		return realPath(p)
	}
	return filepath.Join(realPath(filepath.Clean(dir)), base)
}

func linkRootPaths(policy config.LinkPolicy) []string {
	raw := policy.FilesRoots
	if raw == nil {
		if def := config.DefaultFilesRoot(); def != "" {
			raw = []string{def}
		}
	}
	return linkPathsOf(raw)
}

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

type linkPrivate struct {
	sock, drop string
}

func (d *Daemon) linkPrivateDirs() linkPrivate {
	if d == nil || d.manager == nil {
		return linkPrivate{}
	}
	return linkPrivateFor(d.manager.SocketPath())
}

func linkPrivateFor(socketPath string) linkPrivate {
	if socketPath == "" {
		return linkPrivate{}
	}
	sock := realPath(filepath.Clean(filepath.Dir(socketPath)))
	lp := linkPrivate{sock: sock, drop: filepath.Join(sock, "drop")}
	if serverscope.Name() != "" {
		lp.sock = filepath.Dir(filepath.Dir(sock))
	}
	return lp
}

type denyKeys []denyKey

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

func (a denyKey) under(b denyKey) bool {
	return a.key == b.key || strings.HasPrefix(a.key, b.dir)
}

func (k denyKeys) holds(p string) bool {
	fp := newDenyKey(p)
	for _, c := range k {
		if fp.under(c) || c.under(fp) {
			return true
		}
	}
	return false
}

func (k denyKeys) covers(p string) bool {
	fp := newDenyKey(p)
	return slices.ContainsFunc(k, fp.under)
}

const denyKeysTTL = 2 * time.Second

var denyKeysCache struct {
	sync.Mutex
	m map[string]denyKeysEntry
}

type denyKeysEntry struct {
	at   time.Time
	keys denyKeys
}

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

func (d *Daemon) linkWalkDenied(cs *connState) func(string) bool {
	if cs == nil || !cs.viaLink {
		return nil
	}
	home := ""
	if h, err := os.UserHomeDir(); err == nil {
		home = realPath(filepath.Clean(h))
	}
	lp := d.linkPrivateDirs()
	return func(p string) bool {
		return !plainLinkPath(p) || linkReadDenied(p, home, lp)
	}
}

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
	return fileFS{root: r, dir: chosen, link: true}, nil
}

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

func (f fileFS) confine(p string) string {
	if f.root == nil {
		return p
	}
	real := realParent(p)
	if foldPaths && pathUnder(real, f.dir) && !strings.HasPrefix(real, f.dir) {
		real = f.dir + real[len(f.dir):]
	}
	return real
}

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
