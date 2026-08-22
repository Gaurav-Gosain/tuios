package app

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// notifTestOS is an OS wide enough to draw a dock, with nothing else on it.
func notifTestOS(t testing.TB, width int) *OS {
	t.Helper()
	win := newTestWindow(t, "notif-render-0001", 60, 20)
	win.Workspace = 1
	m := newTestOS(win)
	m.Width, m.Height = width, 40
	m.CurrentWorkspace = 1
	return m
}

// notifWidths are the widths every geometry assertion is run at. 80 is there
// because that is where the dock is tight enough for the message and the mode
// pill to be fighting over the same columns, which is where the old renderer's
// after-the-fact clamp did its damage.
var notifWidths = []int{40, 60, 80, 100, 120, 200}

// TestNotificationBlockStaysInsideItsBudget is the geometry contract.
//
// The block's width is what the dock reserves for it, what the burn rule is
// drawn to, and what the block itself measures. The old renderer had no such
// contract: it clamped a rendered box with MaxWidth and let whatever was last
// fall off the end, so a long message on a narrow screen lost its closing cap
// and the box stopped reading as a shape at all.
func TestNotificationBlockStaysInsideItsBudget(t *testing.T) {
	messages := []string{
		"ok",
		"Layout saved: development",
		strings.Repeat("a message far longer than any dock will ever hold ", 6),
	}

	for _, width := range notifWidths {
		for _, sev := range []string{"info", "success", "warning", "error"} {
			for _, message := range messages {
				m := notifTestOS(t, width)
				m.ShowNotification(message, sev, config.NotificationDuration)

				block, ok := m.renderNotificationBlock(width, 0)
				if !ok {
					t.Fatalf("width %d, %s: no block for a live message", width, sev)
				}

				budget := notifBudget(width)
				if block.Width > budget {
					t.Errorf("width %d, %s: block is %d columns, budget is %d",
						width, sev, block.Width, budget)
				}
				if got := lipgloss.Width(block.Text); got != block.Width {
					t.Errorf("width %d, %s: block measures %d but reports %d", width, sev, got, block.Width)
				}
				if block.Width > width {
					t.Errorf("width %d, %s: block is wider than the screen (%d)", width, sev, block.Width)
				}
			}
		}
	}
}

// TestNotificationRuleMatchesTheBlockSpan pins the rule to the block.
//
// The burn is legible only because it runs across exactly the span the message
// occupies: that is what makes "how much rule is left" mean "how much message
// is left" rather than being a bar of its own somewhere on the dock. A rule
// that is a column longer or shorter than the block is a rule that is lying.
func TestNotificationRuleMatchesTheBlockSpan(t *testing.T) {
	for _, width := range notifWidths {
		for _, sev := range []string{"info", "success", "warning", "error"} {
			m := notifTestOS(t, width)
			m.ShowNotification("Layout applied: development", sev, config.NotificationDuration)

			block, ok := m.renderNotificationBlock(width, 0)
			if !ok {
				t.Fatalf("width %d, %s: no block for a live message", width, sev)
			}
			if got := lipgloss.Width(block.Rule); got != block.Width {
				t.Errorf("width %d, %s: rule spans %d columns, block spans %d", width, sev, got, block.Width)
			}
		}
	}
}

