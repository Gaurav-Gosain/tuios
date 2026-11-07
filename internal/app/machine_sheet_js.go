//go:build js

package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The browser build has no ssh and no engine to set up a machine with, so
// the add sheet never opens there.

const overlayKindMachineSheet = "machinesheet"

type machineSheet struct{}

type machineSheetMsg interface{ machineSheet() }

// MachineSheetOpen is always false in the browser build.
func (m *OS) MachineSheetOpen() bool { return false }

// OpenMachineSheet says the browser build cannot add a machine.
func (m *OS) OpenMachineSheet(string) tea.Cmd {
	m.ShowNotification("This session cannot add a machine.", "info", m.Settings.NotificationDuration)
	return nil
}

// MachineSheetKey does nothing in the browser build.
func (m *OS) MachineSheetKey(tea.KeyPressMsg) tea.Cmd { return nil }

// MachineSheetType does nothing in the browser build.
func (m *OS) MachineSheetType(string) {}

func (m *OS) closeMachineSheet()                             {}
func (m *OS) machineSheetAfter()                             {}
func (m *OS) applyMachineSheetMsg(machineSheetMsg) tea.Cmd   { return nil }
func (m *OS) renderMachineSheet() (string, overlay.Geometry) { return "", overlay.Geometry{} }
