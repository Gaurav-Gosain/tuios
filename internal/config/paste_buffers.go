package config

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/pastebuf"
)

// PasteBuffersConfig is the [paste_buffers] table: how many yanks tuios keeps
// to paste again, after tmux's buffer-limit. The daemon reads it when it
// starts and when the file changes. A client with no daemon reads it the
// same way.
type PasteBuffersConfig struct {
	// Limit is how many automatic buffers to keep. 0 keeps none, and a yank
	// then goes only to the clipboard. A pointer so an explicit 0 is told
	// apart from a file that never mentions it (default: 20).
	Limit *int `toml:"limit"`
	// MaxKB is how many KiB all buffers hold together. When a new buffer
	// passes it, the oldest go. 0 uses 16384 (16 MiB), and the most it may
	// be is MaxPasteBufferKB.
	MaxKB int `toml:"max_kb"`
}

// MaxPasteBufferKB bounds max_kb: 256 MiB. The buffers are in the daemon's
// memory, and a value past this reads as it.
const MaxPasteBufferKB = 256 << 10

// defaultPasteBuffersConfig is [paste_buffers] as DefaultConfig holds it: the
// values a file that leaves the table out resolves to, written out, so a
// config.toml that repeats them is pruned.
func defaultPasteBuffersConfig() PasteBuffersConfig {
	limit := pastebuf.DefaultLimit
	return PasteBuffersConfig{Limit: &limit, MaxKB: pastebuf.DefaultMaxBytes >> 10}
}

// Resolved returns the count limit and the byte cap the store runs with.
func (c PasteBuffersConfig) Resolved() (limit, maxBytes int) {
	limit = pastebuf.DefaultLimit
	if c.Limit != nil {
		limit = max(0, min(*c.Limit, pastebuf.MaxLimit))
	}
	maxBytes = pastebuf.DefaultMaxBytes
	if c.MaxKB > 0 {
		maxBytes = min(c.MaxKB, MaxPasteBufferKB) << 10
	}
	return limit, maxBytes
}

// validatePasteBuffers warns about [paste_buffers] values past their bounds.
func validatePasteBuffers(cfg *UserConfig, result *ValidationResult) {
	c := cfg.PasteBuffers
	if c.MaxKB < 0 || c.MaxKB > MaxPasteBufferKB {
		_, maxBytes := c.Resolved()
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "paste_buffers", Key: "max_kb",
			Message: fmt.Sprintf("%d is outside 0 to %d (256 MiB); read as %d", c.MaxKB, MaxPasteBufferKB, maxBytes>>10),
		})
	}
	if c.Limit != nil && (*c.Limit < 0 || *c.Limit > pastebuf.MaxLimit) {
		limit, _ := c.Resolved()
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "paste_buffers", Key: "limit",
			Message: fmt.Sprintf("%d is outside 0 to %d; read as %d", *c.Limit, pastebuf.MaxLimit, limit),
		})
	}
}
