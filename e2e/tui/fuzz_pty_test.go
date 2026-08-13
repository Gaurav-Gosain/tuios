package tuie2e

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuitest"
)

// The other end of the fuzzer: the same action alphabet, replayed against a
// real tuios in a real PTY with a real daemon behind it.
//
// It is deliberately the weaker oracle. In-process the fuzzer can ask the model
// what size it told each guest and where it recorded every hit rectangle; here
// there is only what a user would see. What this target has instead is
// everything the in-process one stubs out: the daemon protocol, the PTY layer,
// the terminal writer, and the process staying alive. A finding from either can
// be pasted into the other, because the script format is shared.
//
// Runs are short and shrinking is off by default. A replay costs a full tuios
// boot plus a daemon, so the minimisation that takes seconds in process takes
// minutes here; a PTY finding is reproduced with its script and then narrowed
// in process, which is orders of magnitude faster.

// ptyTarget drives one tuios instance.
type ptyTarget struct {
	t    *testing.T
	term *tuitest.Terminal
	cols int
	rows int
	last fuzz.Action
	held int
}

func newPTYTarget(t *testing.T) func() (fuzz.Target, error) {
	return func() (fuzz.Target, error) { return &ptyTarget{t: t, cols: 120, rows: 40}, nil }
}

func (p *ptyTarget) Reset() error {
	p.cols, p.rows, p.held = 120, 40, 0
	term, _ := start(p.t, startOpts{cols: p.cols, rows: p.rows})
	waitBoot(p.t, term)
	p.term = term
	return nil
}

func (p *ptyTarget) Close() {
	// tuitest registers its own teardown against the test, and start registers
	// the daemon kill, so both are reaped when the test ends. Closing here as
	// well would tear down a terminal those cleanups still hold.
	p.term = nil
}

// Apply replays one action as real bytes on the wire. Actions with no keyboard
// or pointer spelling are mapped onto the chord a user would actually press,
// which is the point of running this target at all: it exercises the binding
// table, not the method behind it.
func (p *ptyTarget) Apply(a fuzz.Action) error {
	p.last = a
	t := p.term
	if t == nil {
		return nil
	}
	var err error
	switch a.Kind {
	case fuzz.Key:
		err = t.SendKeys(ptyKey(a.S))
	case fuzz.Chord:
		err = t.SendKeys(tuitest.Ctrl('b'), ptyKey(a.S))
	case fuzz.Text:
		err = t.Type(a.S)
	case fuzz.MousePress:
		p.held = a.C
		err = t.SendMouse(p.mouse(a, tuitest.MousePress))
	case fuzz.MouseMotion:
		err = t.SendMouse(p.mouse(a, tuitest.MouseMove))
	case fuzz.MouseRelease:
		p.held = 0
		err = t.SendMouse(p.mouse(a, tuitest.MouseRelease))
	case fuzz.MouseWheel:
		err = t.SendMouse(p.mouse(a, tuitest.MousePress))
	case fuzz.Resize:
		// The PTY refuses a zero dimension, and the degenerate-viewport class
		// is the in-process target's to hunt; here the floor keeps the run
		// exploring instead of wedging on an ioctl error.
		p.cols, p.rows = max(a.A, 20), max(a.B, 6)
		err = t.Resize(p.cols, p.rows)
	case fuzz.NewPane:
		err = t.SendKeys(tuitest.Ctrl('b'), "c")
	case fuzz.ClosePane:
		err = t.SendKeys(tuitest.Ctrl('b'), "x")
	case fuzz.ZoomPane:
		err = t.SendKeys(tuitest.Ctrl('b'), "z")
	case fuzz.FocusPane:
		err = t.SendKeys(tuitest.Ctrl('b'), "n")
	case fuzz.MovePane:
		err = t.SendKeys([]string{"left", "right", "up", "down"}[a.A%4])
	case fuzz.SwitchWorkspace:
		err = t.SendKeys(tuitest.Ctrl('b'), "w", strconv.Itoa(1+a.A%9))
	case fuzz.ToggleTiling:
		err = t.SendKeys(tuitest.Ctrl('b'), " ")
	case fuzz.LayoutMode:
		err = t.SendKeys(tuitest.Ctrl('b'), "L")
	case fuzz.ToggleSidebar, fuzz.SidebarCollapse, fuzz.SidebarPosition:
		err = t.SendKeys(tuitest.Ctrl('b'), "b")
	case fuzz.OpenOverlay:
		err = t.SendKeys(tuitest.Ctrl('b'), []string{"?", "P", "S", "W", ","}[a.A%5])
	case fuzz.CloseOverlay:
		err = t.SendKeys("esc")
	case fuzz.Rename:
		if err = t.SendKeys(tuitest.Ctrl('b'), "r"); err == nil {
			if err = t.Type(sanitiseName(a.S)); err == nil {
				err = t.SendKeys("enter")
			}
		}
	case fuzz.Detach:
		err = t.SendKeys(tuitest.Ctrl('b'), "d")
	case fuzz.Guest:
		err = t.Type(a.S)
	case fuzz.SwitchSession, fuzz.ToggleShared, fuzz.Setting, fuzz.Attach, fuzz.Tick:
		// Nothing to send. A session switch and a detach-reattach need a second
		// client to be meaningful, the border and settings toggles live behind
		// the settings panel rather than a chord, and time passes on its own
		// here because the real timers are running.
	}
	// The screen is asynchronous, so a beat is needed before the assertion or
	// the check reads the frame from before the action.
	time.Sleep(12 * time.Millisecond)
	return err
}

