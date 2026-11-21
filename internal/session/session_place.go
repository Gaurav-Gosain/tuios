package session

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
	"github.com/Gaurav-Gosain/tuios/internal/winpath"
)

type placeRecord struct {
	cwd       atomic.Pointer[string]
	branch    atomic.Pointer[string]
	gen       atomic.Uint64
	announced atomic.Bool
	elsewhere atomic.Pointer[elsewhereReport]

	onChange func()
}

func (r *placeRecord) changed() {
	if r.onChange != nil {
		r.onChange()
	}
}

type elsewhereReport struct {
	host, dir string
}

func (r *placeRecord) setCwd(raw string) {
	path, ok := parseCwdReport(raw)
	if !ok {
		return
	}
	r.setCwdPath(path)
}

func (r *placeRecord) announce(raw string) bool {
	path, host, ok := parseCwdAnnouncement(raw)
	if !ok {
		return false
	}
	if host != "" {
		changed := r.Elsewhere() != host
		r.elsewhere.Store(&elsewhereReport{host: host, dir: path})
		r.changed()
		return changed
	}
	moved := r.setElsewhere("")
	if !r.announced.Swap(true) {
		moved = true
	}
	if r.setCwdPath(path) {
		moved = true
	}
	return moved
}

func (r *placeRecord) Elsewhere() string {
	host, _ := r.ElsewhereReport()
	return host
}

func (r *placeRecord) ElsewhereReport() (host, dir string) {
	if p := r.elsewhere.Load(); p != nil {
		return p.host, p.dir
	}
	return "", ""
}

func (r *placeRecord) setElsewhere(host string) bool {
	if r.Elsewhere() == host {
		return false
	}
	if host == "" {
		r.elsewhere.Store(nil)
	} else {
		r.elsewhere.Store(&elsewhereReport{host: host})
	}
	r.changed()
	return true
}

func (r *placeRecord) announcedCwd() string {
	if !r.announced.Load() {
		return ""
	}
	return r.Cwd()
}

func (r *placeRecord) setCwdPath(path string) bool {
	if cur := r.cwd.Load(); cur != nil && *cur == path {
		return false
	}
	r.cwd.Store(&path)
	empty := ""
	r.branch.Store(&empty)
	gen := r.gen.Add(1)
	go func() {
		b := gitstate.Branch(path)
		if r.gen.Load() == gen {
			r.branch.Store(&b)
			r.changed()
		}
	}()
	r.changed()
	return true
}

func (r *placeRecord) Cwd() string {
	if p := r.cwd.Load(); p != nil {
		return *p
	}
	return ""
}

func (r *placeRecord) Branch() string {
	if p := r.branch.Load(); p != nil {
		return *p
	}
	return ""
}

func (r *placeRecord) place() (dir, branch string) {
	return dirLabel(r.Cwd(), homeDir()), r.Branch()
}

var homeDir = sync.OnceValue(func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Clean(home)
})

var localHostname = sync.OnceValue(func() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.ToLower(h)
})

var localHostNames = sync.OnceValue(func() map[string]bool {
	return hostNameSet(localHostname(), os.Getenv("COMPUTERNAME"), runtime.GOOS == "darwin")
})

func hostNameSet(hostname, computerName string, shortName bool) map[string]bool {
	names := map[string]bool{"localhost": true}
	for _, name := range []string{hostname, computerName} {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			names[name] = true
		}
	}
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if short, _, ok := strings.Cut(hostname, "."); shortName && ok && short != "" {
		names[short] = true
	}
	return names
}

// IsLocalHostName reports whether host, from an OSC 7 report or a file://
// address, names this machine. An empty host does.
func IsLocalHostName(host string) bool {
	host = strings.ToLower(host)
	return host == "" || localHostNames()[host]
}

func parseCwdReport(raw string) (string, bool) {
	path, host, ok := parseCwdAnnouncement(raw)
	if !ok || host != "" {
		return "", false
	}
	return path, true
}

// ParseCwdAnnouncement is parseCwdAnnouncement for a client that judges a
// report from a pane it runs itself, so both sides read OSC 7 the same way.
func ParseCwdAnnouncement(raw string) (path, host string, ok bool) {
	return parseCwdAnnouncement(raw)
}

func parseCwdAnnouncement(raw string) (path, host string, ok bool) {
	return parseCwdFor(raw, runtime.GOOS, IsLocalHostName)
}

func parseCwdFor(raw, goos string, isLocal func(string) bool) (dir, host string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if !strings.HasPrefix(raw, "file://") {
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			raw = raw[1 : len(raw)-1]
		}
		dir, ok := winpath.ForOS(raw, goos)
		return dir, "", ok
	}
	h, p, ok := winpath.FileURL(raw)
	if !ok {
		return "", "", false
	}
	if !isLocal(h) {
		return cleanSlashPath(p), ClampDisplayText(h), true
	}
	dir, ok = winpath.ForOS(p, goos)
	return dir, "", ok
}

func cleanSlashPath(p string) string {
	if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
		return "/" + path.Clean(p)
	}
	return path.Clean(p)
}

func dirLabel(cwd, home string) string {
	if cwd == "" {
		return ""
	}
	if home != "" && cwd == home {
		return "~"
	}
	return filepath.Base(cwd)
}

var generatedSessionName = lazyre.New(`^session-[0-9]+$`)

// IsGeneratedSessionName reports whether a session name is one tuios made up
// rather than one a person chose. A surface labels such a session by where it
// is, because "session-3" says nothing and a directory says something.
func IsGeneratedSessionName(name string) bool {
	return generatedSessionName().MatchString(name)
}
