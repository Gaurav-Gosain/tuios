//go:build slim

package app

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/edition"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// tuios-slim has no screenshots and no capture mode, and does not link the
// renderer or its fonts. These stand in for screenshot.go, render_capture.go,
// render_screenshot_preview.go and the screenshot graphics files. A key or
// tape command that asks for a screenshot shows one line saying so.

// captureState keeps the fields the core reads. Capture mode never starts.
type captureState struct {
	Active   bool
	Dragging bool
}

// screenshotPreview keeps the field the core reads. The panel never opens.
type screenshotPreview struct {
	Open bool
}

type captureHit struct{}

type screenshotPlacementState struct{}

// noScreenshots says the feature is not in this build.
func (m *OS) noScreenshots() {
	m.ShowNotification(edition.MissingMessage("Screenshot"), "info", m.Settings.NotificationDuration)
}

func (m *OS) CaptureActive() bool { return false }

func (m *OS) BeginCapture(bool) { m.noScreenshots() }

func (m *OS) ScreenshotScreen() tea.Cmd { m.noScreenshots(); return nil }

func (m *OS) ScreenshotFocusedWindow() tea.Cmd { m.noScreenshots(); return nil }

// ScreenshotExec is the tape command Screenshot.
func (m *OS) ScreenshotExec() error { return edition.Missing("Screenshot") }

func (m *OS) CloseScreenshotPreview(bool) {}

func (m *OS) captureOccluders() []cellRect { return nil }

func (m *OS) renderCaptureMode() []*lipgloss.Layer { return nil }

func (m *OS) renderScreenshotPreview() (string, overlay.Geometry, []overlayRowHit) {
	return "", overlay.Geometry{}, nil
}

func (m *OS) flushScreenshotGraphicsForFrame() {}

func (m *OS) ScreenshotPreviewOpen() bool { return false }

// The capture's messages. Nothing sends them in tuios-slim.
type (
	screenshotResultMsg struct{}
	screenshotCopiedMsg struct{}
)

func (m *OS) HandleScreenshotResult(screenshotResultMsg) tea.Cmd { return nil }
func (m *OS) HandleScreenshotCopied(screenshotCopiedMsg)         {}
