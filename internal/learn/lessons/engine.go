package lessons

import "time"

// The hint ladder: time without progress before the hint shows and before
// "show me" is offered, and the wrong keys that show the hint early.
const (
	HintAfter      = 8 * time.Second
	ShowMeAfter    = 20 * time.Second
	HintAfterWrong = 2
	// An alt chord that never arrives: time, or other keys, before the
	// lesson says something outside tuios may take it.
	AltBlockedAfter     = 8 * time.Second
	AltBlockedAfterKeys = 3
)

// Result is how a step went.
type Result string

const (
	Clean   Result = "clean"
	Hinted  Result = "hinted"
	Skipped Result = "skipped"
)

// Lesson is a person's place in one track: the current step, the keys of it
// pressed so far, whether they look stuck, and how each step went. Feed it
// every event tuios reports.
type Lesson struct {
	Track *Track
	Index int
	// Pressed is how many of the step's keys were pressed in order.
	Pressed int
	Wrong   int
	Hinted  bool
	// WrongMode is set when the last key was a step key in the other mode.
	WrongMode bool
	// KeysSeen counts the keys tuios read this step, and AltSeen says
	// whether one was an alt chord.
	KeysSeen int
	AltSeen  bool
	Results  []Result
	Ctx      Ctx
	// happened marks, per step, which of its After events came, counted
	// from the start of the lesson.
	happened [][]bool

	Started, StepStarted time.Time
	// Active is the last time the person made progress, for the hint timer.
	Active   time.Time
	Finished time.Time
}

// Start begins a track.
func Start(t *Track, now time.Time) *Lesson {
	l := &Lesson{Track: t, Ctx: NewCtx(nil), Started: now, StepStarted: now, Active: now}
	l.happened = make([][]bool, len(t.Steps))
	for i, s := range t.Steps {
		l.happened[i] = make([]bool, len(s.After))
	}
	return l
}

// Observe takes every event of the lesson, also the ones Feed never sees,
// such as a setup's or one between steps, and marks the After events of
// the steps still to come. It reports whether the current step just became
// ready.
func (l *Lesson) Observe(e Event, now time.Time) bool {
	before := l.Ready()
	for i := max(l.Index, 0); i < len(l.Track.Steps); i++ {
		for j, r := range l.Track.Steps[i].After {
			if !l.happened[i][j] && r.Match(e, &Ctx{Mem: map[string]int{}}) {
				l.happened[i][j] = true
			}
		}
	}
	if !before && l.Ready() {
		// The hint ladder starts once there is something to do.
		l.Active = now
		return true
	}
	return false
}

// Ready reports whether every After event of the current step came.
func (l *Lesson) Ready() bool {
	if l.Index < 0 || l.Index >= len(l.happened) {
		return true
	}
	for _, ok := range l.happened[l.Index] {
		if !ok {
			return false
		}
	}
	return true
}

// Step is the current step, or nil once the track is done.
func (l *Lesson) Step() *Step {
	if l.Index < 0 || l.Index >= len(l.Track.Steps) {
		return nil
	}
	return &l.Track.Steps[l.Index]
}

// Done reports whether every step is finished.
func (l *Lesson) Done() bool { return !l.Finished.IsZero() }

// Advance moves to the next step, recording how this one went.
func (l *Lesson) Advance(r Result, now time.Time) {
	l.Results = append(l.Results, r)
	l.Index++
	l.resetStep(now)
	if l.Index >= len(l.Track.Steps) {
		l.Finished = now
	}
}

// Back returns to the step before this one, forgetting how it went.
func (l *Lesson) Back(now time.Time) bool {
	if l.Index == 0 {
		return false
	}
	l.Index--
	l.Results = l.Results[:l.Index]
	l.Finished = time.Time{}
	l.resetStep(now)
	return true
}

// Restart resets the timers of the current step, as it starts.
func (l *Lesson) Restart(now time.Time) { l.StepStarted, l.Active = now, now }

func (l *Lesson) resetStep(now time.Time) {
	l.Pressed, l.Wrong, l.Hinted, l.WrongMode = 0, 0, false, false
	l.KeysSeen, l.AltSeen = 0, false
	l.Ctx = NewCtx(l.Ctx.State)
	l.StepStarted, l.Active = now, now
}

// Feed takes one event and reports whether it finished the current step.
func (l *Lesson) Feed(e Event, now time.Time) bool {
	step := l.Step()
	if step == nil || l.Done() {
		return false
	}
	if e.State != nil {
		l.Ctx.State = e.State
	}
	switch e.Type {
	case "key":
		key := str(e.Data["key"])
		if IsModifierKey(key) {
			// A modifier on its own: the chord it starts comes next.
			break
		}
		// The action event, if the key has one, comes right after it.
		l.Ctx.LastAction = ""
		l.trackKey(step, key, str(e.Data["mode"]), now)
		l.KeysSeen++
		l.AltSeen = l.AltSeen || IsAltChord(key)
	case "action":
		l.Ctx.LastAction = str(e.Data["name"])
	}
	if step.Explainer == nil && step.Done != nil && step.Done.Match(e, &l.Ctx) {
		r := Clean
		if l.Hinted {
			r = Hinted
		}
		l.Advance(r, now)
		return true
	}
	return false
}

