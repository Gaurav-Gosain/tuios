package tutor

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// View draws the screen on show.
func (m *Model) View() tea.View {
	m.hits = m.hits[:0]
	if m.w <= 0 || m.h <= 0 {
		return tea.NewView("")
	}
	if m.tooSmall() {
		return m.full([]string{
			m.styles().title.Render("Make the window bigger"),
			"",
			"The tutor needs at least " + itoa(stripCols) + " by " + itoa(stripRows) + ".",
			"This window is " + itoa(m.w) + " by " + itoa(m.h) + ".",
			"Your lesson waits here.",
		}, nil, "q leave")
	}
	switch m.scr {
	case scrNested:
		return m.full([]string{
			m.styles().title.Render("tuios tutor"),
			"",
			"You are inside tuios. The outer tuios takes ctrl+b.",
			"Press ctrl+b then enter to give every key to the tutor.",
			"F12 gives the keys back.",
		}, []row{{label: "Start", do: func() tea.Cmd { m.scr, m.sel = scrMenu, 0; return nil }}}, "enter start    q leave")
	case scrMenu:
		lines := []string{
			m.styles().title.Render("tuios tutor"),
			"",
			// One paragraph, so it wraps at the window's width.
			"Learn tuios by doing it. The shell, the files and the agents here are a practice copy. Nothing on your computer changes.",
		}
		if m.saveErr != "" {
			lines = append(lines, "", m.styles().warn.Render(m.saveErr))
		}
		return m.full(lines, m.rows(), "enter start    up and down choose    q leave")
	case scrPause:
		title := "Paused"
		if m.track != nil {
			title = m.track.Title + ", paused"
		}
		return m.full([]string{m.styles().title.Render(title), "", "The lesson waits. Pick what to do next."}, m.rows(), "enter choose    esc back to the lesson    q leave")
	case scrFinish:
		return m.finishView()
	}
	return m.lessonView()
}

// styles are the tutor's inks and grounds, from the theme's dialog palette,
// as all chrome is.
type styles struct {
	pal                               overlay.Palette
	ground                            color.Color
	title, text, dim, mute, warn, key lipgloss.Style
}

// styles are the inks on the screens of the tutor's own, which sit on the
// terminal's own background.
func (m *Model) styles() styles { return stylesOn(theme.UI(), overlay.NoColor) }

// cardStyles are the inks on the card. The card sits on the dialog surface,
// so it reads as apart from tuios above it. At 16 colours the surface is the
// terminal's own background, and the bar's glyphs set the card apart.
func (m *Model) cardStyles() styles {
	pal := theme.UI()
	g := pal.Surface
	if pal.Depth == overlay.Depth16 {
		g = overlay.NoColor
	}
	return stylesOn(pal, g)
}

func stylesOn(pal overlay.Palette, g color.Color) styles {
	on := func(c color.Color) lipgloss.Style {
		return overlay.Style(g).Foreground(overlay.Readable(c, g))
	}
	return styles{
		pal:    pal,
		ground: g,
		title:  on(pal.Accent).Bold(true),
		text:   on(pal.Fg),
		dim:    on(pal.FgDim),
		mute:   on(pal.FgMute),
		warn:   on(pal.Warning),
		key:    on(pal.AccentBright).Bold(true),
	}
}

// lessonView is tuios with the card under it.
func (m *Model) lessonView() tea.View {
	v := m.tour.View()
	th := m.tourHeight()
	lines := strings.SplitN(v.Content, "\n", th+1)
	for len(lines) < th {
		lines = append(lines, "")
	}
	lines = lines[:th]
	var card []string
	if m.cardRows() == 4 {
		card = m.card(th)
	} else {
		card = m.strip(th)
	}
	v.Content = strings.Join(lines, "\n") + "\n" + strings.Join(card, "\n")
	return v
}

// lessonState is what the card draws, read from the lesson engine.
type lessonState struct {
	phase                 string
	shown, total, pressed int
	level                 int
	nudge                 string
	waiting               bool
	results               []string
	step                  *lessons.Step
	explainer             bool
	booting               bool
	hasLesson             bool
	noWindow, altB        bool
}

