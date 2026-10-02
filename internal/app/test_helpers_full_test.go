//go:build !slim

package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// saverSettings is the settings a saver engine is built from, with the frame
// rate the caller names. NormalFPS is the only field screensaverBuild reads,
// and it is per session now, so a test says the rate by handing one over
// instead of writing a package variable another session could read.
func saverSettings(rate int) *config.Settings {
	s := config.DefaultSettings()
	s.NormalFPS = rate
	return &s
}

// defaultSaverSettings is saverSettings at the rate a session starts on, for
// the tests that build an engine but make no claim about its clock.
func defaultSaverSettings() *config.Settings {
	return saverSettings(config.DefaultSettings().NormalFPS)
}

// hostCapture stands in for the terminal on the far end of the render stream.
type hostCapture struct{ b strings.Builder }

func (h *hostCapture) Write(p []byte) (int, error) { return h.b.Write(p) }

// captureHost points the client's raw host writes at a buffer.
func captureHost(t *testing.T, m *OS) *hostCapture {
	t.Helper()
	h := &hostCapture{}
	m.KittyPassthrough = NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: h})
	return h
}
