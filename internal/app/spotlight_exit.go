package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The way out of the spotlight.
//
// The beam dims most of the screen, and a person who switched it on by
// accident saw "Spotlight: ON" for a few seconds and then nothing that said
// what had happened or how to undo it. So while the beam is on, the dock
// carries a chip that names it and says the key that turns it off, and a click
// on the chip turns it off too. The chip and the dock's message block are
// left out of the dimming, so the way out is never in the dark.
//
// The keys. In window mode esc turns the beam off: esc is bound to
// enter_window_mode there, which does nothing in window mode, so nothing is
// taken from anyone. In terminal mode esc belongs to the program in the pane
// and stays there. A double esc was considered and rejected: vim users press
// esc twice out of habit, and Claude Code and readline give a double esc a
// meaning of their own, so the beam would go off in the middle of a demo and
// the program would lose its key. The terminal-mode way out is the leader
// chord prefix_toggle_spotlight (leader, B), which the chip names in that mode.

// spotlightChipHit is where the chip was drawn last frame, in screen cells.
type spotlightChipHit struct {
	x0, x1, y int
	drawn     bool
}

// spotlightChipLabel is the chip's name for the feature.
const spotlightChipLabel = "Spotlight"

// spotlightOffChord is the leader chord that turns the beam off, as the help
// overlay spells a chord ("ctrl+b, B"), or "" when the chord is unbound.
func (m *OS) spotlightOffChord() string {
	if m.KeybindRegistry == nil {
		return ""
	}
	keys := m.KeybindRegistry.GetKeys("prefix_toggle_spotlight")
	if len(keys) == 0 {
		return ""
	}
	return m.Settings.LeaderKey + ", " + keys[0]
}

// spotlightOffHint is the key the chip shows for turning the beam off in the
// mode the keyboard is in. In terminal mode with the chord unbound there is no
// key, and the chip says to click it.
func (m *OS) spotlightOffHint() overlay.Hint {
	if m.Mode != TerminalMode {
		return overlay.Hint{Key: "esc", Label: "turn off", Priority: overlay.HintEssential}
	}
	if chord := m.spotlightOffChord(); chord != "" {
		return overlay.Hint{Key: chord, Label: "turn off", Priority: overlay.HintEssential}
	}
	return overlay.Hint{Key: "click", Label: "turn off", Priority: overlay.HintEssential}
}

// spotlightChip is the dock chip drawn while the beam is on, with a column in
// front of it to keep it off the mode pill, or "" while the beam is off.
func (m *OS) spotlightChip() string {
	if !m.spotlight.on {
		return ""
	}
	pal := m.groundUI()
	ground := pal.WarningTint
	label := lipgloss.NewStyle().Background(ground).
		Foreground(overlay.Readable(pal.Warning, ground)).Bold(true).
		Render(" " + spotlightChipLabel + " ")
	hint := []overlay.Hint{m.spotlightOffHint()}
	strip, _ := overlay.FitHintStrip(hint, overlay.HintStripWidth(hint), pal.Panel, pal)
	pad := overlay.Style(pal.Panel).Render(" ")
	return " " + label + pad + strip + pad
}

// spotlightChipWidth is the room the chip takes in the dock's left block.
func (m *OS) spotlightChipWidth() int {
	return lipgloss.Width(m.spotlightChip())
}

// recordSpotlightChip notes where the chip landed, from the column the dock
// drew it at. The leading separator column is not part of the target.
func (m *OS) recordSpotlightChip(x, width, y int) {
	if width <= 1 {
		m.spotlight.chip = spotlightChipHit{}
		return
	}
	m.spotlight.chip = spotlightChipHit{x0: x + 1, x1: x + width, y: y, drawn: true}
}

// SpotlightChipAt reports whether (x, y) is on the spotlight chip.
func (m *OS) SpotlightChipAt(x, y int) bool {
	c := m.spotlight.chip
	return m.spotlight.on && c.drawn && y == c.y && x >= c.x0 && x < c.x1
}

// TurnOffSpotlight turns the beam off, saves the choice and says so. It does
// nothing when the beam is already off, and reports whether it did anything.
func (m *OS) TurnOffSpotlight() (tea.Cmd, bool) {
	if !m.spotlight.on {
		return nil, false
	}
	save := m.ToggleSpotlight()
	m.AnnounceSpotlight()
	return save, true
}

// AnnounceSpotlight shows the message for the beam's new state. Every path
// that switches the beam (the key, the chord, the palette, the shake, the
// chip) uses it, so they all say the same thing and the "on" message always
// says how to turn it off.
func (m *OS) AnnounceSpotlight() {
	m.ShowNotification(m.spotlightMessage(), "info", m.Settings.NotificationDuration)
}

// spotlightMessage is the text AnnounceSpotlight shows.
func (m *OS) spotlightMessage() string {
	if !m.spotlight.on {
		return "Spotlight is off."
	}
	if m.Mode != TerminalMode {
		return "Spotlight is on. Press Esc to turn it off."
	}
	if chord := m.spotlightOffChord(); chord != "" {
		return "Spotlight is on. Press " + spotlightChordWords(chord) + " to turn it off."
	}
	return "Spotlight is on. Click Spotlight in the dock to turn it off."
}

// spotlightChordWords writes "ctrl+b, B" as "Ctrl+B, B" for a sentence.
func spotlightChordWords(chord string) string {
	parts := strings.Split(chord, ", ")
	for i, p := range parts {
		mods := strings.Split(p, "+")
		for j, k := range mods[:len(mods)-1] {
			if k != "" {
				mods[j] = strings.ToUpper(k[:1]) + k[1:]
			}
		}
		if last := mods[len(mods)-1]; len(mods) > 1 && len(last) == 1 {
			mods[len(mods)-1] = strings.ToUpper(last)
		}
		parts[i] = strings.Join(mods, "+")
	}
	return strings.Join(parts, ", ")
}

// spotlightLitSpans is the way out (the chip, and the dock's message block),
// which the pass leaves undimmed so it is never in the dark.
func (m *OS) spotlightLitSpans() [2]spotlightSpan {
	var out [2]spotlightSpan
	if c := m.spotlight.chip; c.drawn {
		out[0] = spotlightSpan{y: c.y, x0: c.x0, x1: c.x1}
	}
	if n := m.notifHit; n.Active {
		out[1] = spotlightSpan{y: n.Y, x0: n.X0, x1: n.X1}
	}
	return out
}

// spotlightSpan is a run of cells on one row, x0 inclusive and x1 exclusive.
// The zero value is empty.
type spotlightSpan struct {
	y, x0, x1 int
}
