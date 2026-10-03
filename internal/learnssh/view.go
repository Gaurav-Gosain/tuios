package learnssh

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
)

// Colours. The program steps them down for a 256 or 16 colour terminal.
var (
	cAccent = lipgloss.Color("#b894ff")
	cOK     = lipgloss.Color("#7ee787")
	cWarn   = lipgloss.Color("#f2cc60")
	cBad    = lipgloss.Color("#ff7b72")
	cDim    = lipgloss.Color("#8b949e")
	cText   = lipgloss.Color("#e6edf3")
	cCapBg  = lipgloss.Color("#30363d")
	cDoneBg = lipgloss.Color("#2ea043")
	cBg     = lipgloss.Color("#161b22")

	sAccent = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sOK     = lipgloss.NewStyle().Foreground(cOK)
	sWarn   = lipgloss.NewStyle().Foreground(cWarn)
	sBad    = lipgloss.NewStyle().Foreground(cBad)
	sDim    = lipgloss.NewStyle().Foreground(cDim)
	sBold   = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sCap    = lipgloss.NewStyle().Foreground(cText).Background(cCapBg).Padding(0, 1)
	sCapOn  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(cDoneBg).Bold(true).Padding(0, 1)
	sButton = lipgloss.NewStyle().Foreground(cText).Background(cCapBg)
	sSel    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color("#6e40c9")).Bold(true)
	sCard   = lipgloss.NewStyle().Background(cBg)
)

func (t *tutor) View() tea.View {
	t.hits = t.hits[:0]
	if t.w < MinCols || t.h < MinRows {
		return t.full([]string{
			sAccent.Render("tuios learn"),
			"",
			fmt.Sprintf("Your terminal is %d by %d.", t.w, t.h),
			fmt.Sprintf("Make it at least %d by %d to start.", MinCols, MinRows),
			"",
			sDim.Render("Press q to leave."),
		}, nil)
	}
	switch t.scr {
	case scrLesson, scrChallenge:
		v := t.tour.View()
		lines := strings.Split(v.Content, "\n")
		th := t.tourHeight()
		for len(lines) < th {
			lines = append(lines, "")
		}
		v.Content = strings.Join(lines[:th], "\n") + "\n" + t.card()
		return v
	case scrWelcome:
		return t.welcome()
	case scrChapters:
		return t.chapters()
	case scrLessonDone:
		return t.lessonDone()
	case scrChallenges:
		return t.challengeList()
	case scrChallengeDone:
		return t.challengeDone()
	case scrBoard:
		return t.boardView()
	case scrWizard:
		return t.wizardView()
	}
	return t.full([]string{"", sAccent.Render(t.byeText), ""}, nil)
}

// menuRow is one choice on a full screen.
type menuRow struct {
	label string
	do    func() tea.Cmd
}

