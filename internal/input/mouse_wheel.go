package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	uv "github.com/charmbracelet/ultraviolet"
)

func wheelIsHorizontal(b tea.MouseButton) bool {
	return b == tea.MouseWheelLeft || b == tea.MouseWheelRight
}

func handleMouseWheel(msg tea.MouseWheelMsg, o *app.OS) (*app.OS, tea.Cmd) {
	horizontal := wheelIsHorizontal(msg.Button)

	if o.PathPickerOpen() {
		wm := msg.Mouse()
		if !horizontal {
			o.PathPickerWheel(wm.X, wm.Y, msg.Button == tea.MouseWheelUp)
		}
		return o, nil
	}

	if wm := msg.Mouse(); o.QuickLookContains(wm.X, wm.Y) {
		if !horizontal {
			o.QuickLookWheel(wm.X, wm.Y, msg.Button == tea.MouseWheelUp)
		}
		return o, nil
	}

	if wm := msg.Mouse(); o.CockpitCodeWheel(wm.X, wm.Y, msg.Button, wm.Mod&tea.ModShift != 0, time.Now()) {
		return o, nil
	}

	if o.OverlayActive() {
		wm := msg.Mouse()
		if horizontal {
			return o, nil
		}
		if o.OverlayMouseWheel(wm.X, wm.Y, msg.Button == tea.MouseWheelUp) {
			return o, o.LauncherIconWork()
		}
	}

	if wm := msg.Mouse(); !o.OverlayActive() {
		if w := o.SidebarSessionPaneAt(wm.X, wm.Y); w != nil {
			if !horizontal {
				if paneTakesMouse(o, w) {
					if tx, ty, inside := w.ScreenToTerminal(wm.X, wm.Y); inside {
						event := uv.MouseWheelEvent{X: tx, Y: ty, Button: uv.MouseButton(wm.Button), Mod: uv.KeyMod(wm.Mod)}
						for range max(o.Settings.ScrollLines, 1) {
							sendMouseToWindow(o, w, event)
						}
						return o, nil
					}
				}
				switch wm.Button {
				case tea.MouseWheelUp:
					if !w.InCopyMode() && w.Terminal != nil && !w.IsAltScreen() && w.ScrollbackLen() > 0 {
						w.EnterCopyModeImplicit()
					}
					if w.InCopyMode() {
						scrollCopyModeUp(w, &o.Settings)
					}
				case tea.MouseWheelDown:
					if w.InCopyMode() {
						scrollCopyModeDown(w, &o.Settings)
					}
				}
				o.TouchRailPane(w)
			}
			return o, nil
		}
	}

	if o.SidebarActive() {
		wm := msg.Mouse()
		if horizontal {
			if o.SidebarBandContains(wm.X, wm.Y) {
				return o, nil
			}
		} else if o.SidebarWheel(wm.X, wm.Y, msg.Button == tea.MouseWheelUp) {
			return o, nil
		}
	}

	if wm := msg.Mouse(); pipOwns(wm.X, wm.Y, o) {
		return o, nil
	}

	if o.ShowLogs {
		switch msg.Button {
		case tea.MouseWheelUp:
			o.LogViewerMove(-1)
		case tea.MouseWheelDown:
			o.LogViewerMove(1)
		}
		return o, nil
	}

	if o.AutoTiling && o.UseScrollingLayout {
		mouse := msg.Mouse()
		if mouse.Mod&(tea.ModAlt|tea.ModShift) != 0 {
			if !o.WheelAxisAccepts(horizontal, time.Now()) {
				return o, nil
			}
			dir := 1
			if o.Settings.NiriReverseScroll {
				dir = -1
			}
			step := 0
			switch msg.Button {
			case tea.MouseWheelUp:
				step = -1 * dir
			case tea.MouseWheelDown:
				step = 1 * dir
			case tea.MouseWheelLeft:
				step = -1 * dir
			case tea.MouseWheelRight:
				step = 1 * dir
			}
			o.ScrollingScrollViewport(step)
			return o, o.ScheduleStripSnap(step)
		}
	}

	if o.Mode == app.TerminalMode {
		focusedWindow := o.GetFocusedWindow()
		if paneTakesMouse(o, focusedWindow) {
			mouse := msg.Mouse()
			termX, termY, inContent := focusedWindow.ScreenToTerminal(mouse.X, mouse.Y)
			if inContent {
				adjustedMouse := uv.MouseWheelEvent{
					X:      termX,
					Y:      termY,
					Button: uv.MouseButton(mouse.Button),
					Mod:    uv.KeyMod(mouse.Mod),
				}
				reps := max(o.Settings.ScrollLines, 1)
				for range reps {
					sendMouseToWindow(o, focusedWindow, adjustedMouse)
				}
				return o, nil
			}
		}
	}

	if o.Mode == app.TerminalMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			switch msg.Button {
			case tea.MouseWheelUp:
				if focusedWindow.InCopyMode() {
					scrollCopyModeUp(focusedWindow, &o.Settings)
				} else if o.Mode == app.TerminalMode && focusedWindow.Terminal != nil && !paneTakesMouse(o, focusedWindow) && !focusedWindow.IsAltScreen() && focusedWindow.ScrollbackLen() > 0 {
					focusedWindow.EnterCopyModeImplicit()
					scrollCopyModeUp(focusedWindow, &o.Settings)
				}
				return o, nil
			case tea.MouseWheelDown:
				if focusedWindow.InCopyMode() {
					scrollCopyModeDown(focusedWindow, &o.Settings)
					leaveCopyModeAtBottom(focusedWindow)
				}
				return o, nil
			}
		}
	}

	if o.Mode == app.WindowManagementMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.Terminal != nil && !focusedWindow.IsAltScreen() {
			switch msg.Button {
			case tea.MouseWheelUp:
				if focusedWindow.InCopyMode() {
					scrollCopyModeUp(focusedWindow, &o.Settings)
				} else if focusedWindow.ScrollbackLen() > 0 {
					focusedWindow.EnterCopyModeImplicit()
					scrollCopyModeUp(focusedWindow, &o.Settings)
				}
			case tea.MouseWheelDown:
				if focusedWindow.InCopyMode() {
					scrollCopyModeDown(focusedWindow, &o.Settings)
					leaveCopyModeAtBottom(focusedWindow)
				}
			}
		}
	}

	return o, nil
}

func handleStripTouch(msg app.StripTouchMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if !o.AutoTiling || !o.UseScrollingLayout || o.OverlayActive() || o.ShowLogs {
		return o, nil
	}
	if o.SidebarSessionPaneAt(msg.X, msg.Y) != nil || (o.SidebarActive() && o.SidebarBandContains(msg.X, msg.Y)) || pipOwns(msg.X, msg.Y, o) {
		return o, nil
	}
	switch msg.Kind {
	case app.StripTouchLift:
		o.SnapStripNow()
	case app.StripTouchLeft:
		o.ScrollingScrollViewportCells(-msg.Cells)
		o.CancelStripSnap()
	case app.StripTouchRight:
		o.ScrollingScrollViewportCells(msg.Cells)
		o.CancelStripSnap()
	}
	return o, nil
}
