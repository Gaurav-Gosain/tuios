package learn

import (
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
)

// Lesson events: what the lesson engine tells the page. See
// cmd/tuios-wasm/README.md for each one's data.
const (
	EventLessonStart  = "lesson.start"
	EventLessonStep   = "lesson.step"
	EventLessonKeys   = "lesson.keys"
	EventLessonHint   = "lesson.hint"
	EventLessonPress  = "lesson.press"
	EventLessonDone   = "lesson.done"
	EventLessonFinish = "lesson.finish"
)

// LessonCommands are the names Lessons.Command takes.
var LessonCommands = []string{"start", "showMe", "skip", "next", "back", "stop", "log"}

// The lesson's pacing.
const (
	// doneHold is the least time a finished step's card stays up.
	doneHold = 700 * time.Millisecond
	// settle is how long after a step starts its events still belong to the
	// step before, unless the person presses a key first.
	settle = 150 * time.Millisecond
	// untilLimit is the longest a setup waits for its event.
	untilLimit = 3 * time.Second
	// setupGap is the pause after a setup entry with no event to wait for.
	setupGap = 120 * time.Millisecond
	// skipLimit is how long skip lets "show me" try before moving on.
	skipLimit = 2500 * time.Millisecond
	hintTick  = 250 * time.Millisecond
	// readyLimit is the longest show me waits for a step's After events.
	readyLimit = time.Minute
)

// LessonHooks connect a Lessons to the program it teaches.
type LessonHooks struct {
	// Emit sends a lesson.* event to the page.
	Emit func(Event)
	// Send delivers a message to the program and returns once the
	// program's loop took it, so two calls arrive in order.
	Send func(tea.Msg)
	// Input types bytes as if the person typed them.
	Input func(string)
	// State is the app's current snapshot.
	State func() map[string]any
}

// Lessons runs one lesson at a time on top of a Model: it sets each step's
// scene, checks the person's progress with the lessons engine, plays "show
// me", and reports it all as lesson.* events. The page draws those and
// keeps no step logic of its own.
type Lessons struct {
	surface string
	file    *lessons.File
	hooks   LessonHooks

	mu sync.Mutex
	// gen ends the goroutines of an earlier lesson: each one stops once gen
	// moved on.
	gen    int
	phase  string // "", "booting", "running", "finished"
	lesson *lessons.Lesson
	// shown is the step whose card is up. It lags lesson.Index while the
	// next step's scene is set.
	shown       int
	settingUp   bool
	settleUntil time.Time
	skipping    bool
	catching    bool
	playing     bool
	lastHint    string
	waiter      *untilWait
	logging     bool
	log         []any
}

type untilWait struct {
	rule *lessons.Rule
	ctx  lessons.Ctx
	done chan struct{}
}

// NewLessons loads the lessons for a surface, "web" or "terminal".
func NewLessons(surface string, hooks LessonHooks) (*Lessons, error) {
	f, err := lessons.Load()
	if err != nil {
		return nil, err
	}
	return &Lessons{surface: surface, file: f, hooks: hooks}, nil
}

// Observe takes every event the Model emits. It is safe from any goroutine.
func (r *Lessons) Observe(e Event) {
	if strings.HasPrefix(e.Type, "lesson.") {
		return
	}
	le := lessons.Event{Type: e.Type, Data: e.Data, State: e.State}
	now := time.Now()
	var out []Event
	r.mu.Lock()
	if w := r.waiter; w != nil && w.rule.Match(le, &w.ctx) {
		close(w.done)
		r.waiter = nil
	}
	l := r.lesson
	if r.phase != "running" || l == nil || l.Done() {
		r.mu.Unlock()
		return
	}
	// Every event counts toward a step's After events, also a setup's.
	if l.Observe(le, now) && r.shown == l.Index {
		out = append(out, r.hintEvent(now, true)...)
	}
	if e.Type == EventKey && !r.settingUp {
		r.settleUntil = time.Time{}
	}
	// A setup's events are the scene's, and events right after a step are
	// the step's that just finished.
	if r.settingUp || now.Before(r.settleUntil) {
		r.mu.Unlock()
		r.emit(out...)
		return
	}
	if r.logging {
		r.log = append(r.log, map[string]any{"type": le.Type, "data": le.Data, "state": le.State, "t": now.UnixMilli()})
	}
	before := l.Index
	pressed, wrong, wrongMode := l.Pressed, l.Wrong, l.WrongMode
	ctx := l.Ctx
	if l.Feed(le, now) {
		out = append(out, r.stepDone(before, &le, ctx.LastAction, now)...)
	} else if e.Type == EventKey && (l.Pressed != pressed || l.Wrong != wrong || l.WrongMode != wrongMode) {
		out = append(out, r.keysEvent())
		out = append(out, r.hintEvent(now, false)...)
	}
	r.mu.Unlock()
	r.emit(out...)
}