// full centres lines on the screen, with rows as a selectable menu under
// them.
func (t *tutor) full(lines []string, rows []menuRow) tea.View {
	block := append([]string(nil), lines...)
	first := len(block)
	for i, r := range rows {
		label := "  " + r.label + "  "
		if i == t.sel {
			label = sSel.Render("› " + r.label + "  ")
		}
		block = append(block, label)
	}
	// Text lines are centred one by one. The menu rows are centred as one
	// block, so they line up.
	width := 0
	for _, l := range block[first:] {
		width = max(width, lipgloss.Width(l))
	}
	width = min(width, t.w)
	top := max((t.h-len(block))/2, 0)
	out := make([]string, 0, t.h)
	for range top {
		out = append(out, "")
	}
	for i, l := range block {
		if len(out) >= t.h {
			break
		}
		left := max((t.w-width)/2, 0)
		if i < first {
			left = max((t.w-lipgloss.Width(l))/2, 0)
		}
		out = append(out, strings.Repeat(" ", left)+ansi.Truncate(l, t.w-left, "…"))
		if i >= first {
			r := rows[i-first]
			idx := i - first
			t.hits = append(t.hits, hit{y: len(out) - 1, x0: left, x1: left + width, do: func() tea.Cmd {
				t.sel = idx
				return r.do()
			}})
		}
	}
	v := tea.NewView(strings.Join(out, "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// onMenuKey moves and picks on the full screens.
func (t *tutor) onMenuKey(key string) tea.Cmd {
	rows := t.rows()
	switch key {
	case "up", "k", "shift+tab":
		if t.sel > 0 {
			t.sel--
		}
	case "down", "j", "tab":
		if t.sel < len(rows)-1 {
			t.sel++
		}
	case "enter", "space":
		if t.sel < len(rows) {
			return rows[t.sel].do()
		}
	case "esc", "backspace", "m":
		if t.scr == scrWelcome {
			return nil
		}
		t.toMenu()
	case "q", "ctrl+c":
		if t.scr == scrWelcome {
			return t.leave()
		}
		t.toMenu()
	case "p":
		if t.scr == scrChallengeDone {
			t.publish()
		}
	default:
		// Number keys pick a row.
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			if i := int(key[0] - '1'); i < len(rows) {
				t.sel = i
				return rows[i].do()
			}
		}
	}
	return nil
}

// rows is the menu of the current full screen, the same list the view
// draws.
func (t *tutor) rows() []menuRow {
	switch t.scr {
	case scrWelcome:
		return t.welcomeRows()
	case scrChapters:
		return t.chapterRows()
	case scrLessonDone:
		return t.lessonDoneRows()
	case scrChallenges:
		return t.challengeRows()
	case scrChallengeDone:
		return t.challengeDoneRows()
	case scrBoard, scrWizard:
		return t.backRows()
	}
	return nil
}

func (t *tutor) goScreen(s screen) func() tea.Cmd {
	return func() tea.Cmd {
		t.scr = s
		t.sel = 0
		return nil
	}
}

func (t *tutor) backRows() []menuRow {
	return []menuRow{
		{"Back to the menu", func() tea.Cmd { t.toMenu(); return nil }},
		{"Leave", t.leave},
	}
}

func (t *tutor) doneCount() int {
	n := 0
	for _, tr := range t.file.Tracks {
		if t.finished[tr.ID] {
			n++
		}
	}
	return n
}

func (t *tutor) welcomeRows() []menuRow {
	start := "Start the tour"
	next := t.nextTrack()
	if t.doneCount() > 0 && next >= 0 {
		start = "Go on with the tour: " + t.file.Tracks[next].Title
	}
	rows := []menuRow{}
	if next >= 0 {
		rows = append(rows, menuRow{start, func() tea.Cmd { return t.startTrack(next) }})
	}
	return append(rows,
		menuRow{"Pick a chapter", t.goScreen(scrChapters)},
		menuRow{"Timed challenges", t.goScreen(scrChallenges)},
		menuRow{"Leaderboard", t.goScreen(scrBoard)},
		menuRow{"Get tuios", t.goScreen(scrWizard)},
		menuRow{"Leave", t.leave},
	)
}

func (t *tutor) welcome() tea.View {
	var lines []string
	for i, l := range padBlock(logo) {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(logoColors[i])).Render(l))
	}
	name := t.init.User
	if name == "" {
		name = "there"
	}
	lines = append(lines, "",
		sBold.Render("Hi "+name+"!")+" Welcome to the tuios tour.",
		sDim.Render("tuios is a window manager for your terminal."),
		sDim.Render("Learn it here, one short chapter at a time. There is nothing to install."),
		"",
	)
	if n := t.doneCount(); n > 0 {
		lines = append(lines, progressBar(n, len(t.file.Tracks), 20)+fmt.Sprintf(" %d of %d chapters done", n, len(t.file.Tracks)), "")
	}
	rows := t.welcomeRows()
	lines2 := []string{"", sDim.Render("Up and down to move. Enter to pick. q to leave.")}
	v := t.full(lines, rows)
	return t.appendFooter(v, lines2)
}

