package app

import (
	"image/color"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The rail's git section: which repository the focused pane is in, which branch
// it has checked out, and how far that branch has drifted from its upstream.
//
// Two facts and no more, which is a decision. Dirtiness is the one git fact
// that cannot be had without walking the working tree, so it is the one that
// would make the rail stutter on a large repository, and it is also the fact
// you are least likely to be surprised by: you know whether you have edited
// something. See internal/gitstate for the cost model.

// gitRefreshInterval is how often the section asks again about a directory it
// is already showing. The reading itself is nearly free, because gitstate only
// spends a subprocess when the commits have moved, so this is about how soon a
// commit shows up on the rail rather than about cost.
const gitRefreshInterval = 2 * time.Second

// gitView is what the section is showing.
type gitView struct {
	// Dir is the directory State describes, which is not necessarily the one
	// the focused pane is in right now: a reading is taken on a goroutine and
	// the old one stays on screen until the new one lands, so the section never
	// blanks between two directories that are both repositories.
	Dir   string
	State gitstate.State
	Found bool

	// asked is the directory a reading is in flight for, and when it was asked.
	// Kept apart from Dir so an answer that arrives for a directory the focus
	// has already left can be dropped.
	asked string
	at    time.Time
}

// GitStateMsg carries a reading back from the goroutine that took it.
type GitStateMsg struct {
	Dir   string
	State gitstate.State
	Found bool
}

// gitSectionEnabled reports whether the rail is drawing the section at all.
// Asked once per message from Update, so it answers on the cheapest facts
// first.
func (m *OS) gitSectionEnabled() bool {
	if !m.Settings.SidebarEnabled {
		return false
	}
	if !sidebarLayoutHas(sidebarSectionGit, &m.Settings) {
		return false
	}
	w := m.GetSidebarWidth()
	return w > 0 && sidebarVariant(w) != sidebarVariantGlyph
}

// GitSyncCmd is the one place the section decides it needs a new reading. It is
// called once per message from Update, after the handler has run, so every path
// that can move the focus or change a pane's directory is covered by one
// comparison rather than by a hook in each of them.
//
// It answers nil, allocating nothing, for a client with no git section and for
// a section that asked recently enough, which is every message on an idle
// client.
func (m *OS) GitSyncCmd() tea.Cmd {
	if !m.gitSectionEnabled() {
		return nil
	}
	want := m.filesWantDir()
	if want == "" {
		// No pane, or a pane that has never said where it is. The section has
		// nothing to be about, and holding the last repository on screen would
		// be a claim about a pane that is not making it.
		if m.gitView.Dir != "" || m.gitView.asked != "" {
			m.gitView = gitView{}
		}
		return nil
	}
	if want == m.gitView.asked && time.Since(m.gitView.at) < gitRefreshInterval {
		return nil
	}
	m.gitView.asked, m.gitView.at = want, time.Now()
	return func() tea.Msg {
		state, found := gitstate.Read(want)
		return GitStateMsg{Dir: want, State: state, Found: found}
	}
}

// ApplyGitState takes a reading, unless the focus has moved on since it was
// asked for.
func (m *OS) ApplyGitState(msg GitStateMsg) {
	if msg.Dir != m.gitView.asked {
		return
	}
	m.gitView.Dir, m.gitView.State, m.gitView.Found = msg.Dir, msg.State, msg.Found
}

// gitRowKind is what a row of the section is.
type gitRowKind uint8

const (
	gitRowRepo gitRowKind = iota
	gitRowBranch
)

type gitRowSpec struct {
	Kind  gitRowKind
	Name  string
	Right string
}

// divergence is the "ahead behind" figure, or empty when there is nothing to
// say. A branch level with its upstream prints nothing rather than two zeros:
// the figure is for when something has drifted, and a pair of zeros on every
// row is a column that is only ever noise.
func divergence(ahead, behind int) string {
	switch {
	case ahead > 0 && behind > 0:
		return "↑" + strconv.Itoa(ahead) + " ↓" + strconv.Itoa(behind)
	case ahead > 0:
		return "↑" + strconv.Itoa(ahead)
	case behind > 0:
		return "↓" + strconv.Itoa(behind)
	}
	return ""
}

// gitRows is what the section draws, top to bottom.
func (m *OS) gitRows() []gitRowSpec {
	if !m.gitView.Found {
		return nil
	}
	st := m.gitView.State
	rows := make([]gitRowSpec, 0, 2)
	if st.Repo != "" {
		rows = append(rows, gitRowSpec{Kind: gitRowRepo, Name: st.Repo})
	}
	if st.Branch != "" {
		row := gitRowSpec{Kind: gitRowBranch, Name: st.Branch}
		if st.HasUpstream() {
			row.Right = divergence(st.Ahead, st.Behind)
		}
		rows = append(rows, row)
	}
	return rows
}

// sidebarGitRow draws one row.
//
// The figure is fixed width and the name is what gives way, which is the rule
// every list in this rail follows and the right way round here too: a branch
// cut to "feature/long-nam…" still says which branch, where "↑12" cut to "↑1"
// says something false.
func (m *OS) sidebarGitRow(row gitRowSpec, cw int, pal overlay.Palette, st sidebarRowState) string {
	rowBg := sidebarRowBg(st, pal)

	right, rightW := "", 0
	if row.Right != "" {
		right = gitDivergenceToken(row.Right, rowBg, pal)
		rightW = lipgloss.Width(row.Right)
	}

	ink := pal.FgDim
	glyph := sidebarStyle(rowBg, pal.FgMute).Render(m.Settings.GetRailBullet())
	if row.Kind == gitRowRepo {
		// The repository is the name you act on, so it takes the brighter ink,
		// the same way a session name outranks the machine it is under.
		ink = pal.Fg
		glyph = sidebarQuietDot(rowBg, pal, &m.Settings)
	}
	if st.lit() {
		ink = pal.Fg
	}

	indent := 0
	if row.Kind == gitRowBranch {
		indent = m.sidebarRowIndent()
	}
	name := sidebarStyle(rowBg, ink).Render(
		overlay.Truncate(printableTitle(row.Name), sidebarNameAvailIn(cw, rightW, indent)))
	gutter := sidebarGutter(false, "", rowBg, pal, &m.Settings)
	return sidebarComposeGroupRow(indent, gutter, glyph, name, right, cw, rowBg)
}

// gitDivergenceToken colours the two halves of the figure separately: commits
// you have and the upstream does not are not the same news as commits it has
// and you do not, and one ink for both would make a row that needs a pull look
// like a row that needs a push.
func gitDivergenceToken(text string, rowBg color.Color, pal overlay.Palette) string {
	out := ""
	for _, part := range splitDivergence(text) {
		ink := pal.Success
		if part.behind {
			ink = pal.Warn
		}
		out += sidebarStyle(rowBg, ink).Render(part.text)
	}
	return out
}

type divergencePart struct {
	text   string
	behind bool
}

// splitDivergence cuts the figure back into its halves, separator included, so
// the two can be inked apart without the caller reassembling spacing.
func splitDivergence(text string) []divergencePart {
	runes := []rune(text)
	parts := make([]divergencePart, 0, 2)
	start, behind := 0, false
	for i := 0; i < len(runes); i++ {
		if runes[i] != '↑' && runes[i] != '↓' {
			continue
		}
		if i > start {
			parts = append(parts, divergencePart{string(runes[start:i]), behind})
		}
		behind = runes[i] == '↓'
		start = i
	}
	if start < len(runes) {
		parts = append(parts, divergencePart{string(runes[start:]), behind})
	}
	return parts
}
