package harness

import (
	"os"
	"strings"
	"testing"
)

// BenchmarkClassify prices one screen scan with the bundled rules, on the
// screens the daemon sees most: a working turn and an idle prompt. The scan
// runs on every settle of every agent pane, so its cost times the number of
// panes is the budget the manifest engine spends.
func BenchmarkClassify(b *testing.B) {
	r, _ := Load(os.Getenv("TUIOS_BENCH_MANIFESTS"))
	for _, tc := range []struct {
		harness string
		tail    []string
	}{
		{"claude-code", []string{
			"⏺ Reading internal/session/agent_state.go",
			"✻ Thinking… (12s · ↑ 1.2k tokens)",
			strings.Repeat("─", 100),
			"❯",
			strings.Repeat("─", 100),
			"  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt",
		}},
		{"claude-code", []string{
			"⏺ Done.",
			strings.Repeat("─", 100),
			"❯",
			strings.Repeat("─", 100),
			"  ? for shortcuts",
		}},
		{"qwen", []string{
			"⠹ Reading files (12s · esc to cancel)",
			"╭──────────────────────────────────────────╮",
			"│ >   Type your message or @path/to/file   │",
			"╰──────────────────────────────────────────╯",
		}},
		{"kiro", []string{
			"  fs_write src/main.go",
			"> Allow",
			"  Always allow",
			"  Deny",
			"  Always deny",
			"esc to close · ↑↓ to navigate · enter to select · tab to edit",
		}},
	} {
		b.Run(tc.harness, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r.Classify(tc.harness, tc.tail)
			}
		})
	}
}