// Check is what a user could see going wrong. It is a short list on purpose:
// anything the model can be asked directly belongs in the in-process oracle,
// and duplicating it here against a screen scrape would be slower and less
// certain about the same property.
func (p *ptyTarget) Check() []fuzz.Violation {
	t := p.term
	if t == nil {
		return nil
	}
	if code, exited := t.ExitCode(); exited {
		return []fuzz.Violation{{
			Rule:   "pty-exit",
			Detail: "tuios exited with code " + strconv.Itoa(code) + " after " + p.last.String(),
		}}
	}
	s := t.Screen()
	text := s.Text()
	// A Go runtime panic reaches the screen before the process dies, and it is
	// the one failure a user reports as "it vanished".
	for _, marker := range []string{"panic:", "goroutine ", "runtime error:", "SIGSEGV"} {
		if strings.Contains(text, marker) {
			return []fuzz.Violation{{
				Rule:   "pty-panic",
				Detail: "the screen shows " + strconv.Quote(marker) + " after " + p.last.String(),
			}}
		}
	}
	cols, rows := s.Size()
	if cols != p.cols || rows != p.rows {
		return []fuzz.Violation{{
			Rule: "pty-size",
			Detail: "the grid is " + strconv.Itoa(cols) + "x" + strconv.Itoa(rows) +
				", the PTY is " + strconv.Itoa(p.cols) + "x" + strconv.Itoa(p.rows),
		}}
	}
	return nil
}

// Rules names this target's oracle for an attached display.
func (p *ptyTarget) Rules() []string { return []string{"pty-exit", "pty-panic", "pty-size"} }

func (p *ptyTarget) mouse(a fuzz.Action, action tuitest.MouseAction) tuitest.MouseEvent {
	return tuitest.MouseEvent{
		Col:    min(max(a.A, 0), p.cols-1),
		Row:    min(max(a.B, 0), p.rows-1),
		Button: ptyButton(a.C),
		Action: action,
	}
}

func ptyButton(c int) tuitest.MouseButton {
	switch c {
	case fuzz.ButtonRight:
		return tuitest.MouseRight
	case fuzz.ButtonMiddle:
		return tuitest.MouseMiddle
	}
	return tuitest.MouseLeft
}

// ptyKey maps a key name onto what tuitest sends. Modified names are spelled
// out; everything else goes as its own text.
func ptyKey(name string) any {
	if rest, ok := strings.CutPrefix(name, "ctrl+"); ok && len(rest) == 1 {
		return tuitest.Ctrl(rune(rest[0]))
	}
	if rest, ok := strings.CutPrefix(name, "alt+"); ok {
		return tuitest.Alt(rest)
	}
	if rest, ok := strings.CutPrefix(name, "shift+"); ok {
		return strings.ToUpper(rest)
	}
	if name == "space" {
		return " "
	}
	return name
}

// sanitiseName drops the control characters from a generated name. They are the
// point of the pool in process, where the name reaches the width table
// directly; sent down a PTY they would be read as keys and the run would stop
// meaning what its script says.
func sanitiseName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "a"
	}
	return b.String()
}

// TestFuzzPTY is the bounded PTY campaign:
//
//	cd e2e/tui && TUIOS_E2E=1 go test -count=1 -run TestFuzzPTY ./...
//
// Seeds and steps are settable so a local run can go wider:
//
//	TUIOS_E2E=1 TUIOS_FUZZ_SEEDS=200 TUIOS_FUZZ_STEPS=120 \
//	  go test -count=1 -run TestFuzzPTY -timeout 4h ./...
func TestFuzzPTY(t *testing.T) {
	seeds := ptyEnvInt(t, "TUIOS_FUZZ_SEEDS", 2)
	steps := ptyEnvInt(t, "TUIOS_FUZZ_STEPS", 40)
	shrink := os.Getenv("TUIOS_FUZZ_SHRINK") != ""

	for seed := range uint64(seeds) {
		res, err := fuzz.Run(newPTYTarget(t), fuzz.Config{
			Seed: seed, Steps: steps,
			MinWidth: 40, MinHeight: 12,
			NoShrink: !shrink, ShrinkBudget: 40,
		})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if res.Failed {
			t.Errorf("seed %d broke %s\n%s", seed, res.Violations[0].Rule, res.Repro())
		}
	}
}

func ptyEnvInt(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("%s=%q: %v", name, v, err)
	}
	return n
}
