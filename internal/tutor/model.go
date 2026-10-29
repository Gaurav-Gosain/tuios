package tutor

import (
	"reflect"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/learn"
	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
)

// screen is what the tutor shows. The lesson screen is tuios with the card
// under it. The others are the tutor's own, with tuios out of sight.
type screen int

const (
	scrNested screen = iota // inside a tuios pane: how to give the tutor every key
	scrMenu                 // start, resume, leave
	scrPause                // the menu F2 and the quit keys open during a lesson
	scrLesson
	scrFinish
)

// Messages the tutor sends itself.
type (
	// drainMsg says events wait in the queue.
	drainMsg struct{}
	// openMenuMsg is a quit from inside tuios: it opens the menu.
	openMenuMsg struct{}
	// leaveMsg ends the tutor, from a signal.
	leaveMsg struct{}
	// finishMsg shows the finish screen, a moment after the last step.
	finishMsg struct{ gen int }
)

// finishDelay is how long the last step's card stays up before the finish
// screen.
const finishDelay = 1200 * time.Millisecond

// coachFor is how long the coach's line stays on the card.
const coachFor = 4 * time.Second

// Model is the tutor's Bubble Tea model. It owns the screen, routes keys and
// the mouse to tuios or to its own buttons, and shows the lesson that
// learn.Lessons runs.
type Model struct {
	tour    *learn.Model
	lessons *learn.Lessons
	file    *lessons.File
	tracks  []*lessons.Track
	store   *progressStore
	queue   *eventQueue
	ascii   bool

	w, h int
	scr  screen
	sel  int
	hits []hit

	// leaving lets the quit through the program's filter.
	leaving atomic.Bool

	// The lesson on screen.
	track      *lessons.Track
	doneBefore map[string]bool // steps done on an earlier run, ticked on the bar
	coach      string
	coachUntil time.Time
	finishGen  int
	finished   *finishInfo
	saveErr    string
}

// finishInfo is what the finish screen shows.
type finishInfo struct {
	track   *lessons.Track
	results []string
	seconds int
	line    string
}

// hit is a place on screen a click runs.
type hit struct {
	y, x0, x1 int
	do        func() tea.Cmd
}

// eventQueue holds the lesson's events until the next Update. They come from
// inside Update and from the lesson's goroutines, and the model reads them
// on the program's goroutine only.
type eventQueue struct {
	mu      sync.Mutex
	ev      []learn.Event
	pending bool
	nudge   func()
}

// maxQueued bounds the queue. Only a stuck program reaches it.
const maxQueued = 4096

func (q *eventQueue) push(e learn.Event) {
	q.mu.Lock()
	if len(q.ev) < maxQueued {
		q.ev = append(q.ev, e)
	}
	wake := !q.pending && q.nudge != nil
	q.pending = true
	q.mu.Unlock()
	if wake {
		q.nudge()
	}
}

func (q *eventQueue) take() []learn.Event {
	q.mu.Lock()
	defer q.mu.Unlock()
	ev := q.ev
	q.ev = nil
	q.pending = false
	return ev
}

// Init starts tuios.
func (m *Model) Init() tea.Cmd { return m.tour.Init() }

// Card sizes. At 80x24 and up the card has 4 rows, from 60x16 a 2-row
// strip, and below that the tutor asks for a bigger window.
const (
	fullCols, fullRows   = 80, 24
	stripCols, stripRows = 60, 16
)

// tooSmall reports whether the window is below the smallest size the tutor
// draws.
func (m *Model) tooSmall() bool { return m.w < stripCols || m.h < stripRows }

// cardRows is the height of the card at the current size.
func (m *Model) cardRows() int {
	if m.w >= fullCols && m.h >= fullRows {
		return 4
	}
	return 2
}

// tourHeight is the rows tuios gets.
func (m *Model) tourHeight() int { return max(m.h-m.cardRows(), 1) }