// Command runs a lesson command from the page: start TRACK [FROM], showMe,
// skip, next, back, stop, or log on|off|get. log get returns the events the
// engine was fed since logging began.
func (r *Lessons) Command(name string, args ...string) (any, error) {
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch name {
	case "start":
		from := 0
		if s := arg(1); s != "" {
			if _, err := fmt.Sscan(s, &from); err != nil {
				return nil, fmt.Errorf("start: from %q is not a number", s)
			}
		}
		return nil, r.start(arg(0), from)
	case "showMe":
		r.showMe()
	case "skip":
		r.skip()
	case "next":
		r.next()
	case "back":
		r.back()
	case "stop":
		r.mu.Lock()
		r.gen++
		r.phase, r.lesson = "", nil
		r.mu.Unlock()
	case "log":
		r.mu.Lock()
		defer r.mu.Unlock()
		switch arg(0) {
		case "on":
			r.logging, r.log = true, nil
		case "off":
			r.logging, r.log = false, nil
		default:
			return append([]any(nil), r.log...), nil
		}
	default:
		return nil, fmt.Errorf("unknown lesson command %q", name)
	}
	return nil, nil
}

// State is the lesson as the page draws it.
func (r *Lessons) State() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]any{"phase": r.phase}
	l := r.lesson
	if l == nil {
		return out
	}
	now := time.Now()
	out["track"] = l.Track.ID
	out["index"] = l.Index
	out["shown"] = r.shown
	out["total"] = len(l.Track.Steps)
	out["pressed"] = l.Pressed
	out["wrong"] = l.Wrong
	out["wrongMode"] = l.WrongMode
	out["hinted"] = l.Hinted
	out["results"] = results(l.Results)
	out["seconds"] = int(l.Elapsed(now) / time.Second)
	maps.Copy(out, r.hintData(now))
	return out
}

func (r *Lessons) start(id string, from int) error {
	t := r.file.Find(id)
	if t == nil {
		return fmt.Errorf("no track %q", id)
	}
	if from < 0 || from > len(t.Steps) {
		return fmt.Errorf("start: step %d is outside the track", from)
	}
	r.mu.Lock()
	r.gen++
	g := r.gen
	r.phase, r.lesson, r.shown = "booting", nil, 0
	r.skipping, r.catching, r.playing, r.settingUp = false, false, false, false
	r.lastHint, r.log = "", nil
	r.mu.Unlock()
	go func() {
		// A clean screen: no windows, the shipped keys, the rail closed.
		if !r.runSetup(g, []lessons.Setup{{Command: "reset"}}) || !r.runSetup(g, t.Setup) {
			return
		}
		r.mu.Lock()
		if r.gen != g {
			r.mu.Unlock()
			return
		}
		r.lesson = lessons.Start(t, time.Now())
		r.phase = "running"
		r.catching = from > 0
		r.mu.Unlock()
		r.emit(Event{Type: EventLessonStart, Data: map[string]any{
			"track": t.ID, "title": t.Title, "total": len(t.Steps), "from": from,
		}})
		go r.tick(g)
		if from > 0 {
			r.catchUp(g, from)
			return
		}
		r.startStep(g, 0, time.Time{})
	}()
	return nil
}

func (r *Lessons) alive(g int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen == g
}

// runSetup runs setup entries in order, each after the one before has had
// its effect. It reports false once the lesson was replaced.
func (r *Lessons) runSetup(g int, list []lessons.Setup) bool {
	for _, s := range list {
		r.mu.Lock()
		if r.gen != g {
			r.mu.Unlock()
			return false
		}
		var w *untilWait
		if s.Until != nil {
			w = &untilWait{rule: s.Until, ctx: lessons.NewCtx(nil), done: make(chan struct{})}
			r.waiter = w
		}
		r.mu.Unlock()
		if s.Command != "" {
			r.hooks.Send(CommandMsg{Name: s.Command, Args: s.Args})
		} else {
			r.hooks.Input(s.Input)
		}
		switch {
		case w != nil:
			select {
			case <-w.done:
			case <-time.After(untilLimit):
				r.mu.Lock()
				if r.waiter == w {
					r.waiter = nil
				}
				r.mu.Unlock()
			}
			if s.Wait > 0 {
				time.Sleep(time.Duration(s.Wait) * time.Millisecond)
			}
		case s.Wait > 0:
			time.Sleep(time.Duration(s.Wait) * time.Millisecond)
		default:
			time.Sleep(setupGap)
		}
	}
	return r.alive(g)
}

