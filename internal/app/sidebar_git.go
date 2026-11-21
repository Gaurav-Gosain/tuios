package app

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type gitView struct {
	Dir   string
	State gitstate.State
	Dirty gitstate.Dirty
	Found bool

	asked string
	at    time.Time

	gen uint64
}

// GitStateMsg carries a reading back from the goroutine that took it.
type GitStateMsg struct {
	Dir   string
	State gitstate.State
	Dirty gitstate.Dirty
	Found bool
}

func (m *OS) gitSectionEnabled() bool {
	if !m.Settings.SidebarEnabled && !m.sidebarDrawing {
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
	want := m.gitWantDir()
	if want == "" {
		if m.gitView.Dir != "" || m.gitView.asked != "" {
			m.gitView = gitView{}
		}
		return nil
	}
	if want == m.gitView.asked && time.Since(m.gitView.at) < m.gitRefreshInterval() {
		return nil
	}
	m.gitView.asked, m.gitView.at = want, time.Now()
	dirty := m.Settings.SidebarGitDirty
	return func() tea.Msg {
		state, found := gitstate.Read(want)
		msg := GitStateMsg{Dir: want, State: state, Found: found}
		if found && dirty {
			msg.Dirty, _ = gitstate.ReadDirty(state.Root)
		}
		return msg
	}
}

func (m *OS) gitWantDir() string {
	w := m.GetFocusedWindow()
	if w != nil && m.paneIsLocal(w) && !(m.filesView.Pinned && m.filesView.Origin == w.ID) {
		if dir, ok := paneShellDir(w, false); ok {
			return dir
		}
	}
	return m.filesWantDir()
}

// ApplyGitState takes a reading, unless the focus has moved on since it was
// asked for.
func (m *OS) ApplyGitState(msg GitStateMsg) {
	if msg.Dir != m.gitView.asked {
		return
	}
	m.gitView.Dir, m.gitView.State, m.gitView.Found = msg.Dir, msg.State, msg.Found
	m.gitView.Dirty = msg.Dirty
	m.gitView.gen++
}

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

func dirtyFigure(d gitstate.Dirty) string {
	if !d.Any() {
		return ""
	}
	out := ""
	add := func(mark string, n int) {
		if n == 0 {
			return
		}
		if out != "" {
			out += " "
		}
		out += mark + strconv.Itoa(n)
	}
	add("+", d.Staged)
	add("!", d.Modified)
	add("?", d.Untracked)
	return out
}

func problemFigure(st gitstate.State) string {
	switch {
	case st.Conflicts > 0:
		return "✕" + strconv.Itoa(st.Conflicts)
	case st.Divergent > 0:
		return "◇" + strconv.Itoa(st.Divergent)
	}
	return ""
}

func (m *OS) gitRows() []gitRowSpec {
	if !m.gitView.Found {
		return nil
	}
	st := m.gitView.State
	rows := make([]gitRowSpec, 0, 2)
	if st.Repo != "" {
		right := problemFigure(st)
		if right == "" {
			right = dirtyFigure(m.gitView.Dirty)
		}
		rows = append(rows, gitRowSpec{Kind: gitRowRepo, Name: st.Repo, Right: right})
	}
	if st.Branch != "" {
		row := gitRowSpec{Kind: gitRowBranch, Name: st.Branch}
		if st.HasUpstream() {
			row.Right = divergence(st.Ahead, st.Behind)
		}
		if st.Unbookmarked > 0 {
			row.Right = strings.TrimSpace("+" + strconv.Itoa(st.Unbookmarked) + " " + row.Right)
		}
		rows = append(rows, row)
	}
	return rows
}

func (m *OS) sidebarGitRow(row gitRowSpec, cw int, pal overlay.Palette, st sidebarRowState) string {
	rowBg := sidebarRowBg(st, pal)

	right, rightW := "", 0
	if row.Right != "" {
		rightW = lipgloss.Width(row.Right)
		if row.Kind == gitRowBranch {
			right = gitDivergenceToken(row.Right, rowBg, pal)
		} else {
			right = sidebarStyle(rowBg, pal.Warn).Render(row.Right)
		}
	}

	ink := pal.FgDim
	glyph := sidebarStyle(rowBg, pal.FgMute).Render(m.Settings.GetRailBullet())
	if row.Kind == gitRowRepo {
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

func gitDivergenceToken(text string, rowBg color.Color, pal overlay.Palette) string {
	var out strings.Builder
	for _, part := range splitDivergence(text) {
		ink := pal.Success
		if part.behind {
			ink = pal.Warn
		}
		out.WriteString(sidebarStyle(rowBg, ink).Render(part.text))
	}
	return out.String()
}

type divergencePart struct {
	text   string
	behind bool
}

func splitDivergence(text string) []divergencePart {
	runes := []rune(text)
	parts := make([]divergencePart, 0, 2)
	start, behind := 0, false
	for i := range runes {
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

func (m *OS) gitRefreshInterval() time.Duration {
	ms := m.Settings.SidebarGitRefreshMS
	if ms <= 0 {
		ms = config.SidebarGitRefreshMSDefault
	}
	return time.Duration(ms) * time.Millisecond
}
