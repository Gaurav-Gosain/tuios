//go:build !slim

package session

import "github.com/Gaurav-Gosain/tuios/internal/config"

// screenshotFormats and screenshotFrames are the screenshot verb's closed
// sets. They are the config registry's own lists rather than copies, so a
// format added to one place cannot be missing from the other.
var screenshotFormats = config.ScreenshotFormats

var screenshotFrames = config.ScreenshotFrames

// knownEventTypes are the event types a subscribe filter can name.
var knownEventTypes = EventTypeNames
