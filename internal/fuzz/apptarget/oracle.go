package apptarget

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

func vio(rule, format string, args ...any) []fuzz.Violation {
	return []fuzz.Violation{{Rule: rule, Detail: fmt.Sprintf(format, args...)}}
}

var oracle = []struct {
	rules []fuzz.RuleInfo
	check func(*Target) []fuzz.Violation
}{
	{[]fuzz.RuleInfo{
		{Name: "panic", Family: "process", Doc: "Update swallowed a panic, so a frame did nothing and said nothing"},
	}, checkNoRecoveredPanic},

	{[]fuzz.RuleInfo{
		{Name: "focus-index", Family: "model", Doc: "the focused pane index is outside the pane slice"},
		{Name: "workspace-range", Family: "model", Doc: "a workspace number is outside the configured range"},
		{Name: "nil-pane", Family: "model", Doc: "the pane slice holds a nil"},
	}, checkModelIndexes},

	{[]fuzz.RuleInfo{
		{Name: "pane-size", Family: "geometry", Doc: "a guest was told a size its emulator does not hold"},
	}, checkPaneSizeAgreement},

	{[]fuzz.RuleInfo{
		{Name: "layout-overlap", Family: "layout", Doc: "two tiled panes claim the same cell"},
	}, checkLayoutIsDisjoint},

	{[]fuzz.RuleInfo{
		{Name: "frame-size", Family: "layout", Doc: "the frame handed to the host is wider or taller than the host"},
	}, checkFrameFitsTheHost},
}

var checkRules = []fuzz.RuleInfo{
	{Name: "render-panic", Family: "process", Doc: "a rule panicked composing a frame, which in the real render loop kills the process"},
}

// Rules names the oracle for an attached display. It is the optional half of the
// observer seam: without it a display still sees actions and failures, with it
// the display can show every rule and how it fared.
func (t *Target) Rules() []fuzz.RuleInfo {
	out := make([]fuzz.RuleInfo, 0, len(oracle)+len(checkRules))
	for _, r := range oracle {
		out = append(out, r.rules...)
	}
	return append(out, checkRules...)
}

// Check runs the oracle against the current model.
//
// A panic raised by a rule is a finding rather than a crash. Update recovers its
// own panics, but the render does not: View runs inside bubbletea's frame loop,
// where a panic takes the process down and the user loses every pane. So the
// oracle recovers one here and reports it, which also lets the shrinker minimise
// it instead of the run dying with the binary.
func (t *Target) Check() (found []fuzz.Violation) {
	if t.m.QuitRequested {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			found = vio("render-panic", "%v (after %s, %d panes, %dx%d)",
				r, t.lastAction, len(t.m.Windows), t.m.Width, t.m.Height)
		}
	}()
	m := t.m
	for _, entry := range oracle {
		if vs := entry.check(t); len(vs) > 0 {
			for i := range vs {
				vs[i].Detail += fmt.Sprintf(" [after %s, %d panes, %dx%d]",
					t.lastAction, len(m.Windows), m.Width, m.Height)
			}
			return vs
		}
	}
	return nil
}

func checkNoRecoveredPanic(t *Target) []fuzz.Violation {
	for _, msg := range t.m.LogMessages {
		if strings.Contains(msg.Message, "recovered panic") {
			line, _, _ := strings.Cut(msg.Message, "\n")
			return vio("panic", "%s", line)
		}
	}
	return nil
}

func checkModelIndexes(t *Target) []fuzz.Violation {
	m := t.m
	if m.FocusedWindow < -1 || m.FocusedWindow >= len(m.Windows) {
		return vio("focus-index", "FocusedWindow %d with %d panes", m.FocusedWindow, len(m.Windows))
	}
	if (m.CurrentWorkspace < 1 || m.CurrentWorkspace > m.NumWorkspaces) && !session.IsScratchWorkspace(m.CurrentWorkspace) {
		return vio("workspace-range", "CurrentWorkspace %d outside 1..%d", m.CurrentWorkspace, m.NumWorkspaces)
	}
	for i, w := range m.Windows {
		if w == nil {
			return vio("nil-pane", "Windows[%d] is nil", i)
		}
		if (w.Workspace < 1 || w.Workspace > m.NumWorkspaces) && !(w.IsScratch && session.IsScratchWorkspace(w.Workspace)) {
			return vio("workspace-range", "%s sits on workspace %d, outside 1..%d", w.ID, w.Workspace, m.NumWorkspaces)
		}
	}
	return nil
}