// startStep sets the scene of step i and shows its card, no sooner than
// doneHold after doneAt, when the step before finished.
func (r *Lessons) startStep(g, i int, doneAt time.Time) {
	r.mu.Lock()
	l := r.lesson
	if r.gen != g || l == nil || l.Index != i || l.Step() == nil {
		r.mu.Unlock()
		return
	}
	step := l.Step()
	r.settleUntil = time.Now().Add(settle)
	r.settingUp = len(step.Setup) > 0
	r.mu.Unlock()
	if !r.runSetup(g, step.Setup) {
		return
	}
	if wait := doneHold - time.Since(doneAt); !doneAt.IsZero() && wait > 0 {
		time.Sleep(wait)
	}
	r.mu.Lock()
	if r.gen != g || r.lesson.Index != i {
		r.settingUp = false
		r.mu.Unlock()
		return
	}
	now := time.Now()
	r.settingUp = false
	r.settleUntil = now.Add(settle)
	r.lesson.Restart(now)
	r.shown = i
	r.lastHint = ""
	out := []Event{r.stepEvent()}
	out = append(out, r.hintEvent(now, true)...)
	r.mu.Unlock()
	r.emit(out...)
}

// stepDone reports a finished step and moves the lesson on. The caller
// holds r.mu, and has advanced the lesson past index.
func (r *Lessons) stepDone(index int, ev *lessons.Event, lastAction string, now time.Time) []Event {
	l := r.lesson
	if r.skipping {
		r.skipping = false
		l.Results[index] = lessons.Skipped
	}
	r.settleUntil = now.Add(settle)
	step := l.Track.Steps[index]
	data := map[string]any{
		"index": index, "id": step.ID, "result": string(l.Results[index]),
		"results": results(l.Results), "finished": l.Done(), "lastAction": lastAction,
		"learned": step.Learned, "explainer": step.Explainer != nil,
	}
	if ev != nil {
		data["event"] = map[string]any{"type": ev.Type, "data": orEmpty(ev.Data)}
	}
	out := []Event{{Type: EventLessonDone, Data: data}}
	if l.Done() {
		r.phase = "finished"
		out = append(out, Event{Type: EventLessonFinish, Data: map[string]any{
			"track": l.Track.ID, "results": results(l.Results), "seconds": int(l.Elapsed(now) / time.Second),
		}})
		return out
	}
	if !r.catching {
		g, next := r.gen, l.Index
		go r.startStep(g, next, now)
	}
	return out
}

// next ends an explainer step: the person read it.
func (r *Lessons) next() {
	r.mu.Lock()
	l := r.lesson
	if r.phase != "running" || l == nil || l.Step() == nil || l.Step().Explainer == nil || r.shown != l.Index {
		r.mu.Unlock()
		return
	}
	index := l.Index
	r.logMark("lesson.next")
	l.Advance(lessons.Clean, time.Now())
	out := r.stepDone(index, nil, "", time.Now())
	r.mu.Unlock()
	r.emit(out...)
}

func (r *Lessons) back() {
	r.mu.Lock()
	l := r.lesson
	if r.phase == "" || l == nil || r.catching || !l.Back(time.Now()) {
		r.mu.Unlock()
		return
	}
	r.phase = "running"
	g, i := r.gen, l.Index
	r.mu.Unlock()
	go r.startStep(g, i, time.Time{})
}

func (r *Lessons) showMe() {
	r.mu.Lock()
	l := r.lesson
	if r.phase != "running" || l == nil || l.Step() == nil || l.Step().Explainer != nil {
		r.mu.Unlock()
		return
	}
	l.MarkHinted()
	r.logMark("lesson.showMe")
	g := r.gen
	out := r.hintEvent(time.Now(), false)
	r.mu.Unlock()
	r.emit(out...)
	go r.play(g, false)
}

