package lessons

import (
	"strings"
	"time"
)

// The hint ladder, as on the page: seconds without progress before the hint
// shows, and wrong keys before it shows early.
const (
	HintAfter      = 8 * time.Second
	ShowMeAfter    = 20 * time.Second
	HintAfterWrong = 2
)

// Result is how a step went.
type Result int

const (
	Clean Result = iota
	Hinted
	Skipped
)

// Lesson is a reader's place in one track. It mirrors LessonState in the
// page's engine.ts.
type Lesson struct {
	Track     *Track
	Index     int
	Pressed   int
	Wrong     int
	HintShown bool
	WrongMode bool
	Results   []Result
	Ctx       Ctx
	Started   time.Time
	StepAt    time.Time
	ActiveAt  time.Time
	Finished  time.Time
}

// Start begins a track.
func Start(t *Track, now time.Time) *Lesson {
	return &Lesson{
		Track: t, Started: now, StepAt: now, ActiveAt: now,
		Ctx: Ctx{Mem: map[string]int{}},
	}
}

// Step is the current step, with the SSH words in place, or nil when done.
func (l *Lesson) Step() *Step {
	if l.Index >= len(l.Track.Steps) {
		return nil
	}
	s := l.Track.Steps[l.Index].ForSSH()
	return &s
}

// Done reports whether every step is finished.
func (l *Lesson) Done() bool { return !l.Finished.IsZero() }

// Advance moves to the next step.
func (l *Lesson) Advance(r Result, now time.Time) {
	l.Results = append(l.Results, r)
	l.Index++
	l.Pressed, l.Wrong, l.HintShown, l.WrongMode = 0, 0, false, false
	l.Ctx = Ctx{State: l.Ctx.State, Mem: map[string]int{}}
	l.StepAt, l.ActiveAt = now, now
	if l.Index >= len(l.Track.Steps) {
		l.Finished = now
	}
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
		key, _ := e.Data["key"].(string)
		mode, _ := e.Data["mode"].(string)
		l.Ctx.LastAction = ""
		l.trackKey(step, key, mode, now)
	case "action":
		l.Ctx.LastAction, _ = e.Data["name"].(string)
	}
	if step.Explainer == nil && step.Done != nil && step.Done.Match(e, &l.Ctx) {
		r := Clean
		if l.HintShown {
			r = Hinted
		}
		l.Advance(r, now)
		return true
	}
	return false
}

// trackKey lights the step's keycaps as they are pressed in order.
func (l *Lesson) trackKey(step *Step, key, mode string, now time.Time) {
	isStepKey := false
	for _, k := range step.Keys {
		if k.Chord != "" && SameChord(key, k.Chord) {
			isStepKey = true
		}
	}
	if step.Needs != "" && mode != "" && mode != step.Needs && isStepKey {
		l.Pressed, l.WrongMode, l.HintShown = 0, true, true
		l.Wrong++
		return
	}
	if l.Pressed < len(step.Keys) {
		want := step.Keys[l.Pressed]
		if want.Chord != "" && SameChord(key, want.Chord) {
			l.Pressed++
			l.ActiveAt, l.WrongMode = now, false
			return
		}
		if want.Text != "" {
			// Typing: every key is fine while the reader types the text.
			l.ActiveAt, l.WrongMode = now, false
			return
		}
	}
	if len(step.Keys) > 0 && step.Keys[0].Chord != "" && SameChord(key, step.Keys[0].Chord) {
		l.Pressed = 1
		l.ActiveAt, l.WrongMode = now, false
		return
	}
	l.Pressed, l.WrongMode = 0, false
	l.Wrong++
	if l.Wrong >= HintAfterWrong {
		l.HintShown = true
	}
}

// HintLevel is 0 for no hint, 1 for the hint, 2 for the hint and "show me".
func (l *Lesson) HintLevel(now time.Time) int {
	idle := now.Sub(l.ActiveAt)
	switch {
	case idle >= ShowMeAfter:
		return 2
	case l.HintShown || idle >= HintAfter || l.WrongMode:
		return 1
	}
	return 0
}

// SameChord reports whether the key tuios reported is the chord a step asks
// for. "|" and "shift+|" are the same, and case does not matter.
func SameChord(reported, wanted string) bool {
	if reported == wanted {
		return true
	}
	norm := func(k string) string {
		if rest, ok := strings.CutPrefix(k, "shift+"); ok && len([]rune(rest)) == 1 {
			k = rest
		}
		k = strings.ToLower(k)
		switch k {
		case "escape":
			return "esc"
		case "return":
			return "enter"
		}
		return k
	}
	return norm(reported) == norm(wanted)
}

// KeyBytes is what "show me" types for a key: the bytes a terminal sends.
func KeyBytes(k Key) string {
	if k.Text != "" {
		return k.Text
	}
	c := k.Chord
	if rest, ok := strings.CutPrefix(c, "ctrl+"); ok && len(rest) == 1 {
		b := rest[0]
		if b >= 'a' && b <= 'z' {
			return string(rune(b - 'a' + 1))
		}
	}
	if rest, ok := strings.CutPrefix(c, "alt+"); ok {
		return "\x1b" + KeyBytes(Key{Chord: rest})
	}
	switch c {
	case "enter":
		return "\r"
	case "esc", "escape":
		return "\x1b"
	case "tab":
		return "\t"
	case "shift+tab":
		return "\x1b[Z"
	case "space":
		return " "
	case "up":
		return "\x1b[A"
	case "down":
		return "\x1b[B"
	case "right":
		return "\x1b[C"
	case "left":
		return "\x1b[D"
	case "backspace":
		return "\x7f"
	}
	if rest, ok := strings.CutPrefix(c, "shift+"); ok && len(rest) == 1 {
		return strings.ToUpper(rest)
	}
	return c
}

// Label is a keycap's text.
func Label(k Key) string {
	if k.Text != "" {
		return k.Text
	}
	return k.Chord
}
