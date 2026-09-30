package learnssh

import (
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/learn"
	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
)

// screen is what the tutor shows. The lesson and challenge screens show
// tuios with the card under it. The others are the tutor's own full screens,
// with tuios running out of sight.
type screen int

const (
	scrWelcome screen = iota
	scrChapters
	scrLesson
	scrLessonDone
	scrChallenges
	scrChallenge
	scrChallengeDone
	scrBoard
	scrWizard
	scrBye
)

type phase int

const (
	phaseSetup phase = iota
	phaseCountdown
	phaseRunning
	phaseFinished
)

// Messages the tutor sends itself.
type (
	drainMsg    struct{}
	openMenuMsg struct{}
	quitNowMsg  struct{}
	ctlMsg      Msg
	byeMsg      struct{ text string }
	tickMsg     struct{ gen int }
	setupMsg    struct {
		gen  int
		list []lessons.Setup
		at   int
		then func() tea.Cmd
	}
)

// tutor wraps the tour: it owns the screen, routes keys and mouse to tuios
// or to its own menus, and checks lesson steps against the tour's events.
type tutor struct {
	tour   *learn.Model
	send   func(tea.Msg)
	queue  *eventQueue
	inject func([]byte)
	ctl    *ctlWriter
	file   *lessons.File
	init   Msg

	w, h     int
	scr      screen
	sel      int
	hits     []hit
	quitting bool
	byeText  string
	started  time.Time

	// Lessons.
	track      int
	lesson     *lessons.Lesson
	settingUp  bool
	setupGen   int
	tickGen    int
	finished   map[string]bool
	reached    map[string]int
	flash      string
	flashUntil time.Time
	notice     string
	noticeTill time.Time
	doneAt     time.Time // when to show the chapter-done screen

	// Challenges.
	ch        *challenge
	chPhase   phase
	chGo      time.Time
	chStart   time.Time
	chMem     map[string]int
	chState   map[string]any
	chTime    time.Duration
	chRank    int
	chNewBest bool
	published bool
	bests     map[string]int64
	board     Board
}

// hit is a clickable place on screen.
type hit struct {
	y, x0, x1 int
	do        func() tea.Cmd
}

func newTutor(file *lessons.File, init Msg, ctl *ctlWriter, inject func([]byte)) *tutor {
	bests := init.Bests
	if bests == nil {
		bests = map[string]int64{}
	}
	return &tutor{
		file: file, init: init, ctl: ctl, inject: inject,
		queue:    &eventQueue{},
		finished: map[string]bool{},
		reached:  map[string]int{},
		bests:    bests,
		board:    init.Board,
		started:  time.Now(),
	}
}

// eventQueue holds the tour's events until the tutor's next Update. Events
// come from Update itself and from the fake shell's goroutines, and the
// lesson engine must see them in order on the program's goroutine.
type eventQueue struct {
	mu      sync.Mutex
	ev      []learn.Event
	pending bool
	nudge   func()
}

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

func (t *tutor) Init() tea.Cmd { return t.tour.Init() }

func (t *tutor) tourHeight() int { return max(t.h-cardHeight, 1) }

func (t *tutor) showsTour() bool { return t.scr == scrLesson || t.scr == scrChallenge }

func (t *tutor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	now := time.Now()
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		t.w, t.h = clampSize(m.Width, m.Height)
		cmds = append(cmds, t.forward(tea.WindowSizeMsg{Width: t.w, Height: t.tourHeight()}))
	case ctlMsg:
		cmds = append(cmds, t.onCtl(Msg(m)))
	case byeMsg:
		return t, t.bye(m.text)
	case quitNowMsg:
		t.quitting = true
		return t, tea.Quit
	case openMenuMsg:
		t.toMenu()
	case tickMsg:
		if m.gen == t.tickGen {
			cmds = append(cmds, t.onTick(now))
		}
	case setupMsg:
		if m.gen == t.setupGen {
			cmds = append(cmds, t.runSetup(m))
		}
	case drainMsg:
	case tea.KeyPressMsg:
		if handled, cmd := t.onKey(m, now); handled {
			cmds = append(cmds, cmd)
		} else {
			cmds = append(cmds, t.forward(m))
		}
	case tea.MouseMsg:
		mouse := m.Mouse()
		if t.showsTour() && mouse.Y < t.tourHeight() {
			cmds = append(cmds, t.forward(m))
		} else if _, click := m.(tea.MouseClickMsg); click {
			cmds = append(cmds, t.onClick(mouse.X, mouse.Y))
		}
	default:
		cmds = append(cmds, t.forward(msg))
	}
	cmds = append(cmds, t.drain(now))
	return t, tea.Batch(cmds...)
}

