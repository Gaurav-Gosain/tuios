package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestLayoutModeSurvivesReattach is the bug this field exists for. The BSP tree,
// the master ratio and the tiling scheme were all carried in session state; the
// mode that selects between the layouts reading them was not, so a scrolling
// session came back as a BSP one and the user's layout silently changed.
func TestLayoutModeSurvivesReattach(t *testing.T) {
	for _, tc := range []struct {
		name        string
		apply       func(*OS)
		wantMode    string
		wantScroll  bool
		wantBSPFlag bool
	}{
		{"scrolling", func(m *OS) { m.UseScrollingLayout, m.UseBSPLayout = true, false }, LayoutModeScrolling, true, false},
		{"bsp", func(m *OS) { m.UseScrollingLayout, m.UseBSPLayout = false, true }, LayoutModeBSP, false, true},
		{"master-stack", func(m *OS) { m.UseScrollingLayout, m.UseBSPLayout = false, false }, LayoutModeMasterStack, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := NewOS(OSOptions{})
			src.Width, src.Height = 100, 40
			src.AutoTiling = true
			tc.apply(src)

			state := src.BuildSessionState()
			if state.LayoutMode != tc.wantMode {
				t.Fatalf("BuildSessionState LayoutMode = %q, want %q", state.LayoutMode, tc.wantMode)
			}

			// A fresh client starts on the BSP default, which is exactly why the
			// mode has to arrive with the state rather than be assumed.
			dst := NewOS(OSOptions{})
			dst.Width, dst.Height = 100, 40
			if err := dst.RestoreFromState(state); err != nil {
				t.Fatalf("RestoreFromState: %v", err)
			}
			if dst.UseScrollingLayout != tc.wantScroll || dst.UseBSPLayout != tc.wantBSPFlag {
				t.Fatalf("after restore scrolling=%v bsp=%v, want scrolling=%v bsp=%v",
					dst.UseScrollingLayout, dst.UseBSPLayout, tc.wantScroll, tc.wantBSPFlag)
			}
			if got := dst.LayoutModeName(); got != tc.wantMode {
				t.Fatalf("LayoutModeName = %q, want %q", got, tc.wantMode)
			}
		})
	}
}

// TestLayoutModeArrivesOnStateSync covers the live path: a peer client or the
// daemon pushing state mid-session, not just reattach.
func TestLayoutModeArrivesOnStateSync(t *testing.T) {
	m := NewOS(OSOptions{})
	m.Width, m.Height = 100, 40
	m.AutoTiling = true
	m.UseBSPLayout = true

	m.ApplyStateSync(&session.SessionState{
		Name: "s", CurrentWorkspace: 1, AutoTiling: true,
		LayoutMode: LayoutModeScrolling,
	})

	if !m.UseScrollingLayout || m.UseBSPLayout {
		t.Fatalf("sync did not apply scrolling mode: scrolling=%v bsp=%v", m.UseScrollingLayout, m.UseBSPLayout)
	}
}

// TestUnstatedLayoutModeLeavesTheClientAlone is what makes the field additive.
// State written before it existed, and any client that does not set it, must not
// reset this client's layout to a default nobody chose.
func TestUnstatedLayoutModeLeavesTheClientAlone(t *testing.T) {
	m := NewOS(OSOptions{})
	m.Width, m.Height = 100, 40
	m.AutoTiling = true
	m.UseScrollingLayout, m.UseBSPLayout = true, false

	m.ApplyStateSync(&session.SessionState{Name: "s", CurrentWorkspace: 1, AutoTiling: true})

	if !m.UseScrollingLayout {
		t.Fatal("a sync with no layout_mode reset the client's layout mode")
	}
}