// appendFooter adds lines under a full screen's block.
func (t *tutor) appendFooter(v tea.View, lines []string) tea.View {
	out := strings.Split(v.Content, "\n")
	for _, l := range lines {
		if len(out) >= t.h {
			break
		}
		left := max((t.w-lipgloss.Width(l))/2, 0)
		out = append(out, strings.Repeat(" ", left)+l)
	}
	v.Content = strings.Join(out, "\n")
	return v
}

func (t *tutor) chapterRows() []menuRow {
	var rows []menuRow
	for i, tr := range t.file.Tracks {
		mark := sDim.Render("○")
		if t.finished[tr.ID] {
			mark = sOK.Render("✓")
		} else if t.reached[tr.ID] > 0 {
			mark = sWarn.Render("◐")
		}
		label := fmt.Sprintf("%s %-16s %s", mark, tr.Title, sDim.Render(fmt.Sprintf("%s, %d min, %d steps", tr.Audience, tr.Minutes, len(tr.Steps))))
		idx := i
		rows = append(rows, menuRow{label, func() tea.Cmd { return t.startTrack(idx) }})
	}
	return rows
}

func (t *tutor) chapters() tea.View {
	lines := []string{
		sAccent.Render("Pick a chapter"),
		sDim.Render("Each chapter takes a few minutes. Jump to any of them."),
		"",
	}
	v := t.full(lines, t.chapterRows())
	return t.appendFooter(v, []string{"", sDim.Render("Enter starts a chapter. Esc goes back.")})
}

func (t *tutor) lessonDoneRows() []menuRow {
	var rows []menuRow
	if next := t.nextTrack(); next >= 0 {
		rows = append(rows, menuRow{"Next chapter: " + t.file.Tracks[next].Title, func() tea.Cmd { return t.startTrack(next) }})
	} else {
		rows = append(rows, menuRow{"See what you won", t.goScreen(scrWizard)})
	}
	return append(rows,
		menuRow{"Do this chapter again", func() tea.Cmd { return t.startTrack(t.track) }},
		menuRow{"Try a timed challenge", t.goScreen(scrChallenges)},
		menuRow{"Back to the menu", func() tea.Cmd { t.toMenu(); return nil }},
	)
}

func (t *tutor) lessonDone() tea.View {
	l := t.lesson
	clean, hinted, skipped := 0, 0, 0
	var learned []string
	for i, r := range l.Results {
		switch r {
		case lessons.Clean:
			clean++
		case lessons.Hinted:
			hinted++
		case lessons.Skipped:
			skipped++
		}
		if r != lessons.Skipped && i < len(l.Track.Steps) && l.Track.Steps[i].Learned != "" {
			learned = append(learned, l.Track.Steps[i].Learned)
		}
	}
	took := l.Finished.Sub(l.Started).Round(time.Second)
	lines := []string{
		sOK.Render("✓ Chapter done: " + l.Track.Title),
		"",
		fmt.Sprintf("%d steps in %s. %s clean, %s with a hint, %s skipped.", len(l.Results), took,
			sOK.Render(fmt.Sprint(clean)), sWarn.Render(fmt.Sprint(hinted)), sDim.Render(fmt.Sprint(skipped))),
		"",
	}
	if len(learned) > 0 {
		lines = append(lines, sDim.Render("You learned: ")+strings.Join(learned, ", "), "")
	}
	n := t.doneCount()
	lines = append(lines, progressBar(n, len(t.file.Tracks), 20)+fmt.Sprintf(" %d of %d chapters done", n, len(t.file.Tracks)), "")
	return t.full(lines, t.lessonDoneRows())
}

func (t *tutor) challengeRows() []menuRow {
	var rows []menuRow
	for i := range challenges {
		c := &challenges[i]
		best := sDim.Render("no time yet")
		if b := t.bests[c.ID]; b > 0 {
			best = "best " + fmtMs(b)
		}
		label := fmt.Sprintf("%-14s %s  %s", c.Title, sDim.Render("target "+fmtMs(c.Target.Milliseconds())), best)
		rows = append(rows, menuRow{label, func() tea.Cmd { return t.startChallenge(c) }})
	}
	return append(rows, menuRow{"Back to the menu", func() tea.Cmd { t.toMenu(); return nil }})
}