// Update routes a message to the tutor or to tuios.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	own := true
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		if !m.tooSmall() {
			// tuios keeps the size it had while the window is too small, so
			// nothing in the lesson reflows.
			cmds = append(cmds, m.forward(tea.WindowSizeMsg{Width: m.w, Height: m.tourHeight()}))
		}
	case leaveMsg:
		return m, m.leave()
	case openMenuMsg:
		m.openPause()
	case finishMsg:
		if msg.gen == m.finishGen && m.scr == scrLesson {
			m.scr, m.sel = scrFinish, 0
		}
	case drainMsg:
	case tea.KeyPressMsg:
		if handled, cmd := m.onKey(msg); handled {
			cmds = append(cmds, cmd)
		} else {
			own = false
			cmds = append(cmds, m.forward(msg))
		}
	case tea.MouseMsg:
		mouse := msg.Mouse()
		switch {
		case m.scr == scrLesson && !m.tooSmall() && mouse.Y < m.tourHeight():
			own = false
			cmds = append(cmds, m.forward(msg))
		default:
			if click, ok := msg.(tea.MouseClickMsg); ok && click.Button == tea.MouseLeft {
				cmds = append(cmds, m.onClick(mouse.X, mouse.Y))
			}
		}
	default:
		own = false
		cmds = append(cmds, m.forward(msg))
	}
	cmds = append(cmds, m.drain())
	if own {
		// The card or a screen of the tutor's own changed, and tuios writes
		// no frame of its own for that.
		cmds = append(cmds, m.tour.OS.RequestFlush())
	}
	return m, tea.Batch(cmds...)
}

// forward runs msg through tuios.
func (m *Model) forward(msg tea.Msg) tea.Cmd {
	_, cmd := m.tour.Update(msg)
	return cmd
}

// onKey handles the keys of the tutor itself. It reports false for a key
// that belongs to tuios.
func (m *Model) onKey(k tea.KeyPressMsg) (bool, tea.Cmd) {
	key := k.String()
	if m.tooSmall() {
		if key == "q" || key == "ctrl+c" {
			return true, m.leave()
		}
		return true, nil
	}
	switch m.scr {
	case scrNested:
		switch key {
		case "enter", "space":
			m.scr, m.sel = scrMenu, 0
		case "q", "ctrl+c", "esc":
			return true, m.leave()
		}
		return true, nil
	case scrMenu, scrPause, scrFinish:
		return true, m.onMenuKey(key)
	}
	// The lesson.
	switch key {
	case "f2":
		m.openPause()
		return true, nil
	case "f3":
		_, _ = m.lessons.Command("showMe")
		return true, nil
	case "f4":
		_, _ = m.lessons.Command("skip")
		return true, nil
	case "enter":
		if s := m.lessonStep(); s != nil && s.Explainer != nil {
			_, _ = m.lessons.Command("next")
			return true, nil
		}
	}
	return false, nil
}

// onMenuKey moves and picks on the tutor's own screens.
func (m *Model) onMenuKey(key string) tea.Cmd {
	rows := m.rows()
	switch key {
	case "up", "k", "shift+tab":
		if m.sel > 0 {
			m.sel--
		}
	case "down", "j", "tab":
		if m.sel < len(rows)-1 {
			m.sel++
		}
	case "enter", "space":
		if m.sel < len(rows) {
			return rows[m.sel].do()
		}
	case "esc", "f2":
		if m.scr == scrPause {
			m.scr = scrLesson
		}
	case "q", "ctrl+c":
		return m.leave()
	}
	return nil
}

// onClick runs the button under the pointer.
func (m *Model) onClick(x, y int) tea.Cmd {
	for _, h := range m.hits {
		if y == h.y && x >= h.x0 && x < h.x1 {
			return h.do()
		}
	}
	return nil
}

// row is one choice on a tutor screen.
type row struct {
	label string
	// detail is drawn after the label, quieter.
	detail string
	do     func() tea.Cmd
}

