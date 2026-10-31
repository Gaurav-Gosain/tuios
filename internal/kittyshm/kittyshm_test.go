package kittyshm

import (
	"testing"

	"tuios.dev/vt"
)

// A delete is the one thing here a printed name can cause, so its rule is pinned
// on every platform: the name may be another program's object.
func TestIsFrame(t *testing.T) {
	const page = 16384
	rgba := func(w, h int) *vt.KittyCommand {
		return &vt.KittyCommand{Format: vt.KittyFormatRGBA, Width: w, Height: h}
	}
	tests := []struct {
		name    string
		cmd     *vt.KittyCommand
		size    int64
		regular bool
		owned   bool
		want    bool
	}{
		{"the size rounds up to a page", rgba(50, 50), page, true, true, true},
		{"a whole number of pages", rgba(64, 64), page, true, true, true},
		{"one page too many", rgba(50, 50), 2 * page, true, true, false},
		{"one page too few", rgba(100, 100), page, true, true, false},
		{"another user", rgba(50, 50), page, true, false, false},
		{"not a regular object", rgba(50, 50), page, false, true, false},
		{"S gives the size", &vt.KittyCommand{Format: vt.KittyFormatPNG, Size: 100}, page, true, true, true},
		{"nothing gives the size", &vt.KittyCommand{Format: vt.KittyFormatPNG}, page, true, true, false},
		{"compressed without S", &vt.KittyCommand{Format: vt.KittyFormatRGBA, Width: 1, Height: 1, Compression: vt.KittyCompressionZlib}, page, true, true, false},
		{"no command", nil, page, true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsFrame(tt.cmd, tt.size, tt.regular, tt.owned, page); got != tt.want {
				t.Fatalf("IsFrame = %v, want %v", got, tt.want)
			}
		})
	}
}
