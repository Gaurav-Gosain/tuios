package app

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

var railCheckPath = os.Getenv("TUIOS_RAIL_CHECK")

const railCheckMax = 20

var railCheckState struct {
	sync.Mutex
	written int
	seen    map[string]bool
}

func (m *OS) checkRailCache(ext railGens) {
	if railCheckPath == "" {
		return
	}
	if diff := m.railCacheMiss(ext); diff != "" {
		reportRailMiss(diff)
	}
}

func (m *OS) railCacheMiss(ext railGens) string {
	c := &m.sidebarCache
	rails := m.tickStats.Rail
	lines, w := m.sidebarPanelLinesForTree(m.BuildSessionTree())
	m.tickStats.Rail = rails
	if m.railExternalGens() != ext {
		return ""
	}
	if pane := m.railPaneWindow(); pane != nil {
		if begun, _ := pane.EmulatorWrites(); begun != m.railPaneView.noted {
			return ""
		}
	}
	return railCacheDiff(c, lines, w, m)
}

type railGens struct{ daemon, theme uint64 }

func (m *OS) railExternalGens() railGens {
	g := railGens{theme: theme.Generation()}
	if m.DaemonClient != nil {
		g.daemon = m.DaemonClient.CacheGen()
	}
	return g
}

func railCacheDiff(c *sidebarRenderCache, lines []string, w int, m *OS) string {
	var b strings.Builder
	if w != c.w {
		fmt.Fprintf(&b, "width: cached %d, built %d\n", c.w, w)
	}
	if len(lines) != len(c.lines) {
		fmt.Fprintf(&b, "rows: cached %d, built %d\n", len(c.lines), len(lines))
	}
	for i := range min(len(lines), len(c.lines)) {
		if lines[i] == c.lines[i] {
			continue
		}
		fmt.Fprintf(&b, "row %d:\n  cached %q\n  built  %q\n", i, ansi.Strip(c.lines[i]), ansi.Strip(lines[i]))
		if ansi.Strip(c.lines[i]) == ansi.Strip(lines[i]) {
			fmt.Fprintf(&b, "  (style only)\n  cached %q\n  built  %q\n", c.lines[i], lines[i])
		}
		break
	}
	if !slices.Equal(m.SidebarHits, c.hits) {
		b.WriteString("hit rectangles differ\n")
	}
	if !slices.Equal(m.SidebarSessionIDs, c.sessionIDs) || !slices.Equal(m.SidebarHostIDs, c.hostIDs) {
		b.WriteString("session or host ids differ\n")
	}
	if !slices.Equal(m.SidebarNav, c.nav) {
		b.WriteString("nav rows differ\n")
	}
	if m.sidebarSectionY != c.sections {
		b.WriteString("section rows differ\n")
	}
	if b.Len() > 0 {
		fmt.Fprintf(&b, "session %q, focused window %d\n", m.SessionName, m.FocusedWindow)
		for i := range max(len(lines), len(c.lines)) {
			var was, now string
			if i < len(c.lines) {
				was = ansi.Strip(c.lines[i])
			}
			if i < len(lines) {
				now = ansi.Strip(lines[i])
			}
			mark := " "
			if was != now {
				mark = "*"
			}
			fmt.Fprintf(&b, "%s %2d %-30s | %s\n", mark, i, was, now)
		}
	}
	return b.String()
}

func reportRailMiss(diff string) {
	s := &railCheckState
	s.Lock()
	defer s.Unlock()
	if s.written >= railCheckMax || s.seen[diff] {
		return
	}
	if s.seen == nil {
		s.seen = make(map[string]bool)
	}
	s.seen[diff] = true
	s.written++
	f, err := os.OpenFile(railCheckPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "rail served from its cache differs from a fresh build:\n%s\n", diff)
}