// rows are the choices of the screen on show.
func (m *Model) rows() []row {
	switch m.scr {
	case scrPause:
		return []row{
			{label: "Continue", do: func() tea.Cmd { m.scr = scrLesson; return nil }},
			{label: "Back one step", do: func() tea.Cmd {
				m.scr = scrLesson
				_, _ = m.lessons.Command("back")
				return nil
			}},
			{label: "Restart this track", do: func() tea.Cmd { return m.start(m.track) }},
			{label: "Choose a track", do: func() tea.Cmd { m.scr, m.sel = scrMenu, 0; return nil }},
			{label: "Leave", do: m.leave},
		}
	case scrFinish:
		var out []row
		for _, id := range m.finished.track.Next {
			if t := m.find(id); t != nil {
				out = append(out, row{label: "Next: " + t.Title, do: func() tea.Cmd { return m.start(t) }})
			}
		}
		return append(out,
			row{label: "Choose a track", do: func() tea.Cmd { m.scr, m.sel = scrMenu, 0; return nil }},
			row{label: "Leave", do: m.leave},
		)
	}
	var out []row
	for _, t := range m.tracks {
		if m.inProgress(t) {
			done := m.store.doneCount(t.ID)
			out = append(out, row{label: "Continue " + midSentence(t.Title), detail: plural(done, "step") + " of " + itoa(len(t.Steps)) + " done", do: func() tea.Cmd { return m.start(t) }})
		}
	}
	for _, t := range m.tracks {
		detail := plural(len(t.Steps), "step") + "   " + itoa(t.Minutes) + " min"
		if t.Audience != "" {
			detail += "   " + t.Audience
		}
		if m.trackFinished(t) {
			detail += "   done"
		}
		label := t.Title
		if m.inProgress(t) {
			// The row above continues the track, so this one says it
			// starts it again.
			label = "Start " + midSentence(t.Title) + " over"
		}
		out = append(out, row{label: label, detail: detail, do: func() tea.Cmd { return m.start(t) }})
	}
	return append(out, row{label: "Leave", do: m.leave})
}

// inProgress reports whether t has steps done on an earlier run and is not
// finished: the menu offers to continue it.
func (m *Model) inProgress(t *lessons.Track) bool {
	done := m.store.doneCount(t.ID)
	return done > 0 && done < len(t.Steps) && !m.trackFinished(t)
}

// midSentence is a track's title after a verb: sentence case, so "The
// basics" reads "the basics".
func midSentence(title string) string {
	r, n := utf8.DecodeRuneInString(title)
	if r == utf8.RuneError {
		return title
	}
	return string(unicode.ToLower(r)) + title[n:]
}

func (m *Model) trackFinished(t *lessons.Track) bool {
	p := m.store.track(t.ID)
	return p != nil && p.Finished
}