func (m *Model) lessonState() lessonState {
	st := m.lessons.State()
	ls := lessonState{}
	ls.phase, _ = st["phase"].(string)
	ls.shown, _ = st["shown"].(int)
	ls.total, _ = st["total"].(int)
	ls.pressed, _ = st["pressed"].(int)
	ls.level, _ = st["level"].(int)
	ls.nudge, _ = st["nudge"].(string)
	ls.waiting, _ = st["waiting"].(bool)
	ls.noWindow, _ = st["noWindow"].(bool)
	ls.altB, _ = st["altBlocked"].(bool)
	if rs, ok := st["results"].([]any); ok {
		for _, r := range rs {
			s, _ := r.(string)
			ls.results = append(ls.results, s)
		}
	}
	ls.booting = ls.phase == "booting" || ls.phase == ""
	if m.track != nil {
		ls.total = len(m.track.Steps)
		ls.step = m.lessonStep()
		ls.hasLesson = ls.step != nil && !ls.booting
		ls.explainer = ls.step != nil && ls.step.Explainer != nil
	}
	return ls
}

// card is the 4-row card at y.
func (m *Model) card(y int) []string {
	s := m.cardStyles()
	ls := m.lessonState()
	w := m.w

	// Row 1: the track, where the lesson is, and the bar.
	head := " " + m.track.Title + "  "
	if ls.hasLesson {
		head += itoa(ls.shown+1) + " of " + itoa(ls.total) + "  "
	}
	row1 := s.title.Render(head) + m.bar(ls, w-ansi.StringWidth(head)-1) + s.text.Render(" ")

	// Row 2: the step's title, and its keys.
	title, caps := "Getting the screen ready", ""
	if ls.hasLesson {
		title = ls.step.Title
		caps = m.keycaps(ls)
	}
	row2 := m.spread(s.text.Bold(true).Render(" "+title), caps+s.text.Render("  "), w)

	// Row 3: the note, or the hint once the ladder reaches it.
	row3 := s.dim.Render(" " + m.noteLine(ls))

	// Row 4: the tutor's own keys, and the coach.
	buttons, x := m.buttons(ls, y+3)
	row4 := m.spread(buttons, m.coachText(s, w-x-4)+s.text.Render(" "), w)

	return []string{
		overlay.Fill(ansi.Truncate(row1, w, ""), w, s.ground),
		overlay.Fill(ansi.Truncate(row2, w, ""), w, s.ground),
		overlay.Fill(ansi.Truncate(row3, w, "…"), w, s.ground),
		overlay.Fill(ansi.Truncate(row4, w, ""), w, s.ground),
	}
}

// strip is the 2-row card for a window under 80x24.
func (m *Model) strip(y int) []string {
	s := m.cardStyles()
	ls := m.lessonState()
	w := m.w
	head := " " + m.track.Title
	if ls.hasLesson {
		head = " " + itoa(ls.shown+1) + "/" + itoa(ls.total) + " " + ls.step.Title
	}
	caps := ""
	if ls.hasLesson {
		caps = m.keycaps(ls)
	}
	menu := s.key.Render("F2") + s.dim.Render(" menu ")
	right := menu
	if caps != "" {
		right = caps + s.text.Render("  ") + menu
	}
	room := w - lipgloss.Width(right) - 1
	m.hits = append(m.hits, hit{y: y, x0: w - lipgloss.Width(menu), x1: w, do: func() tea.Cmd { m.openPause(); return nil }})
	row1 := m.spread(s.text.Bold(true).Render(ansi.Truncate(head, max(room, 4), "…")), right, w)
	line := m.noteLine(ls)
	if c := m.coachText(s, w-2); c != "" && ls.level == 0 && ls.nudge == "" {
		row2 := s.text.Render(" ") + c
		return []string{overlay.Fill(row1, w, s.ground), overlay.Fill(ansi.Truncate(row2, w, "…"), w, s.ground)}
	}
	return []string{
		overlay.Fill(ansi.Truncate(row1, w, ""), w, s.ground),
		overlay.Fill(ansi.Truncate(s.dim.Render(" "+line), w, "…"), w, s.ground),
	}
}

// spread puts left at the start of a w-cell row and right at its end.
func (m *Model) spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + overlay.Style(m.cardStyles().ground).Render(strings.Repeat(" ", gap)) + right
}

// bar draws one segment per step: thick for a step done, on this run or an
// earlier one, thin for a step to go. The glyphs carry it, so it reads with
// no colour at all.
func (m *Model) bar(ls lessonState, width int) string {
	s := m.cardStyles()
	n := len(m.track.Steps)
	if width < n || n == 0 {
		return ""
	}
	done, todo := "━", "─"
	if m.ascii {
		done, todo = "=", "-"
	}
	var b strings.Builder
	for i := range n {
		cells := (i+1)*width/n - i*width/n
		seg := todo
		st := s.mute
		switch {
		case i < len(ls.results) && ls.results[i] != "":
			seg, st = done, overlay.Style(s.ground).Foreground(overlay.Readable(s.pal.Success, s.ground))
		case m.doneBefore[m.track.Steps[i].ID]:
			seg, st = done, s.dim
		case i == ls.shown && ls.hasLesson:
			seg, st = done, s.key
		}
		b.WriteString(st.Render(strings.Repeat(seg, cells)))
	}
	return b.String()
}

