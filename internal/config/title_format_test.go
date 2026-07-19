package config_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestFormatWindowTitle covers appearance.window_title_format, which was parsed
// into the config struct and never read by anything: setting it had no effect
// on any window title.
func TestFormatWindowTitle(t *testing.T) {
	prev := config.WindowTitleFormat
	t.Cleanup(func() { config.WindowTitleFormat = prev })

	tests := []struct {
		name   string
		format string
		title  string
		index  int
		cwd    string
		want   string
	}{
		{
			name:   "empty format leaves the title alone",
			format: "",
			title:  "nvim", index: 2, cwd: "/src",
			want: "nvim",
		},
		{
			name:   "index and title",
			format: "{index}: {title}",
			title:  "nvim", index: 2, cwd: "/src",
			want: "2: nvim",
		},
		{
			name:   "cwd only",
			format: "{cwd}",
			title:  "nvim", index: 2, cwd: "/home/me/src",
			want: "/home/me/src",
		},
		{
			name:   "every placeholder at once",
			format: "[{index}] {title} ({cwd})",
			title:  "nvim", index: 7, cwd: "/tmp",
			want: "[7] nvim (/tmp)",
		},
		{
			name:   "repeated placeholder",
			format: "{index}{index}",
			title:  "", index: 3, cwd: "",
			want: "33",
		},
		{
			name:   "unreadable cwd expands to nothing rather than a stray root",
			format: "{title}:{cwd}",
			title:  "sh", index: 1, cwd: "",
			want: "sh:",
		},
		{
			name:   "literal text without placeholders is used verbatim",
			format: "shell",
			title:  "nvim", index: 1, cwd: "/src",
			want: "shell",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config.WindowTitleFormat = tc.format
			if got := config.FormatWindowTitle(tc.title, tc.index, tc.cwd); got != tc.want {
				t.Errorf("FormatWindowTitle(%q, %d, %q) = %q, want %q",
					tc.title, tc.index, tc.cwd, got, tc.want)
			}
		})
	}
}

// TestWindowTitleFormatIsAppliedFromConfig pins the wiring rather than the
// formatting: the option is only honest if loading a config actually reaches
// the global the renderer reads.
func TestWindowTitleFormatIsAppliedFromConfig(t *testing.T) {
	prev := config.WindowTitleFormat
	t.Cleanup(func() { config.WindowTitleFormat = prev })

	cfg := config.DefaultConfig()
	cfg.Appearance.WindowTitleFormat = "{index}. {title}"
	config.ApplyAppearanceConfig(cfg)

	if config.WindowTitleFormat != "{index}. {title}" {
		t.Fatalf("WindowTitleFormat = %q, want the configured format", config.WindowTitleFormat)
	}

	// Clearing it in the config must clear the global too, so a hot reload that
	// removes the option goes back to plain titles.
	cfg.Appearance.WindowTitleFormat = ""
	config.ApplyAppearanceConfig(cfg)
	if config.WindowTitleFormat != "" {
		t.Errorf("WindowTitleFormat = %q, want it cleared", config.WindowTitleFormat)
	}
}
