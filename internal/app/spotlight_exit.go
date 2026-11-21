package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type spotlightChipHit struct {
	x0, x1, y int
	drawn     bool
}

const spotlightChipLabel = "Spotlight"

func (m *OS) spotlightOffChord() string {
	if m.KeybindRegistry == nil {
		return ""
	}
	keys := m.KeybindRegistry.GetKeys("toggle_spotlight")
	if len(keys) == 0 {
		return ""
	}
	return m.Settings.LeaderKey + ", " + keys[0]
}

func (m *OS) spotlightOffHint() overlay.Hint {
	if m.Mode != TerminalMode {
		return overlay.Hint{Key: "esc", Label: "turn off", Priority: overlay.HintEssential}
	}
	if chord := m.spotlightOffChord(); chord != "" {
		return overlay.Hint{Key: chord, Label: "turn off", Priority: overlay.HintEssential}
	}
	return overlay.Hint{Key: "click", Label: "turn off", Priority: overlay.HintEssential}
}

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

func (m *OS) spotlightChipWidth() int {
	return lipgloss.Width(m.spotlightChip())
}

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

type spotlightSpan struct {
	y, x0, x1 int
}
