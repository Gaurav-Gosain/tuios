package config

import "time"

// Motion levels. appearance.motion takes one of these, and it replaced the
// on/off appearance.animations_enabled.
//
// Two levels of motion rather than one switch, because the motion tuios has is
// of two kinds. A window sliding to its tile says where the window went, and
// that is information. A panel fading in or a working agent's row shimmering
// says nothing a static frame does not, and some people find it distracting.
// basic keeps the first kind and drops the second.
const (
	// MotionNone draws every change in one frame: no slides, no fades, no
	// shimmer, and confetti is a still sparkle.
	MotionNone = "none"
	// MotionBasic keeps the motion that carries information: windows slide
	// between positions and a zoom grows from its tile.
	MotionBasic = "basic"
	// MotionFull adds the decorative motion: an overlay fades in, a working
	// agent's row shimmers, and confetti flies. It is the default.
	MotionFull = "full"
)

// MotionLevels is what appearance.motion accepts, from least motion to most.
var MotionLevels = []string{MotionNone, MotionBasic, MotionFull}

// motionRank orders the levels. An unknown or empty level ranks as none, so a
// Settings built by hand, which carries the empty string, animates nothing.
func motionRank(level string) int {
	switch level {
	case MotionBasic:
		return 1
	case MotionFull:
		return 2
	}
	return 0
}

// MotionAllows reports whether motion of the given level may run now: the
// configured level reaches it and nothing has suppressed animations for the
// moment (a remote command in flight, a tape being played).
func (s *Settings) MotionAllows(level string) bool {
	if s.AnimationsSuppressed {
		return false
	}
	return motionRank(s.Motion) >= motionRank(level)
}

// AnimationsOn reports whether any motion is configured. It is what the
// animation toggle and the tape executor's status read.
func (s *Settings) AnimationsOn() bool { return motionRank(s.Motion) > 0 }

// SetAnimationsOn is the on/off switch over the levels: off is none, and on is
// full, the level tuios ships with. A caller that wants basic sets Motion.
func (s *Settings) SetAnimationsOn(on bool) {
	if on {
		s.Motion = MotionFull
	} else {
		s.Motion = MotionNone
	}
}

// Overlay fade-in timing. The fade is short enough that it never makes anybody
// wait, and long enough to be seen as a fade rather than a flicker: at 60
// frames a second it is seven or eight frames.
const (
	// OverlayFadeDuration is how long an overlay takes to come up.
	OverlayFadeDuration = 120 * time.Millisecond
	// OverlayFadeFrame is the interval between the fade's frames.
	OverlayFadeFrame = time.Second / 60
)

// ShimmerFrame is the interval between frames of the working-row shimmer: 15
// frames a second. The band moves a column or less per frame at that rate, so
// more frames would draw the same picture again.
const ShimmerFrame = time.Second / 15

// Modal dim. appearance.modal_dim is the percent the screen behind a modal
// overlay is darkened by; zero turns it off.
const (
	// ModalDimDefault is how far the screen behind a modal is darkened when
	// nothing sets it. Enough that the panel is plainly the thing in front,
	// not so much that the context behind it cannot be read.
	ModalDimDefault = 30
	// ModalDimMax caps it for the reason DimUnfocusedMax does: past this the
	// screen behind is gone rather than dimmed.
	ModalDimMax = 90
)

// migrateAnimationsEnabled folds the old on/off appearance.animations_enabled
// into appearance.motion, and clears it.
//
// false becomes none, which is what it meant: nothing moves. true becomes
// nothing at all, because true was the default and the default level is full;
// writing full into the file would turn a key the user never chose into one
// they had. A config that sets both keeps its motion.
func migrateAnimationsEnabled(a *AppearanceConfig) {
	if a.AnimationsEnabled == nil {
		return
	}
	if a.Motion == "" && !*a.AnimationsEnabled {
		a.Motion = MotionNone
	}
	a.AnimationsEnabled = nil
}