func checkPaneSizeAgreement(t *Target) []fuzz.Violation {
	if t.deferring() {
		return nil
	}
	for _, w := range visiblePanes(t.m) {
		cw, ch := w.ContentWidth(), w.ContentHeight()
		if cw <= 0 || ch <= 0 {
			continue
		}
		if ew, eh := w.Terminal.Width(), w.Terminal.Height(); ew != cw || eh != ch {
			return vio("pane-size", "%s emulator %dx%d, drawable %dx%d", w.ID, ew, eh, cw, ch)
		}
		aw, ah := w.AnnouncedSize()
		if aw != cw || ah != ch {
			return vio("pane-size", "%s announced %dx%d, drawable %dx%d", w.ID, aw, ah, cw, ch)
		}
		if rec := t.told[w.ID]; rec != nil && rec.calls > 0 && (rec.w != aw || rec.h != ah) {
			return vio("pane-size", "%s PTY told %dx%d, announced %dx%d", w.ID, rec.w, rec.h, aw, ah)
		}
	}
	return nil
}

func checkLayoutIsDisjoint(t *Target) []fuzz.Violation {
	m := t.m
	if !m.AutoTiling || m.LayoutModeName() == app.LayoutModeScrolling || !t.hasRoomToDraw() {
		return nil
	}
	if m.Dragging || m.Resizing {
		return nil
	}
	wins := tiledRects(m)
	for _, w := range wins {
		if w.Zoomed {
			return nil
		}
		if w.Width <= config.DefaultWindowWidth || w.Height <= config.DefaultWindowHeight {
			return nil
		}
	}
	for i := range wins {
		for j := i + 1; j < len(wins); j++ {
			a, b := wins[i], wins[j]
			ox := min(a.X+a.Width, b.X+b.Width) - max(a.X, b.X)
			oy := min(a.Y+a.Height, b.Y+b.Height) - max(a.Y, b.Y)
			if ox > 0 && oy > 0 {
				return vio("layout-overlap", "%s (%d,%d %dx%d) overlaps %s (%d,%d %dx%d) by %dx%d",
					a.ID, a.X, a.Y, a.Width, a.Height, b.ID, b.X, b.Y, b.Width, b.Height, ox, oy)
			}
		}
	}
	return nil
}

func checkFrameFitsTheHost(t *Target) []fuzz.Violation {
	m := t.m
	rows := t.frameRows()
	if m.GetRenderHeight() <= 0 || m.GetRenderWidth() <= 0 {
		return nil
	}
	if len(rows) > m.GetRenderHeight() {
		return vio("frame-size", "frame is %d rows, host is %d", len(rows), m.GetRenderHeight())
	}
	for y, row := range rows {
		if w := ansi.StringWidth(row); w > m.GetRenderWidth() {
			return vio("frame-size", "frame row %d is %d cells wide, host is %d", y, w, m.GetRenderWidth())
		}
	}
	return nil
}

func (t *Target) deferring() bool {
	_, viewport := t.m.PendingViewportResize()
	return viewport || t.m.Resizing || len(t.m.PendingResizes) > 0 ||
		t.m.AnnounceGestureHeld()
}

func (t *Target) hasRoomToDraw() bool {
	b := t.m.GetBSPBounds()
	return b.W > 0 && b.H > 0 && t.m.GetRenderWidth() > 0 && t.m.GetRenderHeight() > 0
}

func visiblePanes(m *app.OS) []*terminal.Window {
	var out []*terminal.Window
	for _, w := range m.Windows {
		if w != nil && w.Workspace == m.CurrentWorkspace && !w.Minimized && w.Terminal != nil {
			out = append(out, w)
		}
	}
	return out
}

func tiledRects(m *app.OS) []*terminal.Window {
	var out []*terminal.Window
	for _, w := range m.Windows {
		if w != nil && w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
			out = append(out, w)
		}
	}
	return out
}

// Screen is the frame the app would hand its host right now, for a display that
// wants to show the thing being tested rather than only the instruments
// measuring it.
//
// It is a pull, and it has to be pulled on the goroutine driving the target: the
// model is not safe to render from a second one. A display therefore asks for a
// frame and gets the last one taken, rather than reaching into the model itself.
// The frame is the app's own output, colours and all, because a harness that
// restyled what it was testing would be showing software that does not exist.
func (t *Target) Screen() string {
	if t.m == nil {
		return ""
	}
	return strings.Join(t.frameRows(), "\n")
}

func (t *Target) frameRows() []string {
	if t.m.GetRenderWidth() <= 0 || t.m.GetRenderHeight() <= 0 {
		return nil
	}
	return strings.Split(lipgloss.Sprint(t.m.GetCanvas(true).Render()), "\n")
}
