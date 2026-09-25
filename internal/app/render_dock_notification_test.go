package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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
				m.ShowNotification(message, sev, m.Settings.NotificationDuration)

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

// dockRows splits a drawn dock into its hairline row and its content row,
// whichever way round the bar is configured.
func dockRows(t *testing.T, dock string) (hairline, content string) {
	t.Helper()
	rows := strings.Split(dock, "\n")
	if len(rows) != 2 {
		t.Fatalf("the dock drew %d rows, want a hairline and a bar", len(rows))
	}
	if config.Global.DockbarPosition == "top" {
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
	stroke := []rune(config.Global.GetNotificationRule(config.NotificationRuleHeavy))[0]
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
			m.ShowNotification(message, "error", m.Settings.NotificationDuration)

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
			if got := cellsFrom(stripANSIForTrace(content), z.X0); !strings.HasPrefix(got, notifCap("error", &m.Settings)) {
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
			m.ShowNotification(long, sev, m.Settings.NotificationDuration)

			block, ok := m.renderNotificationBlock(width, 0)
			if !ok {
				t.Fatalf("width %d, %s: no block for a live message", width, sev)
			}
			plain := stripANSIForTrace(block.Text)

			if glyph := notifGlyph(sev, &m.Settings); !strings.Contains(plain, glyph) {
				t.Errorf("width %d, %s: truncation took the severity mark: %q", width, sev, plain)
			}
			if cap := notifCap(sev, &m.Settings); !strings.Contains(plain, cap) {
				t.Errorf("width %d, %s: truncation took the severity cap: %q", width, sev, plain)
			}
			if !strings.Contains(plain, overlay.Ellipsis()) {
				t.Errorf("width %d, %s: a cut message should say it was cut: %q", width, sev, plain)
			}
			if strings.Contains(plain, long) {
				t.Errorf("width %d, %s: the message was not actually truncated: %q", width, sev, plain)
			}
		}
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

	m.ShowNotification("Yanked 240 chars", "success", m.Settings.NotificationDuration)
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
