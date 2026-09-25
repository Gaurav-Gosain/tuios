package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A read-only session applies a change and does not write it: the web client is
// one (cmd/tuios-web sets ConfigReadOnly), and so is any client that must not
// decide the config file's contents for whoever else is attached. A panel that
// silently does not save looks exactly like one that saved, so each of these
// says so in its title, where a notification would be gone by the time the
// second setting is changed.

// TestReadOnlySettingsPanelSaysSo checks the settings panel's own title.
func TestReadOnlySettingsPanelSaysSo(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 44}
	m.UserConfig = config.DefaultConfig()
	m.ShowSettings = true

	writable, _, _ := m.renderSettings()
	if strings.Contains(writable, "this session only") {
		t.Error("a writable session claims it cannot save")
	}

	m.ConfigReadOnly = true
	readOnly, _, _ := m.renderSettings()
	if !strings.Contains(readOnly, "this session only") {
		t.Error("a read-only settings panel does not say it cannot save")
	}
}

// TestReadOnlyDockEditorSaysSo checks the same for the dock editor, which can be
// left open on its own after the panel behind it is closed.
func TestReadOnlyDockEditorSaysSo(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 44}
	m.UserConfig = config.DefaultConfig()
	m.OpenDockEditor()

	writable, _, _ := m.renderDockEditor()
	if strings.Contains(writable, "this session only") {
		t.Error("a writable dock editor claims it cannot save")
	}

	m.ConfigReadOnly = true
	readOnly, _, _ := m.renderDockEditor()
	if !strings.Contains(readOnly, "this session only") {
		t.Error("a read-only dock editor does not say it cannot save")
	}
}
