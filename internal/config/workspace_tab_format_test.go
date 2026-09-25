package config_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestFormatWorkspaceTab covers appearance.dock_workspace_tab_format, which
// formats each workspace tab's label from {index} and {name} placeholders.
func TestFormatWorkspaceTab(t *testing.T) {
	prev := config.Global.DockWorkspaceTabFormat
	t.Cleanup(func() { config.Global.DockWorkspaceTabFormat = prev })

	tests := []struct {
		name   string
		format string
		label  string
		index  int
		want   string
	}{
		{
			name:   "empty format leaves the name alone",
			format: "",
			label:  "dev",
			index:  2,
			want:   "dev",
		},
		{
			name:   "index and name",
			format: "{index}: {name}",
			label:  "dev",
			index:  2,
			want:   "2: dev",
		},
		{
			name:   "name first, index after",
			format: "{name} ({index})",
			label:  "prod",
			index:  3,
			want:   "prod (3)",
		},
		{
			name:   "index alone",
			format: "{index}",
			label:  "anything",
			index:  5,
			want:   "5",
		},
		{
			name:   "unknown placeholder is passed through",
			format: "{cwd}",
			label:  "dev",
			index:  2,
			want:   "{cwd}",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config.Global.DockWorkspaceTabFormat = tc.format
			if got := config.Global.FormatWorkspaceTab(tc.label, tc.index); got != tc.want {
				t.Errorf("FormatWorkspaceTab(%q, %d) = %q, want %q",
					tc.label, tc.index, got, tc.want)
			}
		})
	}
}