func (t *tutor) challengeList() tea.View {
	lines := []string{
		sAccent.Render("Timed challenges"),
		sDim.Render("Beat the target time. The clock starts at Go."),
		"",
	}
	if !t.init.Keyed {
		lines = append(lines, sDim.Render("Connect with an SSH key to keep your best times between visits."), "")
	}
	return t.full(lines, t.challengeRows())
}

func (t *tutor) challengeDoneRows() []menuRow {
	c := t.ch
	rows := []menuRow{{"Try again", func() tea.Cmd { return t.startChallenge(c) }}}
	if !t.published && t.init.Name != "" && t.init.Name != "anonymous" {
		rows = append(rows, menuRow{"Show my name (" + t.init.Name + ") on the board", func() tea.Cmd { t.publish(); return nil }})
	}
	return append(rows,
		menuRow{"Other challenges", t.goScreen(scrChallenges)},
		menuRow{"Leaderboard", t.goScreen(scrBoard)},
		menuRow{"Back to the menu", func() tea.Cmd { t.toMenu(); return nil }},
	)
}

func (t *tutor) publish() {
	if t.published || t.ch == nil {
		return
	}
	t.published = true
	t.sel = 0
	_ = t.ctl.Send(Msg{T: MsgPublish, ID: t.ch.ID})
}

func (t *tutor) challengeDone() tea.View {
	c := t.ch
	ms := t.chTime.Milliseconds()
	var lines []string
	if t.chTime <= c.Target {
		lines = append(lines, sOK.Render(fmt.Sprintf("✓ %s in %s. You beat the target of %s!", c.Title, fmtMs(ms), fmtMs(c.Target.Milliseconds()))))
	} else {
		lines = append(lines, sWarn.Render(fmt.Sprintf("%s in %s. The target is %s. Try again?", c.Title, fmtMs(ms), fmtMs(c.Target.Milliseconds()))))
	}
	lines = append(lines, "")
	if t.chNewBest {
		lines = append(lines, sAccent.Render("A new personal best!"))
	} else if b := t.bests[c.ID]; b > 0 {
		lines = append(lines, "Your best is "+fmtMs(b)+".")
	}
	if t.chRank > 0 {
		lines = append(lines, fmt.Sprintf("That is number %d on the board.", t.chRank))
	}
	if t.published {
		lines = append(lines, sDim.Render("Your name is on the board."))
	} else if t.init.Name != "" && t.init.Name != "anonymous" {
		lines = append(lines, sDim.Render("The board shows you as anonymous. Press p to show your name."))
	}
	lines = append(lines, "")
	return t.full(lines, t.challengeDoneRows())
}

func (t *tutor) boardView() tea.View {
	var table []string
	for _, c := range challenges {
		table = append(table, sBold.Render(c.Title)+sDim.Render("  target "+fmtMs(c.Target.Milliseconds())))
		rows := t.board[c.ID]
		if len(rows) == 0 {
			table = append(table, sDim.Render("   No times yet. Be the first."))
		}
		for i, e := range rows {
			if i >= 10 {
				break
			}
			table = append(table, fmt.Sprintf("  %2d. %-16s %8s", i+1, e.Name, fmtMs(e.Ms)))
		}
		table = append(table, "")
	}
	lines := append([]string{sAccent.Render("Leaderboard"), sDim.Render("The ten best times for each challenge."), ""}, padBlock(table)...)
	return t.full(lines, t.backRows())
}

func (t *tutor) wizardView() tea.View {
	var lines []string
	for i, l := range padBlock(wizard) {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(logoColors[i%len(logoColors)])).Render(l))
	}
	n, all := t.doneCount(), len(t.file.Tracks)
	name := t.init.User
	if name == "" {
		name = "friend"
	}
	lines = append(lines, "")
	if n == all {
		lines = append(lines, sAccent.Render("You are a tuios wizard, "+name+"!"), sDim.Render("You finished every chapter. Your terminal will never be the same."))
	} else {
		lines = append(lines, sAccent.Render(fmt.Sprintf("You finished %d of %d chapters.", n, all)), sDim.Render("Finish them all to become a tuios wizard."))
	}
	lines = append(lines, "", "Get tuios on your own machine:", "")
	var cmds []string
	for _, l := range installLines {
		cmds = append(cmds, sOK.Render("$ ")+l)
	}
	lines = append(lines, padBlock(cmds)...)
	lines = append(lines, "", sDim.Render("Docs: https://tuios.dev/docs    Code: https://github.com/Gaurav-Gosain/tuios"), "")
	return t.full(lines, t.backRows())
}

