package config

import (
	"fmt"
	"strings"
	"time"
)

// Drop preview styles. See AppearanceConfig.DropPreview.
const (
	// DropPreviewFill tints the rectangle a dragged pane would land in and
	// outlines it.
	DropPreviewFill = "fill"
	// DropPreviewOutline draws only the outline.
	DropPreviewOutline = "outline"
	// DropPreviewOff draws nothing. The drop itself still works.
	DropPreviewOff = "off"
)

// DropPreviewModes lists the valid values for appearance.drop_preview.
var DropPreviewModes = []string{DropPreviewFill, DropPreviewOutline, DropPreviewOff}

// DefaultDropPreviewDelay is how long the pointer stays over one drop zone
// before the preview shows, so a pane carried across the screen does not
// flash a zone over every pane it passes.
const DefaultDropPreviewDelay = 120 * time.Millisecond

// ParseDropPreviewDelay reads appearance.drop_preview_delay: a Go duration, or
// 0 to show the preview at once. Empty is the default. ok is false for
// anything else, which reads as the default.
func ParseDropPreviewDelay(v string) (d time.Duration, ok bool) {
	v = strings.TrimSpace(v)
	switch v {
	case "":
		return DefaultDropPreviewDelay, true
	case "0":
		return 0, true
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return DefaultDropPreviewDelay, false
	}
	return d, true
}

// checkDropPreviewDelay is the error for a drop_preview_delay value that is
// not a duration.
func checkDropPreviewDelay(v string) error {
	if _, ok := ParseDropPreviewDelay(v); !ok {
		return fmt.Errorf("%q is not a duration such as 120ms, or 0", v)
	}
	return nil
}