// forward runs msg through tuios.
func (t *tutor) forward(msg tea.Msg) tea.Cmd {
	_, cmd := t.tour.Update(msg)
	return cmd
}

// command runs a learn command on tuios, from inside Update.
func (t *tutor) command(name string, args ...string) tea.Cmd {
	return t.forward(learn.CommandMsg{Name: name, Args: args})
}

// runSetup runs setup entries in order, pausing where one asks to wait.
// Events that arrive meanwhile update the state but complete no step.
func (t *tutor) runSetup(m setupMsg) tea.Cmd {
	if m.at < 0 {
		t.settingUp = false
		if m.then != nil {
			return m.then()
		}
		return nil
	}
	var cmds []tea.Cmd
	for i := m.at; i < len(m.list); i++ {
		s := m.list[i]
		switch {
		case s.Command != "":
			cmds = append(cmds, t.command(s.Command, s.Args...))
		case s.Input != "":
			t.inject([]byte(s.Input))
		}
		if s.Wait > 0 && i+1 < len(m.list) {
			next := m
			next.at = i + 1
			cmds = append(cmds, tea.Tick(time.Duration(s.Wait)*time.Millisecond, func(time.Time) tea.Msg { return next }))
			return tea.Batch(cmds...)
		}
	}
	// Let the last command's events arrive before steps count again.
	finish := m
	finish.at = -1
	cmds = append(cmds, tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return finish }))
	return tea.Batch(cmds...)
}

func (t *tutor) setup(list []lessons.Setup, then func() tea.Cmd) tea.Cmd {
	t.setupGen++
	t.settingUp = true
	gen := t.setupGen
	return func() tea.Msg { return setupMsg{gen: gen, list: list, then: then} }
}

// tick keeps the card's clock and hints fresh while a lesson or challenge
// is on screen.
func (t *tutor) tick(every time.Duration) tea.Cmd {
	t.tickGen++
	gen := t.tickGen
	return tea.Tick(every, func(time.Time) tea.Msg { return tickMsg{gen: gen} })
}

func (t *tutor) onTick(now time.Time) tea.Cmd {
	switch t.scr {
	case scrLesson:
		if !t.doneAt.IsZero() && now.After(t.doneAt) {
			t.doneAt = time.Time{}
			t.scr = scrLessonDone
			t.sel = 0
			return nil
		}
		return t.tick(time.Second)
	case scrChallenge:
		if t.chPhase == phaseCountdown && !now.Before(t.chGo) {
			t.chPhase = phaseRunning
			t.chStart = now
		}
		if t.chPhase == phaseFinished && !t.doneAt.IsZero() && now.After(t.doneAt) {
			t.doneAt = time.Time{}
			t.scr = scrChallengeDone
			t.sel = 0
			return nil
		}
		return t.tick(100 * time.Millisecond)
	}
	return nil
}

// drain feeds the tour's queued events to the lesson or the challenge.
func (t *tutor) drain(now time.Time) tea.Cmd {
	var cmds []tea.Cmd
	for _, e := range t.queue.take() {
		ev := lessons.Event{Type: e.Type, Data: e.Data, State: e.State}
		if ev.Data == nil {
			ev.Data = map[string]any{}
		}
		if e.Type == learn.EventAction {
			switch e.Data["name"] {
			case "quit", "prefix_quit", "kill_session_quit":
				if t.showsTour() {
					t.toMenu()
					continue
				}
			}
		}
		switch t.scr {
		case scrLesson:
			cmds = append(cmds, t.feedLesson(ev, now))
		case scrChallenge:
			cmds = append(cmds, t.feedChallenge(ev, now))
		}
	}
	return tea.Batch(cmds...)
}

