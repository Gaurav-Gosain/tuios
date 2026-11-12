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

// The rail check finds the inputs the rail's cache key misses. With
// TUIOS_RAIL_CHECK naming a file, every frame the cache would serve also
// builds the rail again and compares the two. A difference is an input that
// changed what the rail draws without changing sidebarSignature: the person
// would have been shown the old rail. It is written to the file, and the e2e
// harness fails the test that drew it.
//
// It costs a full rail build on every frame, so it is for tests only.

// railCheckPath is the file the rail check writes to, or "" when it is off.
var railCheckPath = os.Getenv("TUIOS_RAIL_CHECK")

// railCheckMax bounds the reports one process writes: the first misses name
// the input, and a key that misses on every frame would fill the disk.
const railCheckMax = 20

var railCheckState struct {
	sync.Mutex
	written int
	seen    map[string]bool
}

// checkRailCache builds the rail again on a frame the cache serves and
// reports how the build differs from the cached rail. The build's side effects
// on the frame's buffers are replaced by the cache's afterwards, as a cache
// hit does, and the rebuild is not counted as a rail draw.
func (m *OS) checkRailCache(ext railGens) {
	if railCheckPath == "" {
		return
	}
	if diff := m.railCacheMiss(ext); diff != "" {
		reportRailMiss(diff)
	}
}

// railCacheMiss builds the rail again and says how it differs from the
// cache, or "" when it does not, or when it cannot be compared. ext is what
// railExternalGens read when this frame's key was taken.
//
// The daemon client's listing and a live theme change on goroutines of their
// own, and the key reads their generations without a lock. A listing that lands
// after the key is read and before the rebuild is in the rebuild and not in the
// key, which is not a key that misses an input: the generation moves when the
// listing lands, and the frame its message asks for draws it. That case is not
// compared.
func (m *OS) railCacheMiss(ext railGens) string {
	c := &m.sidebarCache
	rails := m.tickStats.Rail
	lines, w := m.sidebarPanelLinesForTree(m.BuildSessionTree())
	m.tickStats.Rail = rails
	if m.railExternalGens() != ext {
		return ""
	}
	return railCacheDiff(c, lines, w, m)
}

// railGens are the generations the rail's key folds that move off the UI
// goroutine.
type railGens struct{ daemon, theme uint64 }

func (m *OS) railExternalGens() railGens {
	g := railGens{theme: theme.Generation()}
	if m.DaemonClient != nil {
		g.daemon = m.DaemonClient.CacheGen()
	}
	return g
}

// railCacheDiff says how a fresh build differs from the cache, or "" when it
// does not. The fresh build's buffers are read from m, where the build left
// them.
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
		// Both rails whole, so the rows around the first difference say which
		// section and which row moved.
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

// reportRailMiss appends one miss to the check's file, once per distinct
// difference and at most railCheckMax times.
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