// TestNotificationBurnsDownAndStickyDoesNot covers what the rule is for.
//
// A timed message's lit run shortens as it ages, and a sticky one's does not
// move at all. "Not moving" is the affordance that the message is waiting for
// you, so a sticky error whose rule crept downwards would be saying the
// opposite of what is true.
func TestNotificationBurnsDownAndStickyDoesNot(t *testing.T) {
	const width = 120

	m := notifTestOS(t, width)
	m.ShowNotification("Building project session", "info", 10*time.Second)

	fresh, _ := m.renderNotificationBlock(width, 0)
	freshStatus, _ := m.notifStatus()
	freshLit := notifLitSpan(freshStatus.frac, fresh.Width)

	m.Notifications[0].StartTime = time.Now().Add(-8 * time.Second)
	aged, _ := m.renderNotificationBlock(width, 0)
	agedStatus, _ := m.notifStatus()
	agedLit := notifLitSpan(agedStatus.frac, aged.Width)

	if agedLit >= freshLit {
		t.Errorf("the rule did not burn down: %d lit cells when fresh, %d when nearly expired", freshLit, agedLit)
	}
	if agedLit < 1 {
		t.Error("a message still on screen should keep at least one lit cell, or it reads as already gone")
	}
	if fresh.Rule == aged.Rule {
		t.Error("the rendered rule did not change as the message aged")
	}

	// A sticky error lights the rule end to end and stays there.
	s := notifTestOS(t, width)
	s.ShowNotification("Failed to save layout", "error", config.NotificationDuration)
	if !s.Notifications[0].Sticky {
		t.Fatal("an error should be sticky by default")
	}

	before, _ := s.renderNotificationBlock(width, 0)
	beforeStatus, _ := s.notifStatus()
	if got := notifLitSpan(beforeStatus.frac, before.Width); got != before.Width {
		t.Errorf("a sticky error should light the whole span: %d of %d", got, before.Width)
	}

	s.Notifications[0].StartTime = time.Now().Add(-time.Hour)
	after, _ := s.renderNotificationBlock(width, 0)
	afterStatus, _ := s.notifStatus()
	if got := notifLitSpan(afterStatus.frac, after.Width); got != after.Width {
		t.Errorf("a sticky error's rule moved after an hour: %d of %d lit", got, after.Width)
	}
	if before.Rule != after.Rule {
		t.Error("a sticky error's rule must not move; a rule that has stopped is the affordance that it is waiting for you")
	}
}

// dockRows splits a drawn dock into its hairline row and its content row,
// whichever way round the bar is configured.
func dockRows(t *testing.T, dock string) (hairline, content string) {
	t.Helper()
	rows := strings.Split(dock, "\n")
	if len(rows) != 2 {
		t.Fatalf("the dock drew %d rows, want a hairline and a bar", len(rows))
	}
	if config.DockbarPosition == "top" {
		return rows[1], rows[0]
	}
	return rows[0], rows[1]
}

// notifBurnSpan is the stretch of the drawn hairline carrying the burn stroke,
// as [x0, x1). Read off the frame rather than off the block, which is the whole
// point: the two used to be a session strip's width apart and every measurement
// taken from the block itself agreed with the block.
func notifBurnSpan(t *testing.T, hairline string) (int, int) {
	t.Helper()
	stroke := []rune(config.GetNotificationRule(config.NotificationRuleHeavy))[0]
	x0, x1 := -1, -1
	for i, r := range []rune(stripANSIForTrace(hairline)) {
		if r != stroke {
			continue
		}
		if x0 < 0 {
			x0 = i
		}
		if x1 >= 0 && x1 != i {
			t.Fatalf("the burn is drawn in two pieces: a gap before column %d", i)
		}
		x1 = i + 1
	}
	if x0 < 0 {
		t.Fatal("the hairline carries no burn at all")
	}
	return x0, x1
}

// cellsFrom is what a plain row shows from an absolute column onwards.
func cellsFrom(plain string, col int) string {
	x := 0
	for i, r := range plain {
		if x >= col {
			return plain[i:]
		}
		x += lipgloss.Width(string(r))
	}
	return ""
}