func (t *tutor) feedLesson(ev lessons.Event, now time.Time) tea.Cmd {
	l := t.lesson
	if l == nil || l.Done() {
		return nil
	}
	if t.settingUp {
		if ev.State != nil {
			l.Ctx.State = ev.State
		}
		return nil
	}
	step := l.Step()
	if !l.Feed(ev, now) {
		return nil
	}
	return t.stepDone(step, now)
}

// stepDone gives the instant feedback, and starts the next step or ends the
// chapter.
func (t *tutor) stepDone(step *lessons.Step, now time.Time) tea.Cmd {
	l := t.lesson
	tr := l.Track
	t.reached[tr.ID] = max(t.reached[tr.ID], l.Index)
	name := step.Learned
	if name == "" {
		name = step.Title
	}
	t.flash = pick(praise, l.Index) + " You learned: " + name + "."
	t.flashUntil = now.Add(2500 * time.Millisecond)
	if l.Done() {
		first := !t.finished[tr.ID]
		t.finished[tr.ID] = true
		if first {
			_ = t.ctl.Send(Msg{T: MsgTrackDone, ID: tr.ID})
		}
		t.flash = "Chapter done! Well played."
		t.doneAt = now.Add(2500 * time.Millisecond)
		return t.command("celebrate", "big")
	}
	if next := l.Step(); next != nil && len(next.Setup) > 0 {
		return t.setup(next.Setup, nil)
	}
	return nil
}

var praise = []string{"Nice!", "Great!", "Neat!", "Smooth!", "Yes!", "Sharp!", "Lovely!"}

func pick(list []string, i int) string { return list[i%len(list)] }

func (t *tutor) feedChallenge(ev lessons.Event, now time.Time) tea.Cmd {
	if ev.State != nil {
		t.chState = ev.State
	}
	if t.ch == nil || t.chPhase != phaseRunning || t.chState == nil {
		return nil
	}
	if !t.ch.done(t.chState, ev, t.chMem) {
		return nil
	}
	t.chPhase = phaseFinished
	t.chTime = now.Sub(t.chStart)
	ms := t.chTime.Milliseconds()
	t.chNewBest = t.bests[t.ch.ID] == 0 || ms < t.bests[t.ch.ID]
	if t.chNewBest {
		t.bests[t.ch.ID] = ms
	}
	t.chRank = 0
	t.published = false
	_ = t.ctl.Send(Msg{T: MsgChallenge, ID: t.ch.ID, Ms: ms})
	t.doneAt = now.Add(1800 * time.Millisecond)
	return t.command("celebrate", "big")
}

func (t *tutor) onCtl(m Msg) tea.Cmd {
	now := time.Now()
	switch m.T {
	case MsgNotice:
		t.notice = m.Text
		t.noticeTill = now.Add(10 * time.Second)
		if !t.showsTour() {
			return nil
		}
		return t.command("notify", m.Text, "warning")
	case MsgBye:
		return t.bye(m.Text)
	case MsgResult:
		if m.Board != nil {
			t.board = m.Board
		}
		if t.ch != nil && m.ID == t.ch.ID {
			t.chRank = m.Rank
			if m.Best > 0 {
				t.bests[m.ID] = m.Best
			}
		}
	case MsgBoard:
		if m.Board != nil {
			t.board = m.Board
		}
	}
	return nil
}

// bye shows why the session ends, then ends it.
func (t *tutor) bye(text string) tea.Cmd {
	t.scr = scrBye
	t.byeText = text
	return tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return quitNowMsg{} })
}

func (t *tutor) toMenu() {
	t.tickGen++
	t.setupGen++
	t.settingUp = false
	t.doneAt = time.Time{}
	t.scr = scrWelcome
	t.sel = 0
}

// startTrack begins chapter i from a clean tuios.
func (t *tutor) startTrack(i int) tea.Cmd {
	if i < 0 || i >= len(t.file.Tracks) {
		return nil
	}
	now := time.Now()
	tr := &t.file.Tracks[i]
	t.track = i
	t.lesson = lessons.Start(tr, now)
	t.scr = scrLesson
	t.flash, t.doneAt = "", time.Time{}
	list := append([]lessons.Setup{{Command: "reset", Wait: 100}}, tr.Setup...)
	if len(tr.Steps) > 0 {
		list = append(list, tr.Steps[0].Setup...)
	}
	return tea.Batch(t.setup(list, func() tea.Cmd {
		t.lesson.ActiveAt, t.lesson.StepAt = time.Now(), time.Now()
		return nil
	}), t.tick(time.Second))
}

