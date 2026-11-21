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

const pathStatTTL = 30 * time.Second

const pathStatMax = 1024

type pathStat struct {
	exists, dir bool
	at          time.Time
}

type pathCache struct {
	known   map[string]pathStat
	asked   map[string]bool
	pending []string
}

type pathStatMsg struct{ stats map[string]pathStat }

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
	sortx.SortFunc(all, func(a, b aged) int { return a.at.Compare(b.at) })
	for _, a := range all[:len(all)/2] {
		delete(c.known, a.p)
	}
}

func pathLinkURL(abs string, line int) string {
	s := (&url.URL{Scheme: "file", Path: abs}).String()
	if line > 0 {
		s += "#L" + strconv.Itoa(line)
	}
	return s
}

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

func (m *OS) pathsOn() bool { return m.Settings.Links == config.LinksAll }

func (m *OS) panePathBlocked(window *terminal.Window) bool {
	if window.Host != "" || m.AttachedHost != "" || slices.Contains(hintRemoteShells, hintForeground(window)) {
		return true
	}
	host, ok := hintCwdHost(window.Cwd)
	return ok && host != "" && !isLocalHost(host)
}

const (
	pathAsk = iota
	pathPeek
	pathNow
	pathPeekKnown
)

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