// card is the lesson or challenge panel under tuios, cardHeight rows.
func (t *tutor) card() string {
	w := t.w
	y0 := t.tourHeight()
	var l [cardHeight]string

	// Row 0: a rule with the buttons on the right.
	type button struct {
		label string
		do    func() tea.Cmd
	}
	var buttons []button
	if t.scr == scrLesson {
		buttons = []button{
			{"F2 menu", func() tea.Cmd { t.toMenu(); return nil }},
			{"F3 show me", func() tea.Cmd { t.showMe(); return nil }},
			{"F4 skip", func() tea.Cmd { return t.skipStep(time.Now()) }},
		}
	} else {
		buttons = []button{{"F2 give up", func() tea.Cmd {
			t.scr = scrChallenges
			t.tickGen++
			t.setupGen++
			return nil
		}}}
	}
	btnText := ""
	x := w
	for i := len(buttons) - 1; i >= 0; i-- {
		b := buttons[i]
		cell := " " + b.label + " "
		x -= lipgloss.Width(cell) + 1
		t.hits = append(t.hits, hit{y: y0, x0: x, x1: x + lipgloss.Width(cell), do: b.do})
		btnText = sButton.Render(cell) + " " + btnText
	}
	title := sAccent.Render(" tuios learn ")
	ruleW := w - lipgloss.Width(title) - lipgloss.Width(btnText) - 1
	l[0] = sDim.Render("─") + title + sDim.Render(strings.Repeat("─", max(ruleW, 0))) + btnText

	now := time.Now()
	if t.scr == scrChallenge {
		t.challengeCard(&l, now)
	} else {
		t.lessonCard(&l, now)
	}
	if t.notice != "" && now.Before(t.noticeTill) {
		l[4] = sWarn.Render(" ! " + t.notice)
	}
	out := make([]string, cardHeight)
	for i, s := range l {
		s = ansi.Truncate(s, w, "…")
		if pad := w - lipgloss.Width(s); pad > 0 {
			s += strings.Repeat(" ", pad)
		}
		out[i] = s
	}
	return strings.Join(out, "\n")
}

