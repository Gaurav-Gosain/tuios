package app

import (
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// File paths in pane output and in the agents view act like links: the
// pointer underlines one and a click opens it in quick look, at the line the
// text names.
//
// A path counts only when it names a file or a folder that is there. The
// text alone cannot say so: "src/main.go" in a log may be a file of another
// checkout, and an underline on text that opens nothing is a promise tuios
// does not keep. So the pointer's first stop on a path asks the disk, off the
// loop, and the answer is kept for a while. Until the answer comes the path is
// plain text. Nothing here runs on a frame, and nothing runs while the pointer
// is still.
//
// Which machine: the client process runs on the pane machine in every
// deployment tuios has (see link_open.go), so the stat and quick look's read
// run where the file is, for a local client, under tuios ssh and under
// tuios-web alike. A pane of another machine (a linked host, a session
// reached through another host, a remote shell in the pane) is skipped: its
// files are not here.

// pathStatTTL is how long an answer about a path is kept. A file a build
// writes shows up on the pointer's next stop after it.
const pathStatTTL = 30 * time.Second

// pathStatMax bounds the answers kept. Past it the oldest half goes.
const pathStatMax = 1024

// pathStat is what the disk said about a path.
type pathStat struct {
	exists, dir bool
	at          time.Time
}

// pathCache holds the answers, the paths asked about and not answered yet,
// and the paths to ask about next.
type pathCache struct {
	known   map[string]pathStat
	asked   map[string]bool
	pending []string
}

// pathStatMsg carries the disk's answers back to the loop.
type pathStatMsg struct{ stats map[string]pathStat }

// pathKnown returns what is known about abs, and asks the disk when nothing
// is, or what is known is old. The ask runs with TakePathStatCmd.
func (m *OS) pathKnown(abs string) (pathStat, bool) {
	c := &m.paths
	st, ok := c.known[abs]
	if ok && time.Since(st.at) < pathStatTTL {
		return st, true
	}
	if c.asked == nil {
		c.asked = map[string]bool{}
	}
	if !c.asked[abs] {
		c.asked[abs] = true
		c.pending = append(c.pending, abs)
	}
	return st, ok
}

// pathMaybe reports whether abs may be a file, without asking anything: true
// when the disk said so or has not been asked. The motion filter reads it, so
// a motion over a path nobody asked about still reaches the handler that asks.
func (m *OS) pathMaybe(abs string) bool {
	st, ok := m.paths.known[abs]
	return !ok || time.Since(st.at) >= pathStatTTL || st.exists
}

// TakePathStatCmd returns the read of the paths the pointer asked about, and
// clears the list. The input path calls it after every input.
func (m *OS) TakePathStatCmd() tea.Cmd {
	c := &m.paths
	if len(c.pending) == 0 {
		return nil
	}
	paths := c.pending
	c.pending = nil
	return func() tea.Msg {
		out := make(map[string]pathStat, len(paths))
		now := time.Now()
		for _, p := range paths {
			info, err := os.Stat(p)
			out[p] = pathStat{exists: err == nil, dir: err == nil && info.IsDir(), at: now}
		}
		return pathStatMsg{stats: out}
	}
}

// applyPathStats keeps the answers, then resolves the pointer again, so the
// path it rests on is underlined as soon as the disk said it is there.
func (m *OS) applyPathStats(msg pathStatMsg) {
	m.applyPathStatsQuiet(msg.stats)
	if m.ShowInbox {
		ck := &m.Inbox.ck
		m.cockpitLinkHover(ck.linkPointX, ck.linkPointY)
		return
	}
	x, y := m.PointerSeen()
	m.TrackLinkPointer(x, y)
}

// applyPathStatsQuiet keeps the answers.
func (m *OS) applyPathStatsQuiet(stats map[string]pathStat) {
	c := &m.paths
	if c.known == nil {
		c.known = map[string]pathStat{}
	}
	if len(c.known)+len(stats) > pathStatMax {
		m.trimPathCache()
	}
	for p, st := range stats {
		c.known[p] = st
		delete(c.asked, p)
	}
}

// trimPathCache drops the older half of the answers.
func (m *OS) trimPathCache() {
	c := &m.paths
	type aged struct {
		p  string
		at time.Time
	}
	all := make([]aged, 0, len(c.known))
	for p, st := range c.known {
		all = append(all, aged{p, st.at})
	}
	// The trim runs once per pathStatMax answers, so it sorts through the
	// shared sortx copy rather than a slices.SortFunc of its own.
	sortx.SortFunc(all, func(a, b aged) int { return a.at.Compare(b.at) })
	for _, a := range all[:len(all)/2] {
		delete(c.known, a.p)
	}
}

// pathLinkURL is the address a found path opens: the file:// URL of the
// absolute path, with #L and the line when the text named one.
func pathLinkURL(abs string, line int) string {
	s := (&url.URL{Scheme: "file", Path: abs}).String()
	if line > 0 {
		s += "#L" + strconv.Itoa(line)
	}
	return s
}

// splitLineFragment takes the #L and line pathLinkURL adds off a file link.
func splitLineFragment(rawURL string) (string, int) {
	i := strings.LastIndex(rawURL, "#L")
	if i < 0 {
		return rawURL, 0
	}
	n, err := strconv.Atoi(rawURL[i+2:])
	if err != nil || n <= 0 {
		return rawURL, 0
	}
	return rawURL[:i], n
}

// fileLinkLabel is a file link's path as a label names it: the home folder
// as ~, and the line after a colon. ok is false for any other link.
func fileLinkLabel(rawURL string) (string, bool) {
	bare, line := splitLineFragment(rawURL)
	p, ok := linkFilePath(bare)
	if !ok {
		return "", false
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rest, under := strings.CutPrefix(p, home); under && (rest == "" || rest[0] == '/') {
			p = "~" + rest
		}
	}
	if line > 0 {
		p += ":" + strconv.Itoa(line)
	}
	return p, true
}

// pathsOn reports whether paths act as links: with links = "all", as bare
// URLs do.
func (m *OS) pathsOn() bool { return m.Settings.Links == config.LinksAll }

// panePathBlocked reports whether a path printed in the pane names a file of
// another machine, so no file here may stand for it.
func (m *OS) panePathBlocked(window *terminal.Window) bool {
	if window.Host != "" || m.AttachedHost != "" || slices.Contains(hintRemoteShells, hintForeground(window)) {
		return true
	}
	host, ok := hintCwdHost(window.Cwd)
	return ok && host != "" && !isLocalHost(host)
}

// How a path's answer is found: pathAsk asks the disk off the loop when
// nothing is known (the pointer), pathPeek only reads what is known and takes
// an unknown path as a file (the motion filter, so the motion reaches the
// handler that asks), and pathNow asks the disk at once (a click, which may
// come with no motion before it).
const (
	pathAsk = iota
	pathPeek
	pathNow
	// pathPeekKnown reads only what is known, and takes an unknown path as
	// no file: the frame, which never asks the disk.
	pathPeekKnown
)

// pathThere reports whether abs is a file or folder, found as how says.
func (m *OS) pathThere(abs string, how int) bool {
	switch how {
	case pathPeek:
		return m.pathMaybe(abs)
	case pathPeekKnown:
		st, ok := m.paths.known[abs]
		return ok && st.exists
	case pathNow:
		if st, ok := m.paths.known[abs]; ok && time.Since(st.at) < pathStatTTL {
			return st.exists
		}
		info, err := os.Stat(abs)
		m.applyPathStatsQuiet(map[string]pathStat{abs: {exists: err == nil, dir: err == nil && info.IsDir(), at: time.Now()}})
		return err == nil
	}
	st, known := m.pathKnown(abs)
	return known && st.exists
}

// panePathAt finds a path covering viewport cell x, y of the pane and returns
// it as a link, when it names a file or folder here (see pathThere).
func (m *OS) panePathAt(window *terminal.Window, x, y int, how int) (PaneLink, bool) {
	if !m.pathsOn() || window == nil || window.Terminal == nil || m.panePathBlocked(window) {
		return PaneLink{}, false
	}
	if !window.TryRLockIO() {
		return PaneLink{}, false
	}
	maxX := min(window.ContentWidth(), window.Terminal.Width())
	if x < 0 || y < 0 || x >= maxX || y >= window.ContentHeight() {
		window.RUnlockIO()
		return PaneLink{}, false
	}
	text, byteAt := paneRowText(window, y, maxX)
	window.RUnlockIO()
	if byteAt[x] < 0 {
		return PaneLink{}, false
	}
	s, e, ok := hints.PathAt(text, byteAt[x])
	if !ok {
		return PaneLink{}, false
	}
	p, line, _ := hints.SplitLineCol(text[s:e])
	abs, why := hintLocalPath(window, p)
	if why != "" {
		return PaneLink{}, false
	}
	if !m.pathThere(abs, how) {
		return PaneLink{}, false
	}
	x0, x1 := -1, -1
	for col, off := range byteAt {
		if off >= s && off < e {
			if x0 < 0 {
				x0 = col
			}
			x1 = col
		}
	}
	return PaneLink{WindowID: window.ID, URL: pathLinkURL(abs, line), Y0: y, X0: x0, Y1: y, X1: x1, Row: y, Col: x}, true
}

// canvasPathAt finds a path covering column x of a line of the agents view,
// placed against dir when it is relative, and returns it as a link when the
// disk said it is there.
func (m *OS) canvasPathAt(line uv.Line, x int, dir string, how int) (url string, x0, x1 int, ok bool) {
	if !m.pathsOn() {
		return "", 0, 0, false
	}
	text, byteAt := canvasRowText(line)
	at := byteAt[x]
	for i := x; at < 0 && i > 0; i-- {
		at = byteAt[i-1]
	}
	s, e, found := hints.PathAt(text, max(at, 0))
	if !found {
		return "", 0, 0, false
	}
	p, ln, _ := hints.SplitLineCol(text[s:e])
	abs, placed := placePath(p, dir)
	if !placed {
		return "", 0, 0, false
	}
	if !m.pathThere(abs, how) {
		return "", 0, 0, false
	}
	x0 = -1
	for col, off := range byteAt {
		if off >= s && off < e {
			if x0 < 0 {
				x0 = col
			}
			x1 = col + max(line[col].Width, 1)
		}
	}
	if x0 < 0 {
		return "", 0, 0, false
	}
	return pathLinkURL(abs, ln), x0, min(x1, len(line)), true
}

// placePath makes p absolute: ~ is the home folder, and a relative path is
// taken from dir. A relative path with no dir is not placed.
func placePath(p, dir string) (string, bool) {
	if rest, ok := strings.CutPrefix(p, "~"); ok && (rest == "" || rest[0] == '/') {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		return filepath.Join(home, rest), true
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), true
	}
	if dir == "" {
		return "", false
	}
	return filepath.Join(dir, p), true
}

// cockpitPathDir is the folder a relative path in the selected agent's
// thread is taken from: its worktree, or the folder its pane's shell last
// reported. "" when neither is known, or the agent is on another machine.
func (m *OS) cockpitPathDir() string {
	e, ok := m.cockpitAgent()
	if !ok || e.Host != "" || m.AttachedHost != "" {
		return ""
	}
	if wt := m.ckWorktreeOf(e); wt.path != "" {
		return wt.path
	}
	if e.Foreign {
		return ""
	}
	w := m.windowByID(e.WindowID)
	if w == nil {
		return ""
	}
	if w.Cwd != "" {
		if cwd, ok := localCwdPath(w.Cwd); ok {
			return cwd
		}
		return ""
	}
	return w.CWD()
}