func (m *Model) find(id string) *lessons.Track {
	for _, t := range m.tracks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// openPause opens the lesson's menu. Outside a lesson it does nothing: the
// tutor's own screens are menus already.
func (m *Model) openPause() {
	if m.scr == scrLesson {
		m.scr, m.sel = scrPause, 0
	}
}

// start runs track t from its first step. Steps done on an earlier run are
// ticked on the bar: v1 resumes at the start of a track, since the scene
// the later steps expect is built by the earlier ones.
func (m *Model) start(t *lessons.Track) tea.Cmd {
	m.track = t
	m.doneBefore = map[string]bool{}
	if p := m.store.track(t.ID); p != nil {
		for _, id := range p.Done {
			m.doneBefore[id] = true
		}
	}
	m.coach, m.finished = "", nil
	m.finishGen++
	m.scr, m.sel = scrLesson, 0
	if _, err := m.lessons.Command("start", t.ID); err != nil {
		m.saveErr = err.Error()
	}
	return nil
}

// leave ends the tutor.
func (m *Model) leave() tea.Cmd {
	m.leaving.Store(true)
	_, _ = m.lessons.Command("stop")
	return tea.Quit
}

// drain takes the lesson's events.
func (m *Model) drain() tea.Cmd {
	var cmds []tea.Cmd
	for _, e := range m.queue.take() {
		switch e.Type {
		case learn.EventAction:
			switch e.Data["name"] {
			case "quit", "prefix_quit", "kill_session_quit":
				m.openPause()
			}
		case learn.EventLessonDone:
			cmds = append(cmds, m.onStepDone(e.Data))
		case learn.EventLessonFinish:
			cmds = append(cmds, m.onFinish(e.Data))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) onStepDone(d map[string]any) tea.Cmd {
	if m.track == nil {
		return nil
	}
	id, _ := d["id"].(string)
	result, _ := d["result"].(string)
	action, _ := d["lastAction"].(string)
	var typ string
	data := map[string]any{}
	if ev, ok := d["event"].(map[string]any); ok {
		typ, _ = ev["type"].(string)
		if dd, ok := ev["data"].(map[string]any); ok {
			data = dd
		}
	}
	if result != string(lessons.Skipped) && id != "" {
		if err := m.store.markStep(m.track.ID, id); err != nil {
			m.saveErr = "Progress not saved: " + err.Error()
		}
	}
	if line := coachLine(m.file.Coach, result, typ, data, action); line != "" {
		m.coach, m.coachUntil = line, time.Now().Add(coachFor)
		// A frame once the line is due to go.
		return tea.Tick(coachFor+50*time.Millisecond, func(time.Time) tea.Msg { return drainMsg{} })
	}
	return nil
}

func (m *Model) onFinish(d map[string]any) tea.Cmd {
	if m.track == nil {
		return nil
	}
	seconds := 0
	switch v := d["seconds"].(type) {
	case int:
		seconds = v
	case float64:
		seconds = int(v)
	}
	var results []string
	if rs, ok := d["results"].([]any); ok {
		for _, r := range rs {
			s, _ := r.(string)
			results = append(results, s)
		}
	}
	if err := m.store.markFinished(m.track.ID, seconds); err != nil {
		m.saveErr = "Progress not saved: " + err.Error()
	}
	m.finished = &finishInfo{track: m.track, results: results, seconds: seconds, line: finishLine(m.file.Coach, m.track)}
	gen := m.finishGen
	return tea.Tick(finishDelay, func(time.Time) tea.Msg { return finishMsg{gen: gen} })
}

// lessonStep is the step whose card is up, in the terminal's words, or nil.
func (m *Model) lessonStep() *lessons.Step {
	st := m.lessons.State()
	shown, ok := st["shown"].(int)
	if !ok || m.track == nil || shown < 0 || shown >= len(m.track.Steps) || st["phase"] == "" {
		return nil
	}
	s := m.track.Steps[shown].For(lessons.SurfaceTerminal)
	return &s
}

// filter is the program's event filter. A quit from inside tuios opens the
// menu, a clipboard write or read never reaches the terminal, and the rest
// goes through tuios's own filters.
func filter(model tea.Model, msg tea.Msg) tea.Msg {
	m, ok := model.(*Model)
	if !ok || m.leaving.Load() {
		return msg
	}
	switch msg.(type) {
	case tea.QuitMsg, tea.InterruptMsg, tea.SuspendMsg:
		return openMenuMsg{}
	}
	if isClipboardMsg(msg) {
		// A yank stays in tuios's own buffer. The copy.yank event the lesson
		// waits on comes from the command before it gets here.
		return nil
	}
	msg = app.FilterLearnMode(m.tour.OS, msg)
	return app.FilterMouseMotion(m.tour.OS, msg)
}

// isClipboardMsg reports whether msg asks Bubble Tea to write or read the
// terminal's clipboard. Bubble Tea keeps those message types to itself, so
// they are known by name.
func isClipboardMsg(msg tea.Msg) bool {
	t := reflect.TypeOf(msg)
	if t == nil || t.PkgPath() != reflect.TypeFor[tea.BatchMsg]().PkgPath() {
		return false
	}
	switch t.Name() {
	case "setClipboardMsg", "setPrimaryClipboardMsg", "readClipboardMsg", "readPrimaryClipboardMsg":
		return true
	}
	return false
}