// TestNotificationBurnSitsUnderItsOwnBlock is the anchoring contract, asserted
// off the drawn frame.
//
// The burn was drawn at the right-hand end of the screen, which was the block's
// own span only while the bar ran that far. The session controls hold those
// columns now, so the rule was landing under them, a strip's width to the right
// of the message it was timing: a progress indicator for something else.
func TestNotificationBurnSitsUnderItsOwnBlock(t *testing.T) {
	messages := []string{
		"ok",
		"Layout saved: development",
		strings.Repeat("a message far longer than any dock will ever hold ", 4),
	}

	for _, width := range []int{80, 120, 200} {
		for _, message := range messages {
			m := notifTestOS(t, width)
			// An error is sticky, so the whole span is lit and the burn's extent
			// is the block's extent exactly.
			m.ShowNotification(message, "error", config.NotificationDuration)

			dock, _ := m.renderDockString()
			hairline, content := dockRows(t, dock)
			z := m.notifHit
			if !z.Active {
				t.Fatalf("width %d: the dock drew no message block", width)
			}

			x0, x1 := notifBurnSpan(t, hairline)
			if x0 != z.X0 || x1 != z.X1 {
				t.Errorf("width %d, %d-column message: the burn covers [%d,%d), the block sits at [%d,%d)",
					width, lipgloss.Width(message), x0, x1, z.X0, z.X1)
			}

			// The recorded rect is the block's real place on the row below, so
			// the two assertions above are about the same thing the user sees.
			if got := cellsFrom(stripANSIForTrace(content), z.X0); !strings.HasPrefix(got, notifCap("error")) {
				t.Errorf("width %d: column %d of the bar reads %q, want the block's opening cap",
					width, z.X0, cellsFrom(got, 0))
			}

			// And nothing else is under it.
			for _, h := range m.dockSessionHits {
				if h.X0 < x1 && x0 < h.X1 {
					t.Errorf("width %d: the burn [%d,%d) runs under a session control [%d,%d)",
						width, x0, x1, h.X0, h.X1)
				}
			}
		}
	}
}

// TestNotificationBurnShortensFromTheBlocksOwnEnd: a half-burnt message keeps
// its rule's left edge on the block's first column and gives up columns from
// the right, so what is left of the rule is what is left of the message.
func TestNotificationBurnShortensFromTheBlocksOwnEnd(t *testing.T) {
	for _, width := range []int{80, 120, 200} {
		m := notifTestOS(t, width)
		m.ShowNotification("Recording saved: demo.tape", "warning", 10*time.Second)
		m.Notifications[0].StartTime = time.Now().Add(-8 * time.Second)

		dock, _ := m.renderDockString()
		hairline, _ := dockRows(t, dock)
		x0, x1 := notifBurnSpan(t, hairline)

		z := m.notifHit
		if x0 != z.X0 {
			t.Errorf("width %d: an aged burn starts at %d, the block starts at %d", width, x0, z.X0)
		}
		if x1 >= z.X1 {
			t.Errorf("width %d: an aged burn still covers [%d,%d) of a block ending at %d",
				width, x0, x1, z.X1)
		}
		if x1 <= x0 {
			t.Errorf("width %d: a live message burnt out entirely: [%d,%d)", width, x0, x1)
		}
	}
}

// TestNotificationBurnClearsTheDismissTarget: the burn is on the hairline and
// the dismiss zone is on the bar, one row apart, so the timer can never cover
// the way out of a sticky message.
func TestNotificationBurnClearsTheDismissTarget(t *testing.T) {
	m := notifTestOS(t, 120)
	m.ShowNotification("Failed to save layout: permission denied", "error", config.NotificationDuration)

	dock, _ := m.renderDockString()
	hairline, _ := dockRows(t, dock)
	notifBurnSpan(t, hairline)

	z := m.notifHit
	if z.DismissX0 < z.X0 || z.DismissX0 >= z.X1 {
		t.Fatalf("the dismiss zone opens at %d, outside the block's [%d,%d)", z.DismissX0, z.X0, z.X1)
	}
	if !m.NotificationClick(z.DismissX0, z.Y) || len(m.Notifications) != 0 {
		t.Error("the dismiss target did not take the sticky message off the dock")
	}
}

// TestNotificationTruncationCutsTheMessageNotTheSeverity is the truncation
// contract: whatever has to go, the severity does not.
//
// The old renderer sliced the message with a byte offset and then clamped the
// whole box, so a narrow dock could take the icon, the trailing padding, or the
// middle of a multi-byte character. A message cut down to nothing must still
// say how bad it was, because that is the part the user acts on.
func TestNotificationTruncationCutsTheMessageNotTheSeverity(t *testing.T) {
	long := strings.Repeat("failed to reach the daemon and could not recover ", 5)

	for _, width := range notifWidths {
		for _, sev := range []string{"info", "success", "warning", "error"} {
			m := notifTestOS(t, width)
			m.ShowNotification(long, sev, config.NotificationDuration)

			block, ok := m.renderNotificationBlock(width, 0)
			if !ok {
				t.Fatalf("width %d, %s: no block for a live message", width, sev)
			}
			plain := stripANSIForTrace(block.Text)

			if glyph := notifGlyph(sev); !strings.Contains(plain, glyph) {
				t.Errorf("width %d, %s: truncation took the severity mark: %q", width, sev, plain)
			}
			if cap := notifCap(sev); !strings.Contains(plain, cap) {
				t.Errorf("width %d, %s: truncation took the severity cap: %q", width, sev, plain)
			}
			if !strings.Contains(plain, "...") {
				t.Errorf("width %d, %s: a cut message should say it was cut: %q", width, sev, plain)
			}
			if strings.Contains(plain, long) {
				t.Errorf("width %d, %s: the message was not actually truncated: %q", width, sev, plain)
			}
		}
	}
}

