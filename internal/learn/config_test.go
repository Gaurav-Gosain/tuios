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
