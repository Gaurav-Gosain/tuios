package config

import "github.com/Gaurav-Gosain/tuios/internal/pastebuf"

// PasteBuffersConfig is the [paste_buffers] table: how many yanks tuios keeps
// to paste again, after tmux's buffer-limit. The daemon reads it when it
// starts and when the file changes. A client with no daemon reads it the
// same way.
type PasteBuffersConfig struct {
	// Limit is how many buffers to keep. 0 keeps none, and a yank then goes
	// only to the clipboard. A pointer so an explicit 0 is told apart from a
	// file that never mentions it (default: 20).
	Limit *int `toml:"limit"`
	// MaxKB is how many KiB all buffers hold together. When a new buffer
	// passes it, the oldest go. 0 uses 16384 (16 MiB).
	MaxKB int `toml:"max_kb"`
}

// Resolved returns the count limit and the byte cap the store runs with.
func (c PasteBuffersConfig) Resolved() (limit, maxBytes int) {
	limit = pastebuf.DefaultLimit
	if c.Limit != nil {
		limit = max(0, min(*c.Limit, pastebuf.MaxLimit))
	}
	maxBytes = pastebuf.DefaultMaxBytes
	if c.MaxKB > 0 {
		maxBytes = c.MaxKB << 10
	}
	return limit, maxBytes
}