// startChallenge sets the scene, then counts down from three.
func (t *tutor) startChallenge(c *challenge) tea.Cmd {
	t.ch = c
	t.scr = scrChallenge
	t.chPhase = phaseSetup
	t.chMem = map[string]int{}
	t.chState = nil
	t.doneAt = time.Time{}
	return tea.Batch(t.setup(c.Setup, func() tea.Cmd {
		t.chPhase = phaseCountdown
		t.chGo = time.Now().Add(3 * time.Second)
		return nil
	}), t.tick(100*time.Millisecond))
}

// nextTrack is the first chapter not yet finished, in tour order.
func (t *tutor) nextTrack() int {
	for i, tr := range t.file.Tracks {
		if !t.finished[tr.ID] {
			return i
		}
	}
	return -1
}

// skipStep moves past the current step without doing it.
func (t *tutor) skipStep(now time.Time) tea.Cmd {
	l := t.lesson
	if l == nil || l.Done() || t.settingUp {
		return nil
	}
	step := l.Step()
	l.Advance(lessons.Skipped, now)
	cmd := t.stepDone(step, now)
	t.flash = "Skipped. You can come back to it from the menu."
	if l.Done() {
		t.flash = "Chapter done!"
	}
	return cmd
}

// showMe types the rest of the step's keys for the reader.
func (t *tutor) showMe() {
	l := t.lesson
	if l == nil || l.Done() || t.settingUp {
		return
	}
	step := l.Step()
	if step.Explainer != nil {
		return
	}
	l.HintShown = true
	if step.ShowMe != "" {
		t.inject([]byte(step.ShowMe))
		return
	}
	var b strings.Builder
	for _, k := range step.Keys[min(l.Pressed, len(step.Keys)):] {
		b.WriteString(lessons.KeyBytes(k))
	}
	t.inject([]byte(b.String()))
}

// onKey handles the tutor's own keys. It reports false for a key tuios
// should get.
func (t *tutor) onKey(k tea.KeyPressMsg, now time.Time) (bool, tea.Cmd) {
	key := k.String()
	if t.w < MinCols || t.h < MinRows {
		if key == "ctrl+c" || key == "q" {
			return true, t.leave()
		}
		return true, nil
	}
	switch t.scr {
	case scrLesson:
		switch key {
		case "f2", "f3", "f4", "enter":
		default:
			if t.settingUp {
				// The scene is still being set, in a mode the step may not
				// want yet. The card says so.
				return true, nil
			}
		}
		switch key {
		case "f2":
			t.toMenu()
			return true, nil
		case "f3":
			t.showMe()
			return true, nil
		case "f4":
			return true, t.skipStep(now)
		case "enter":
			if l := t.lesson; l != nil && !l.Done() {
				if step := l.Step(); step.Explainer != nil {
					if t.settingUp {
						// Too early: the scene is still being set.
						return true, nil
					}
					l.Advance(lessons.Clean, now)
					return true, t.stepDone(step, now)
				}
			}
		}
		return false, nil
	case scrChallenge:
		if key == "f2" {
			t.scr = scrChallenges
			t.tickGen++
			t.setupGen++
			return true, nil
		}
		// Keys wait for "Go".
		if t.chPhase != phaseRunning {
			return true, nil
		}
		return false, nil
	case scrBye:
		return true, nil
	}
	return true, t.onMenuKey(key)
}

// leave ends the session at the reader's request.
func (t *tutor) leave() tea.Cmd {
	_ = t.ctl.Send(Msg{T: MsgQuit})
	return t.bye("See you soon. Come back any time: ssh learn.tuios.dev")
}

func (t *tutor) onClick(x, y int) tea.Cmd {
	for _, h := range t.hits {
		if y == h.y && x >= h.x0 && x < h.x1 {
			return h.do()
		}
	}
	return nil
}
