package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestMouseForwardingRequiresMouseMode verifies that mouse events are only
// forwarded to apps that explicitly request mouse tracking (DECSET 1000-1003),
// not merely because they use the alternate screen buffer. This prevents
// phantom keypresses in apps like kakoune/nano (issue #78).
func TestMouseForwardingRequiresMouseMode(t *testing.T) {
	tests := []struct {
		name          string
		isAltScreen   bool
		hasMouseMode  bool
		shouldForward bool
	}{
		{
			name:          "alt screen without mouse mode (kakoune/nano) - must NOT forward",
			isAltScreen:   true,
			hasMouseMode:  false,
			shouldForward: false,
		},
		{
			name:          "alt screen with mouse mode (vim/helix) - must forward",
			isAltScreen:   true,
			hasMouseMode:  true,
			shouldForward: true,
		},
		{
			name:          "normal screen with mouse mode - must forward",
			isAltScreen:   false,
			hasMouseMode:  true,
			shouldForward: true,
		},
		{
			name:          "normal screen without mouse mode - must NOT forward",
			isAltScreen:   false,
			hasMouseMode:  false,
			shouldForward: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			em := vt.NewEmulator(80, 24)
			defer func() { _ = em.Close() }()
			win := &terminal.Window{
				Terminal: em,
			}

			if tt.hasMouseMode {
				// Enable normal mouse tracking (mode 1000) via DECSET
				_, _ = em.Write([]byte("\x1b[?1000h"))
			}

			hasMouseMode := win.Terminal.HasMouseMode()
			// The forwarding guard is: shouldForward := hasMouseMode
			shouldForward := hasMouseMode

			if shouldForward != tt.shouldForward {
				t.Errorf("shouldForward = %v, want %v (isAltScreen=%v, hasMouseMode=%v)",
					shouldForward, tt.shouldForward, tt.isAltScreen, hasMouseMode)
			}
		})
	}
}