// TestSeverityCapsAreDistinctWeights is the greyscale check.
//
// Severity has to survive a screenshot with no colour in it, which is what the
// weighted cap is for. Three severities sharing a weight would put the whole
// channel back on hue, which is the failure the prototype found when the
// weights were an eighth apart instead of two.
func TestSeverityCapsAreDistinctWeights(t *testing.T) {
	info, warn, err := notifCap("info"), notifCap("warning"), notifCap("error")
	if notifCap("success") != info {
		t.Error("success and info should share the light cap; they are both routine")
	}
	if info == warn || warn == err || info == err {
		t.Errorf("the three cap weights must differ: info %q, warning %q, error %q", info, warn, err)
	}
}

// TestQueuedErrorIsStillIndicatedWhenBuried is the overflow contract.
//
// The newest message wins the block, which means a later info can push an error
// out of sight. The counter behind it takes the colour of the worst thing
// waiting precisely so that this is still visible; without it the error would
// be gone from the screen with nothing saying it had ever arrived.
func TestQueuedErrorIsStillIndicatedWhenBuried(t *testing.T) {
	const width = 120

	m := notifTestOS(t, width)
	m.ShowNotification("Failed to save layout: permission denied", "error", config.NotificationDuration)
	m.ShowNotification("Window created (2 total)", "info", config.NotificationDuration)

	block, ok := m.renderNotificationBlock(width, 0)
	if !ok {
		t.Fatal("no block for a live message")
	}
	plain := stripANSIForTrace(block.Text)

	if !strings.Contains(plain, "Window created") {
		t.Errorf("the newest message should hold the block: %q", plain)
	}
	if !strings.Contains(plain, "+1") {
		t.Errorf("the buried error should be counted: %q", plain)
	}

	// The counter is drawn in the worst queued severity, not the dim default,
	// which is the part that says the thing behind is worth going back for.
	s, _ := m.notifStatus()
	if s.worst != "error" {
		t.Errorf("worst queued severity = %q, want error", s.worst)
	}

	// Several more messages do not change the shape, only the count.
	for i := 0; i < 4; i++ {
		m.ShowNotification("Client joined", "info", config.NotificationDuration)
	}
	block, _ = m.renderNotificationBlock(width, 0)
	plain = stripANSIForTrace(block.Text)
	if !strings.Contains(plain, "+5") {
		t.Errorf("a queue of six should report five behind the newest: %q", plain)
	}
	if lipgloss.Width(block.Text) > notifBudget(width) {
		t.Errorf("a queue pushed the block over budget: %d", lipgloss.Width(block.Text))
	}
}

// TestNotificationOutranksCopyModeHelp is the collision fix.
//
// Copy mode used to hold the dock's right-hand block unconditionally, so a
// message pushed while it was active was not crowded out, it was never drawn:
// "Yanked 240 chars" and any failure behind it went nowhere. A message is a
// thing that just happened and will not repeat; the help line is a reminder the
// user can have back in a moment.
func TestNotificationOutranksCopyModeHelp(t *testing.T) {
	const width = 120

	m := notifTestOS(t, width)
	win := m.Windows[0]
	win.CopyMode = &terminal.CopyMode{Active: true, State: terminal.CopyModeNormal}

	dock, _ := m.renderDockString()
	if !strings.Contains(stripANSIForTrace(dock), "hjkl") {
		t.Fatal("copy mode should hold the dock's right block when nothing else does")
	}

	m.ShowNotification("Yanked 240 chars", "success", config.NotificationDuration)
	dock, _ = m.renderDockString()
	plain := stripANSIForTrace(dock)

	if !strings.Contains(plain, "Yanked 240 chars") {
		t.Errorf("a message pushed during copy mode was dropped: %q", plain)
	}
	if strings.Contains(plain, "hjkl:move") {
		t.Errorf("the help line should give the block up for a live message: %q", plain)
	}

	// And it comes back when the message goes.
	m.DismissNotifications()
	dock, _ = m.renderDockString()
	if !strings.Contains(stripANSIForTrace(dock), "hjkl") {
		t.Error("the copy-mode help line did not come back after the message was dismissed")
	}
}

