package tmuxcompat

import (
	"hash/fnv"
	"path/filepath"
	"strconv"
	"strings"
)

// Version is the tmux version the shim reports for -V. Tools gate features on
// it, and 3.4 has every command the shim answers.
const Version = "3.4"

// PaneNumber is the number in a pane's tmux id, derived from the tuios window
// id. It is stable for the life of the window and needs no state: FNV-1a, 31
// bits, so it prints as a plain positive integer the way tmux's %N does.
func PaneNumber(windowID string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(windowID))
	return h.Sum32() & 0x7fffffff
}

// PaneID is the tmux pane id ("%N") of a tuios window.
func PaneID(windowID string) string {
	return "%" + strconv.FormatUint(uint64(PaneNumber(windowID)), 10)
}

// SocketPath is the path the shim's TMUX names as its server socket. Nothing
// listens there: it is a marker. A `tmux` call whose TMUX (or -S) names it is
// for the shim, and one that names any other socket is for a real tmux.
func SocketPath(dir string) string { return filepath.Join(dir, "socket") }

// BinDir is the directory holding the `tmux` link the launcher puts first on
// PATH.
func BinDir(dir string) string { return filepath.Join(dir, "bin") }

// paneSocket is where the pane holder for a window listens for respawn-pane.
// The number keeps the path short: a unix socket path is capped near 104
// bytes on macOS.
func paneSocket(dir, windowID string) string {
	return filepath.Join(dir, "p", strconv.FormatUint(uint64(PaneNumber(windowID)), 10)+".sock")
}

// TmuxValue is the TMUX value the shim's panes see:
// "socket,server-pid,session-index", the shape tmux writes.
func TmuxValue(dir string, pid int) string {
	return SocketPath(dir) + "," + strconv.Itoa(pid) + ",0"
}

// ForShim decides whether a `tmux` call is for the shim whose runtime
// directory is dir. A call naming a server with -L, or with -S a socket other
// than the shim's, is for a real tmux. Otherwise TMUX decides: the call is the
// shim's when TMUX names the shim's socket, which only the launcher and the
// shim's own panes set.
func ForShim(g Global, tmuxEnv, dir string) bool {
	if dir == "" || g.Name != "" {
		return false
	}
	if g.Socket != "" {
		return g.Socket == SocketPath(dir)
	}
	return SocketFromTmux(tmuxEnv) == SocketPath(dir)
}

// LauncherEnv is the environment `tuios tmux-shim` runs its command with:
// base with TMUX and TMUX_PANE naming the shim, the shim's bin directory first
// on PATH, and the log settings.
func LauncherEnv(base []string, dir, window, logPath string, logAll bool) []string {
	var extra []string
	if logPath != "" {
		extra = append(extra, EnvLog+"="+logPath)
	}
	if logAll {
		extra = append(extra, EnvLogAll+"=1")
	}
	return paneEnv(base, dir, window, extra)
}

// SocketFromTmux returns the socket field of a TMUX value.
func SocketFromTmux(tmux string) string {
	for i := 0; i < len(tmux); i++ {
		if tmux[i] == ',' {
			return tmux[:i]
		}
	}
	return tmux
}

// ExplicitSocket returns the socket path a tmux argv names with -S, read
// loosely: flags the shim does not know do not stop the scan, so a call such
// as `tmux -S <socket> -X ...` still names its socket. It returns "" when no
// -S comes before the command. runAsTmux uses it to keep a call that names
// the shim's socket away from a real tmux, which would otherwise start a
// server on that path.
func ExplicitSocket(args []string) string {
	sock := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || len(a) < 2 || a[0] != '-' {
			break
		}
		for j := 1; j < len(a); j++ {
			c := a[j]
			if !strings.ContainsRune("SLfcT", rune(c)) {
				continue
			}
			val := a[j+1:]
			if val == "" && i+1 < len(args) {
				i++
				val = args[i]
			}
			if c == 'S' {
				sock = val
			}
			break
		}
	}
	return sock
}

// sessionNumber is the number in a session's tmux id ($N) when the shim
// serves every session of the daemon: 20 bits of FNV-1a over the tuios
// session id, so it survives a rename. It is 0 in no case, since $0 is the
// id of the one session the shim serves in a pane.
func sessionNumber(sessionID string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(sessionID))
	n := h.Sum32() & 0xfffff
	if n == 0 {
		n = 1
	}
	return n
}

// windowStride separates the sessions in a window number when the shim
// serves every session: window N is workspace N%windowStride of the session
// whose number is N/windowStride. tmux window ids are unique on the server,
// and workspace numbers repeat in every session.
const windowStride = 1000

// InShimDir reports whether path is the shim's socket or any path inside the
// shim's runtime directory dir.
func InShimDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	p, d := filepath.Clean(path), filepath.Clean(dir)
	return p == filepath.Clean(SocketPath(dir)) || strings.HasPrefix(p, d+string(filepath.Separator))
}