// keycaps draws the step's keys, the ones pressed so far lit.
func (m *Model) keycaps(ls lessonState) string {
	s := m.cardStyles()
	if ls.explainer {
		return s.key.Render("enter") + s.dim.Render(" go on")
	}
	var parts []string
	for i, k := range ls.step.Keys {
		label := k.Chord
		if k.Text != "" {
			label = k.Text
		}
		if ansi.StringWidth(label) == 1 {
			label = " " + label + " "
		}
		lit := i < ls.pressed
		parts = append(parts, m.keycap(label, lit))
	}
	return strings.Join(parts, s.text.Render(" "))
}

// keycap is one key. At 16 colours and with no colour it keeps its brackets
// and a lit key turns to reverse video, so the state never rests on colour.
func (m *Model) keycap(label string, lit bool) string {
	s := m.cardStyles()
	if s.pal.Depth == overlay.Depth16 {
		capText := "[" + label + "]"
		if lit {
			return s.pal.Mark(lipgloss.NewStyle().Bold(true).Render(capText), overlay.RowState{Cursor: true, Focused: true})
		}
		return s.text.Render(capText)
	}
	if lit {
		g := s.pal.Success
		return lipgloss.NewStyle().Background(g).Foreground(overlay.Readable(s.pal.Canvas, g)).Bold(true).Render(" " + label + " ")
	}
	return overlay.KeyBadge(label, s.pal)
}

// noteLine is the card's third row: what the person needs right now.
func (m *Model) noteLine(ls lessonState) string {
	switch {
	case !ls.hasLesson:
		return "One moment."
	case ls.explainer:
		body := ls.step.Explainer.Body
		if ls.step.Try != "" {
			body += " After the tutor, try " + ls.step.Try + "."
		}
		return body
	case ls.waiting:
		return ls.step.Waiting
	}
	switch ls.nudge {
	case "noWindow":
		return "There is no window to work with. Press n for a new one."
	case "altBlocked":
		return "Your alt keys do not reach tuios. Turn on Option as Meta in your terminal."
	case "wrongModeTerminal":
		return "You are in window mode. Press i to type into the shell."
	case "wrongModeWindow":
		return "You are typing into the shell. Press ctrl+b, then esc, for window mode."
	}
	if ls.level >= 1 {
		return "Hint: " + ls.step.Hint
	}
	return ls.step.Note
}

// buttons draws the tutor's keys on row y, each one a click target. It
// returns the row and the cell after it.
func (m *Model) buttons(ls lessonState, y int) (string, int) {
	s := m.cardStyles()
	type button struct {
		key, label string
		strong     bool
		do         func() tea.Cmd
	}
	list := []button{
		{key: "F2", label: "menu", do: func() tea.Cmd { m.openPause(); return nil }},
		{key: "F3", label: "show me", strong: ls.level >= 2, do: func() tea.Cmd { _, _ = m.lessons.Command("showMe"); return nil }},
		{key: "F4", label: "skip", do: func() tea.Cmd { _, _ = m.lessons.Command("skip"); return nil }},
	}
	if ls.explainer {
		list[1] = button{key: "enter", label: "go on", do: func() tea.Cmd { _, _ = m.lessons.Command("next"); return nil }}
	}
	var b strings.Builder
	x := 1
	b.WriteString(s.text.Render(" "))
	for i, bt := range list {
		if i > 0 {
			b.WriteString(s.text.Render("   "))
			x += 3
		}
		text := s.key.Render(bt.key) + s.dim.Render(" "+bt.label)
		if bt.strong {
			text = s.pal.Mark(text, overlay.RowState{Cursor: true, Focused: true})
			if s.pal.Depth != overlay.Depth16 {
				text = s.key.Render(bt.key) + s.text.Bold(true).Render(" "+bt.label)
			}
		}
		width := lipgloss.Width(text)
		m.hits = append(m.hits, hit{y: y, x0: x, x1: x + width, do: bt.do})
		b.WriteString(text)
		x += width
	}
	return b.String(), x
}

// coachText is the coach's line, while it is fresh, cut to room cells.
func (m *Model) coachText(s styles, room int) string {
	text := m.coach
	if text == "" || time.Now().After(m.coachUntil) {
		text = m.saveErr
		if text == "" {
			return ""
		}
		return s.warn.Render(ansi.Truncate(text, max(room, 0), "…"))
	}
	// The name goes first when there is room for the whole line with it.
	if ansi.StringWidth("Tilly: "+text) <= room {
		text = "Tilly: " + text
	}
	return s.title.Render(ansi.Truncate(text, max(room, 0), "…"))
}