func (t *tutor) lessonCard(l *[cardHeight]string, now time.Time) {
	les := t.lesson
	if les == nil {
		return
	}
	tr := les.Track
	steps := len(tr.Steps)
	at := min(les.Index, steps)
	var marks strings.Builder
	for i := range steps {
		switch {
		case i < len(les.Results) && les.Results[i] == lessons.Clean:
			marks.WriteString(sOK.Render("✓"))
		case i < len(les.Results) && les.Results[i] == lessons.Hinted:
			marks.WriteString(sWarn.Render("✓"))
		case i < len(les.Results):
			marks.WriteString(sDim.Render("-"))
		case i == at:
			marks.WriteString(sAccent.Render("›"))
		default:
			marks.WriteString(sDim.Render("·"))
		}
	}
	l[1] = fmt.Sprintf(" Chapter %d of %d: %s   %s %d/%d  %s", t.track+1, len(t.file.Tracks), sBold.Render(tr.Title),
		progressBar(at, steps, 12), at, steps, marks.String())

	step := les.Step()
	if step == nil {
		l[2] = " " + sOK.Render("✓ "+t.flash)
		return
	}
	if step.Explainer != nil {
		l[2] = " " + sAccent.Render("› "+step.Title)
		body := wrap(step.Explainer.Body, t.w-3)
		if len(body) > 0 {
			l[3] = "   " + body[0]
		}
		more := "Press enter to go on."
		if len(body) > 1 {
			l[4] = "   " + body[1]
			l[1] += "   " + sWarn.Render(more)
		} else {
			l[4] = "   " + sWarn.Render(more) + sDim.Render("  More: tuios.dev"+step.Explainer.Href)
		}
		if t.settingUp {
			l[4] = " " + sDim.Render("Getting things ready…")
		}
		return
	}
	title := " " + sAccent.Render("› "+step.Title)
	if step.Note != "" {
		title += sDim.Render("  " + step.Note)
	}
	l[2] = title

	var caps []string
	for i, k := range step.Keys {
		label := lessons.Label(k)
		if i < les.Pressed {
			caps = append(caps, sCapOn.Render(label))
		} else {
			caps = append(caps, sCap.Render(label))
		}
	}
	l[3] = "   " + strings.Join(caps, " ")
	if les.WrongMode {
		if step.Needs == "window" {
			l[3] += sWarn.Render("   You are typing into the shell. Press ctrl+b, then esc, for window mode.")
		} else {
			l[3] += sWarn.Render("   You are in window mode. Press i to type into the shell.")
		}
	}

	switch {
	case t.settingUp && !(t.flash != "" && now.Before(t.flashUntil)):
		l[4] = " " + sDim.Render("Getting things ready…")
	case t.flash != "" && now.Before(t.flashUntil):
		l[4] = " " + sOK.Render("✓ "+t.flash)
	case les.HintLevel(now) == 2:
		l[4] = " " + sWarn.Render("Hint: "+step.Hint) + sDim.Render("  Stuck? F3 shows you.")
	case les.HintLevel(now) == 1:
		l[4] = " " + sWarn.Render("Hint: "+step.Hint)
	default:
		l[4] = " " + sDim.Render("Press the keys above. The tour checks each step for you.")
	}
}

func (t *tutor) challengeCard(l *[cardHeight]string, now time.Time) {
	c := t.ch
	if c == nil {
		return
	}
	best := ""
	if b := t.bests[c.ID]; b > 0 {
		best = "   Your best " + fmtMs(b)
	}
	l[1] = fmt.Sprintf(" Challenge: %s   Target %s%s", sBold.Render(c.Title), fmtMs(c.Target.Milliseconds()), best)
	l[2] = " " + sAccent.Render("› "+c.Goal)
	switch t.chPhase {
	case phaseSetup:
		l[3] = "   " + sDim.Render("Setting the scene…")
	case phaseCountdown:
		left := int(t.chGo.Sub(now).Seconds()) + 1
		l[3] = "   " + sWarn.Render(fmt.Sprintf("Ready… %d", max(left, 1)))
	case phaseRunning:
		el := now.Sub(t.chStart)
		st := sOK
		if el > c.Target {
			st = sBad
		}
		l[3] = "   " + st.Bold(true).Render("Go!  "+fmtMs(el.Milliseconds()))
		if el > 10*time.Second {
			l[4] = " " + sWarn.Render("Hint: "+c.Hint)
		}
	case phaseFinished:
		l[3] = "   " + sOK.Bold(true).Render("Done in "+fmtMs(t.chTime.Milliseconds())+"!")
	}
}

// progressBar is n of total as a bar width cells wide.
func progressBar(n, total, width int) string {
	if total <= 0 {
		return ""
	}
	filled := min(n*width/total, width)
	return sOK.Render(strings.Repeat("█", filled)) + sDim.Render(strings.Repeat("░", width-filled))
}

// fmtMs is "12.3 s".
func fmtMs(ms int64) string { return fmt.Sprintf("%.1f s", float64(ms)/1000) }

// wrap breaks s into lines of at most width cells.
func wrap(s string, width int) []string {
	if width < 10 {
		return []string{s}
	}
	return strings.Split(lipgloss.NewStyle().Width(width).Render(s), "\n")
}

// padBlock pads lines to one width, so a centred block keeps its left edge.
func padBlock(lines []string) []string {
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l + strings.Repeat(" ", w-lipgloss.Width(l))
	}
	return out
}
