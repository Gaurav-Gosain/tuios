package vis

import "slices"

import "github.com/Gaurav-Gosain/tuios/internal/fuzz"

// The tape shows one cell per action and the cell has one glyph, so the
// alphabet has to collapse to a handful of classes. The grouping is by what the
// action is to a user, because that is what the shot is of: a machine doing to
// the multiplexer the things a person does to it, far too fast.
//
// A class covers a set of Kinds explicitly rather than by a range or a prefix.
// A Kind added to the alphabet and left out of every class would otherwise be
// drawn under whichever letter the fallback happened to be, which is a cell
// claiming an action that did not happen. The test in this package asserts the
// classes partition the alphabet, so adding a Kind breaks the build's tests
// rather than the display's truthfulness.

// Class is one row of the mix histogram and one glyph on the tape.
type Class struct {
	// Letter is the tape glyph. One cell, ASCII, so the tape survives both the
	// ASCII pass and a small recording.
	Letter string
	Name   string
	Kinds  []fuzz.Kind
}

// Holds reports whether k belongs to this class.
func (c Class) Holds(k fuzz.Kind) bool {
	return slices.Contains(c.Kinds, k)
}

// DefaultClasses partitions the action alphabet. Order is the order the mix
// histogram lists them.
func DefaultClasses() []Class {
	return []Class{
		{"k", "key", []fuzz.Kind{fuzz.Key, fuzz.Chord, fuzz.Text}},
		{"m", "mouse", []fuzz.Kind{
			fuzz.MousePress, fuzz.MouseMotion, fuzz.MouseRelease, fuzz.MouseWheel,
		}},
		{"w", "window", []fuzz.Kind{
			fuzz.NewPane, fuzz.ClosePane, fuzz.ZoomPane, fuzz.FocusPane, fuzz.MovePane,
			fuzz.ToggleTiling, fuzz.ToggleShared, fuzz.LayoutMode, fuzz.Rename,
		}},
		{"r", "resize", []fuzz.Kind{fuzz.Resize}},
		{"s", "session", []fuzz.Kind{
			fuzz.SwitchWorkspace, fuzz.SwitchSession, fuzz.Detach, fuzz.Attach,
			fuzz.SecondClient, fuzz.DaemonRestart,
		}},
		{"o", "overlay", []fuzz.Kind{
			fuzz.OpenOverlay, fuzz.CloseOverlay, fuzz.ToggleSidebar,
			fuzz.SidebarCollapse, fuzz.SidebarPosition, fuzz.Setting,
		}},
		{"g", "guest", []fuzz.Kind{
			fuzz.Guest, fuzz.AltScreen, fuzz.Burst, fuzz.Tick,
		}},
	}
}