// TestDockStaysOneScreenWideWithAMessage is the containment check on the whole
// bar rather than the block alone. The dock is composed of a left block, the
// window pills and the right block, and a message that fits its own budget can
// still push the bar past the screen if the budget ignored what the rest of the
// dock is already using.
func TestDockStaysOneScreenWideWithAMessage(t *testing.T) {
	long := strings.Repeat("a very long failure message that keeps going ", 4)

	for _, width := range notifWidths {
		m := notifTestOS(t, width)
		m.ShowNotification(long, "error", config.NotificationDuration)

		dock, _ := m.renderDockString()
		for i, line := range strings.Split(dock, "\n") {
			if got := lipgloss.Width(line); got != width {
				t.Errorf("width %d: dock row %d is %d columns", width, i, got)
			}
		}
	}
}

// TestNotificationLifetimeFloorsAndStickiness pins the durations.
//
// 1500ms was a WCAG 2.2.1 Level A failure: a time limit on reading content with
// none of the six exemptions. The floor is per severity, a caller asking for
// longer still gets longer, and an error does not run on a timer at all.
func TestNotificationLifetimeFloorsAndStickiness(t *testing.T) {
	tests := []struct {
		name      string
		notifType string
		requested time.Duration
		want      time.Duration
		sticky    bool
	}{
		{"info takes the floor", "info", 1500 * time.Millisecond, config.NotificationDuration, false},
		{"success takes the floor", "success", time.Second, config.NotificationDuration, false},
		{"a longer request wins", "info", time.Minute, time.Minute, false},
		{"warning has its own floor", "warning", time.Second, config.NotificationWarningDuration, false},
		{"error waits to be dismissed", "error", time.Second, 0, true},
		{"a zero-duration error is still shown", "error", 0, 0, true},
		{"a zero-duration info is not shown", "info", 0, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, sticky := notificationLifetime(tc.notifType, tc.requested)
			if got != tc.want || sticky != tc.sticky {
				t.Errorf("notificationLifetime(%q, %v) = %v, sticky %v; want %v, sticky %v",
					tc.notifType, tc.requested, got, sticky, tc.want, tc.sticky)
			}
		})
	}

	// The severity floors themselves must clear the accessibility minimum, or
	// the defaults are back where they started.
	if config.NotificationDuration < 4*time.Second {
		t.Errorf("the default message lifetime is %v, under the 4s readability floor", config.NotificationDuration)
	}
}

// TestNotificationNeverCoversAPane is the placement decision, asserted rather
// than assumed. The corner toast was drawn as a layer over the workspace and
// landed on the panes it was reporting about; the message block is part of the
// dock and contributes no layer at all.
func TestNotificationNeverCoversAPane(t *testing.T) {
	m := notifTestOS(t, 120)
	m.ShowNotification("Recording saved: demo.tape", "success", config.NotificationDuration)

	for _, layer := range m.renderOverlays() {
		if id := layer.GetID(); strings.HasPrefix(id, "notif") {
			t.Errorf("a message produced an overlay layer %q; it belongs to the dock", id)
		}
		if strings.Contains(stripANSIForTrace(layer.GetContent()), "Recording saved") {
			t.Errorf("a message was drawn over the workspace in layer %q", layer.GetID())
		}
	}

	// And it is in the dock, which is the other half of the same claim.
	dock, _ := m.renderDockString()
	if !strings.Contains(stripANSIForTrace(dock), "Recording saved") {
		t.Error("the message is not in the dock either; it went nowhere")
	}
}
