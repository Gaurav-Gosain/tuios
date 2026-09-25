package learn

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestConfigDoesNotChangeTheDefaults checks that pinning the tour's looks
// leaves DefaultConfig alone, so the tour cannot leak its screen into a
// native session built in the same process.
func TestConfigDoesNotChangeTheDefaults(t *testing.T) {
	_ = Config()
	d := config.DefaultConfig()
	if d.Appearance.Sidebar.Enabled == nil || !*d.Appearance.Sidebar.Enabled {
		t.Errorf("DefaultConfig has the rail off after Config ran")
	}
	if d.Appearance.DockbarPosition != config.DefaultDockbarPosition {
		t.Errorf("DefaultConfig dock at %q after Config ran", d.Appearance.DockbarPosition)
	}
}

// TestConfigKeepsTheTourLooks pins the screen the lessons describe. The
// shipped defaults moved in v0.8.0 and the tour did not, so each of these is
// the value from before that release, read back through the same path the
// browser build seeds its model with.
func TestConfigKeepsTheTourLooks(t *testing.T) {
	s := config.AppearanceFrom(Config(), config.Overrides{})
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"sidebar enabled", s.SidebarEnabled, false},
		{"sidebar position", s.SidebarPosition, "left"},
		{"sidebar width", s.SidebarWidth, 28},
		{"dockbar position", s.DockbarPosition, "bottom"},
		{"window title position", s.WindowTitlePosition, "bottom"},
		{"zoom size", s.GetZoomSize(), 100},
		{"click to type", s.ClickToType, config.ClickToTypeSingle},
		{"scrollbar style", s.ScrollbarStyle, config.ScrollbarStyleThin},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
			}
		})
	}

	if n := Config().Notifications.Agent.Notify; n == nil || *n {
		t.Errorf("desktop notifications are on in the tour")
	}
}
