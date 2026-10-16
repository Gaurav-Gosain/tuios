package app

import (
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestSettingsSavesLandInTheOrderTheyWereMade: Bubble Tea runs each save's
// command in a goroutine of its own, so two saves made in a row can run in
// either order. The file must end with the newer one.
//
// The case that lost the newer value: the file holds thick, a change to double,
// and a change back to thick. The newer save, run first, found thick in the
// file and wrote nothing. The older one ran after it and wrote double.
func TestSettingsSavesLandInTheOrderTheyWereMade(t *testing.T) {
	file := useTempConfig(t)
	cfg := config.DefaultConfig()
	cfg.Appearance.BorderStyle = "thick"
	if err := config.WriteConfigFile(cfg, file); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	m := NewOS(OSOptions{UserConfig: cfg})

	m.setOption("appearance.border_style", "double")
	older := m.persistSettings()
	m.setOption("appearance.border_style", "thick")
	newer := m.persistSettings()
	if older == nil || newer == nil {
		t.Fatal("a settings change made no save")
	}
	// The newer command runs first, as the scheduler is free to run it.
	newer()
	older()

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `border_style = "thick"`) && !strings.Contains(string(data), `border_style = 'thick'`) {
		t.Fatalf("the file does not hold the newer save's border style, thick:\n%s", data)
	}
}