func (r *Lessons) skip() {
	r.mu.Lock()
	l := r.lesson
	if r.phase != "running" || l == nil || l.Step() == nil {
		r.mu.Unlock()
		return
	}
	index := l.Index
	if l.Step().Explainer != nil {
		r.logMark("lesson.skip")
		l.Advance(lessons.Skipped, time.Now())
		out := r.stepDone(index, nil, "", time.Now())
		r.mu.Unlock()
		r.emit(out...)
		return
	}
	r.skipping = true
	g := r.gen
	r.mu.Unlock()
	go r.play(g, true)
	go func() {
		time.Sleep(skipLimit)
		r.mu.Lock()
		if r.gen != g || r.lesson.Index != index || r.lesson.Done() {
			r.mu.Unlock()
			return
		}
		// Playing the keys did not finish it: move on anyway.
		r.skipping = false
		now := time.Now()
		r.logMark("lesson.skip")
		r.lesson.Advance(lessons.Skipped, now)
		out := r.stepDone(index, nil, "", now)
		r.mu.Unlock()
		r.emit(out...)
	}()
}

// play types the step's keys for the person, from the first one not yet
// pressed.
func (r *Lessons) play(g int, fast bool) {
	r.mu.Lock()
	l := r.lesson
	if r.gen != g || r.playing || l == nil || l.Step() == nil {
		r.mu.Unlock()
		return
	}
	r.playing = true
	index := l.Index
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.playing = false
		r.mu.Unlock()
	}()
	// A step that waits on an event, such as an agent asking, is played
	// once it came: the keys before it would do nothing.
	for end := time.Now().Add(readyLimit); ; time.Sleep(100 * time.Millisecond) {
		r.mu.Lock()
		gone := r.gen != g || r.lesson.Index != index
		ready := r.lesson.Ready()
		r.mu.Unlock()
		if gone {
			return
		}
		if ready || time.Now().After(end) {
			break
		}
	}
	r.mu.Lock()
	pressed := r.lesson.Pressed
	step := *r.lesson.Step()
	r.mu.Unlock()
	pace := func(slow, quick time.Duration) {
		if fast {
			time.Sleep(quick)
		} else {
			time.Sleep(slow)
		}
	}
	state := r.hooks.State()
	if lessons.LacksWindow(&step, state) {
		r.hooks.Send(CommandMsg{Name: "newWindow"})
		pace(400*time.Millisecond, 150*time.Millisecond)
		state = r.hooks.State()
	}
	if step.Needs != "" && state["mode"] != step.Needs {
		r.hooks.Send(CommandMsg{Name: "mode", Args: []string{step.Needs}})
		pace(300*time.Millisecond, 80*time.Millisecond)
	}
	// Once the step is done, the keys left over belong to no step.
	moved := func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.gen != g || r.lesson.Index != index
	}
	if step.ShowMe != "" {
		for _, ch := range step.ShowMe {
			if moved() {
				return
			}
			r.hooks.Input(string(ch))
			pace(55*time.Millisecond, 12*time.Millisecond)
		}
		return
	}
	for _, k := range step.Keys[min(pressed, len(step.Keys)):] {
		if moved() {
			return
		}
		if k.Text != "" {
			for _, ch := range k.Text {
				r.hooks.Input(string(ch))
				pace(55*time.Millisecond, 12*time.Millisecond)
			}
		} else {
			r.emit(Event{Type: EventLessonPress, Data: map[string]any{"chord": k.Chord, "down": true}})
			r.hooks.Input(lessons.KeyBytes(k))
			pace(380*time.Millisecond, 90*time.Millisecond)
			r.emit(Event{Type: EventLessonPress, Data: map[string]any{"chord": k.Chord, "down": false}})
		}
		pace(260*time.Millisecond, 60*time.Millisecond)
	}
}

// catchUp plays the steps done on an earlier visit at speed, so the scene
// is the one the next step expects.
func (r *Lessons) catchUp(g, n int) {
	for i := range n {
		r.startStep(g, i, time.Time{})
		time.Sleep(250 * time.Millisecond)
		if !r.alive(g) {
			return
		}
		r.mu.Lock()
		explainer := r.lesson.Step() != nil && r.lesson.Step().Explainer != nil
		r.mu.Unlock()
		if !explainer {
			r.play(g, true)
			for end := time.Now().Add(untilLimit); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
				r.mu.Lock()
				done := r.lesson.Index != i
				r.mu.Unlock()
				if done {
					break
				}
			}
		}
		r.mu.Lock()
		if r.gen != g {
			r.mu.Unlock()
			return
		}
		var out []Event
		if r.lesson.Index == i {
			r.lesson.Advance(lessons.Clean, time.Now())
			out = r.stepDone(i, nil, "", time.Now())
		}
		r.mu.Unlock()
		r.emit(out...)
	}
	r.mu.Lock()
	r.catching = false
	done := r.lesson.Done()
	r.mu.Unlock()
	if !done {
		r.startStep(g, n, time.Time{})
	}
}