// finishView is the screen after the last step.
func (m *Model) finishView() tea.View {
	s := m.styles()
	f := m.finished
	clean, hinted, skipped := 0, 0, 0
	for _, r := range f.results {
		switch r {
		case string(lessons.Clean):
			clean++
		case string(lessons.Hinted):
			hinted++
		case string(lessons.Skipped):
			skipped++
		}
	}
	tick := "✓"
	if m.ascii {
		tick = "[x]"
	}
	summary := tick + " " + plural(len(f.results), "step") + " in " + clock(f.seconds) + ": " + itoa(clean) + " on your own"
	if hinted > 0 {
		summary += ", " + itoa(hinted) + " with a hint"
	}
	if skipped > 0 {
		summary += ", " + itoa(skipped) + " skipped"
	}
	lines := []string{s.title.Render(f.track.Title + ": done"), "", summary}
	if f.line != "" {
		lines = append(lines, "", s.title.Render("Tilly: ")+s.text.Render(f.line))
	}
	lines = append(lines, "", "When you are ready, run "+s.key.Render("tuios")+" to start for real.")
	return m.full(lines, m.rows(), "enter choose    q leave")
}

// full centres lines on the screen, with rows as a menu under them and a key
// line at the foot.
func (m *Model) full(text []string, rows []row, keys string) tea.View {
	s := m.styles()
	// A line too wide for the window wraps by words, each part centred.
	var lines []string
	for _, l := range text {
		lines = append(lines, strings.Split(ansi.Wordwrap(l, min(max(m.w-2, 10), 72), ""), "\n")...)
	}
	labelW, detailW := 0, 0
	for _, r := range rows {
		labelW = max(labelW, ansi.StringWidth(r.label))
		detailW = max(detailW, ansi.StringWidth(r.detail))
	}
	rowW := labelW + 4
	if detailW > 0 {
		rowW += 3 + detailW
	}
	rowW = min(rowW, m.w-2)
	block := len(lines)
	if len(rows) > 0 {
		block += 1 + len(rows)
	}
	if keys != "" {
		block += 2
	}
	top := max((m.h-block)/2, 0)
	out := make([]string, 0, m.h)
	for range top {
		out = append(out, "")
	}
	centre := func(l string) string {
		left := max((m.w-lipgloss.Width(l))/2, 0)
		return strings.Repeat(" ", left) + ansi.Truncate(l, m.w-left, "…")
	}
	for _, l := range lines {
		out = append(out, centre(l))
	}
	if len(rows) > 0 {
		out = append(out, "")
		left := max((m.w-rowW)/2, 0)
		for i, r := range rows {
			st := overlay.RowState{Cursor: i == m.sel, Focused: true}
			mark := "  "
			if st.Cursor {
				mark = "> "
			}
			ground := s.pal.Ground(st, overlay.NoColor)
			if s.pal.Depth == overlay.Depth16 {
				ground = overlay.NoColor
			}
			ink := func(c color.Color) lipgloss.Style {
				return overlay.Style(ground).Foreground(overlay.Readable(c, ground))
			}
			text := ink(s.pal.AccentBright).Bold(true).Render(mark) + ink(s.pal.Fg).Bold(st.Cursor).Render(r.label)
			if r.detail != "" {
				pad := labelW - ansi.StringWidth(r.label) + 3
				text += overlay.Style(ground).Render(strings.Repeat(" ", pad)) + ink(s.pal.FgDim).Render(r.detail)
			}
			text = s.pal.Row(ansi.Truncate(text, rowW, "…"), rowW, st, overlay.NoColor)
			y := len(out)
			idx, do := i, r.do
			m.hits = append(m.hits, hit{y: y, x0: left, x1: left + rowW, do: func() tea.Cmd { m.sel = idx; return do() }})
			out = append(out, strings.Repeat(" ", left)+text)
		}
	}
	if keys != "" {
		out = append(out, "", centre(s.mute.Render(keys)))
	}
	for len(out) < m.h {
		out = append(out, "")
	}
	v := tea.NewView(strings.Join(out[:m.h], "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func itoa(n int) string { return strconv.Itoa(n) }

// plural is "1 step" or "9 steps".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(n) + " " + word + "s"
}

// clock is a time in seconds as m:ss.
func clock(seconds int) string {
	return itoa(seconds/60) + ":" + strconv.FormatInt(int64(seconds%60+100), 10)[1:]
}