// trackKey lights the step's keycaps as they are pressed in order, and
// counts keys that do something else as wrong. Typed text is checked as a
// whole when the shell reports the command, so it only lights the caps up
// to it.
func (l *Lesson) trackKey(step *Step, key, mode string, now time.Time) {
	// A step key pressed in the other mode went to the wrong place: in
	// typing mode, "n" is a letter for the shell.
	isStepKey := false
	for _, k := range step.Keys {
		if k.Text == "" && SameChord(key, k.Chord) {
			isStepKey = true
			break
		}
	}
	if step.Needs != "" && mode != "" && mode != step.Needs && isStepKey {
		l.Pressed, l.WrongMode, l.Hinted = 0, true, true
		l.Wrong++
		return
	}
	if l.Pressed < len(step.Keys) {
		want := step.Keys[l.Pressed]
		if want.Text == "" && SameChord(key, want.Chord) {
			l.Pressed++
			l.Active, l.WrongMode = now, false
			return
		}
		if want.Text != "" {
			// Typing: every key is fine while the person types the text,
			// and the key after the text ends it, so the caps after it
			// light in order too.
			if l.Pressed+1 < len(step.Keys) {
				after := step.Keys[l.Pressed+1]
				if after.Text == "" && SameChord(key, after.Chord) {
					l.Pressed += 2
				}
			}
			l.Active, l.WrongMode = now, false
			return
		}
	}
	if n := RestartAt(step.Keys, l.Pressed, key); n > 0 {
		// Starting over counts as progress.
		l.Pressed = n
		l.Active, l.WrongMode = now, false
		return
	}
	l.Pressed, l.WrongMode = 0, false
	l.Wrong++
	if l.Wrong >= HintAfterWrong {
		l.Hinted = true
	}
}

// RestartAt is where the sequence stands after a key that broke it: the
// longest run of the step's first keys that ends with this key, or 0. With
// the keys "j j enter", the presses "j j j" leave two caps lit, not one.
// Typed text never matches, so a run stops at it.
func RestartAt(keys []Key, pressed int, key string) int {
	for n := min(pressed, len(keys)-1); n >= 0; n-- {
		ok := true
		for i := 0; i <= n && ok; i++ {
			want := keys[i]
			got := key
			if i != n {
				g := keys[pressed-n+i]
				if g.Text != "" {
					ok = false
					break
				}
				got = g.Chord
			}
			ok = want.Text == "" && SameChord(got, want.Chord)
		}
		if ok {
			return n + 1
		}
	}
	return 0
}

// HintLevel is 0 for no hint, 1 for the hint, 2 for the hint and "show me".
func (l *Lesson) HintLevel(now time.Time) int {
	if !l.Ready() {
		return 0
	}
	idle := now.Sub(l.Active)
	switch {
	case idle >= ShowMeAfter:
		return 2
	case l.Hinted || idle >= HintAfter || l.WrongMode:
		return 1
	}
	return 0
}

// AltChordBlocked reports whether the step waits on an alt chord that never
// arrives: the next key is an alt chord, tuios read none this step, and the
// person waited or pressed other keys. A window manager can bind alt and a
// letter system wide, and then tuios never sees the key.
func (l *Lesson) AltChordBlocked(now time.Time) bool {
	step := l.Step()
	if step == nil || step.Explainer != nil || l.Done() || l.AltSeen || l.Pressed >= len(step.Keys) {
		return false
	}
	want := step.Keys[l.Pressed]
	if want.Text != "" || !IsAltChord(want.Chord) {
		return false
	}
	return now.Sub(l.StepStarted) >= AltBlockedAfter || l.KeysSeen >= AltBlockedAfterKeys
}

// MarkHinted counts the step as hinted, as "show me" does.
func (l *Lesson) MarkHinted() { l.Hinted = true }

// Elapsed is how long the lesson took, or has taken so far.
func (l *Lesson) Elapsed(now time.Time) time.Duration {
	end := now
	if l.Done() {
		end = l.Finished
	}
	return max(0, end.Sub(l.Started)).Round(time.Second)
}

// Nudge says why the person looks stuck, most specific first, or "": the
// reasons the coach speaks up about.
func Nudge(level int, wrong int, wrongMode bool, needs string, noWindow, altBlocked bool) string {
	switch {
	case noWindow:
		return "noWindow"
	case altBlocked:
		return "altBlocked"
	case wrongMode && needs == "terminal":
		return "wrongModeTerminal"
	case wrongMode:
		return "wrongModeWindow"
	case level >= 2:
		return "showMe"
	case level >= 1 && wrong > 0:
		return "wrongKeys"
	case level >= 1:
		return "idle"
	}
	return ""
}

// LacksWindow reports whether the step needs a window and the state has
// none to work with. A step whose keys open one is fine.
func LacksWindow(step *Step, state map[string]any) bool {
	if step == nil || state == nil || step.Explainer != nil {
		return false
	}
	for _, k := range step.Keys {
		if k.Text == "" && k.Chord == "n" {
			return false
		}
	}
	field := "totalWindows"
	if step.Needs != "" {
		field = "windows"
	}
	return same(state[field], 0)
}