// tick sends lesson.hint as the hint ladder moves with time.
func (r *Lessons) tick(g int) {
	for {
		time.Sleep(hintTick)
		r.mu.Lock()
		if r.gen != g {
			r.mu.Unlock()
			return
		}
		out := r.hintEvent(time.Now(), false)
		r.mu.Unlock()
		r.emit(out...)
	}
}

// hintData is where the person stands on the hint ladder. The caller holds
// r.mu.
func (r *Lessons) hintData(now time.Time) map[string]any {
	l := r.lesson
	step := l.Step()
	out := map[string]any{"level": 0, "altBlocked": false, "noWindow": false, "nudge": "", "waiting": false}
	if r.phase != "running" || step == nil || step.Explainer != nil || r.shown != l.Index || r.settingUp {
		return out
	}
	if !l.Ready() {
		// The step waits on something to happen first: the card says
		// what, and there is nothing to hint yet.
		out["waiting"] = true
		return out
	}
	level := l.HintLevel(now)
	alt := l.AltChordBlocked(now)
	noWindow := lessons.LacksWindow(step, l.Ctx.State)
	out["level"], out["altBlocked"], out["noWindow"] = level, alt, noWindow
	out["nudge"] = lessons.Nudge(level, l.Wrong, l.WrongMode, step.Needs, noWindow, alt)
	return out
}

// hintEvent is a lesson.hint event when the ladder moved since the last
// one, or always when force is set. The caller holds r.mu.
func (r *Lessons) hintEvent(now time.Time, force bool) []Event {
	if r.lesson == nil {
		return nil
	}
	d := r.hintData(now)
	key := fmt.Sprint(d["level"], d["altBlocked"], d["noWindow"], d["nudge"], d["waiting"], r.lesson.Index)
	if key == r.lastHint && !force {
		return nil
	}
	r.lastHint = key
	d["index"] = r.lesson.Index
	return []Event{{Type: EventLessonHint, Data: d}}
}

// keysEvent is the step's keycaps after a key. The caller holds r.mu.
func (r *Lessons) keysEvent() Event {
	l := r.lesson
	return Event{Type: EventLessonKeys, Data: map[string]any{
		"index": l.Index, "pressed": l.Pressed, "wrong": l.Wrong, "wrongMode": l.WrongMode,
	}}
}

// stepEvent is the current step's card, in this surface's words. The caller
// holds r.mu.
func (r *Lessons) stepEvent() Event {
	l := r.lesson
	s := l.Step().For(r.surface)
	keys := make([]any, len(s.Keys))
	for i, k := range s.Keys {
		if k.Text != "" {
			keys[i] = map[string]any{"text": k.Text}
		} else {
			keys[i] = k.Chord
		}
	}
	data := map[string]any{
		"track": l.Track.ID, "index": l.Index, "total": len(l.Track.Steps),
		"id": s.ID, "title": s.Title, "note": s.Note, "keys": keys, "needs": s.Needs,
		"hint": s.Hint, "waiting": s.Waiting, "learned": s.Learned, "try": s.Try,
	}
	if e := s.Explainer; e != nil {
		data["explainer"] = map[string]any{"art": e.Art, "body": e.Body, "href": e.Href, "linkText": e.LinkText}
	}
	return Event{Type: EventLessonStep, Data: data}
}

// logMark notes in the log a lesson command that changed the step, so a
// replay of the log can do the same. The caller holds r.mu.
func (r *Lessons) logMark(kind string) {
	if r.logging {
		r.log = append(r.log, map[string]any{"type": kind, "t": time.Now().UnixMilli()})
	}
}

func (r *Lessons) emit(events ...Event) {
	for _, e := range events {
		r.hooks.Emit(e)
	}
}

func results(rs []lessons.Result) []any {
	out := make([]any, len(rs))
	for i, r := range rs {
		out[i] = string(r)
	}
	return out
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
