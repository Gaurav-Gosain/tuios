package config

import (
	"slices"
	"time"
)

// MotionAspect names one motion tuios has, so that each can be switched and
// timed on its own under [motion]. The global appearance.motion level and
// appearance.animation_speed stay on top: a level of none stops every aspect,
// and the speed divides every length.
type MotionAspect int

const (
	// AspectWorkspace is the switch between workspaces.
	AspectWorkspace MotionAspect = iota
	// AspectLayout is every pane that moves to a new place in a layout: a pane
	// that opens, closes, splits, swaps, snaps, minimizes or restores.
	AspectLayout
	// AspectZoom is a pane growing to the zoom box and back.
	AspectZoom
	// AspectOverlays is the fade of a palette or a dialog as it opens.
	AspectOverlays
	// AspectSidebar is the shimmer on a working agent row of the rail.
	AspectSidebar
	// AspectScroll is the slide of the scrolling layout.
	AspectScroll
	// AspectCopy is the sweep across the cells a copy took.
	AspectCopy
	// AspectConfetti is the confetti burst.
	AspectConfetti
	numAspects
)

// aspectKeys are the [motion] keys, in aspect order.
var aspectKeys = [numAspects]string{"workspace", "layout", "zoom", "overlays", "sidebar", "scroll", "copy", "confetti"}

// aspectLevels is the least appearance.motion level each aspect runs at.
// Decoration needs full. Motion that says where something went needs basic.
var aspectLevels = [numAspects]string{MotionBasic, MotionBasic, MotionBasic, MotionFull, MotionFull, MotionBasic, MotionBasic, MotionFull}

// AspectSetting is one aspect as configured.
type AspectSetting struct {
	// Off stops the aspect: its change draws in one frame.
	Off bool
	// HasMs says Ms is set. Unset, the aspect runs for its own built-in length.
	HasMs bool
	// Ms is the length in milliseconds at animation speed 100. Zero is a cut.
	Ms int
}

// Motion switch values. Every [motion] key but workspace takes one of these.
const (
	MotionSwitchOn  = "on"
	MotionSwitchOff = "off"
)

// MotionSwitches is what the on/off [motion] keys accept.
var MotionSwitches = []string{MotionSwitchOn, MotionSwitchOff}

// MotionMsMax bounds every [motion] <name>_ms key, in milliseconds.
const MotionMsMax = 1000

// MotionOn is the one check every motion start goes through: the aspect is on,
// the global level reaches it, and nothing has suppressed animation for the
// moment. A Settings built by hand has every aspect on.
func (s *Settings) MotionOn(a MotionAspect) bool {
	return !s.Aspects[a].Off && s.MotionAllows(aspectLevels[a])
}

// MotionLength is how long the aspect runs: zero when it may not run, else its
// configured length or base, divided by animation_speed.
func (s *Settings) MotionLength(a MotionAspect, base time.Duration) time.Duration {
	if !s.MotionOn(a) {
		return 0
	}
	if as := s.Aspects[a]; as.HasMs {
		base = time.Duration(min(max(as.Ms, 0), MotionMsMax)) * time.Millisecond
	}
	return s.scaleAnimation(base)
}

// MotionConfig is the [motion] table. An empty value is unset and reads as the
// default.
type MotionConfig struct {
	Workspace   string `toml:"workspace"` // slide, fade or none (default: slide)
	WorkspaceMs *int   `toml:"workspace_ms"`
	Layout      string `toml:"layout"` // on or off (default: on)
	LayoutMs    *int   `toml:"layout_ms"`
	Zoom        string `toml:"zoom"`
	ZoomMs      *int   `toml:"zoom_ms"`
	Overlays    string `toml:"overlays"`
	OverlaysMs  *int   `toml:"overlays_ms"`
	Sidebar     string `toml:"sidebar"`
	Scroll      string `toml:"scroll"`
	ScrollMs    *int   `toml:"scroll_ms"`
	Copy        string `toml:"copy"`
	Confetti    string `toml:"confetti"`
}

// fields returns the switch and the length of each aspect, in aspect order. A
// nil length is an aspect with none.
func (m *MotionConfig) fields() (sw [numAspects]*string, ms [numAspects]**int) {
	sw = [numAspects]*string{&m.Workspace, &m.Layout, &m.Zoom, &m.Overlays, &m.Sidebar, &m.Scroll, &m.Copy, &m.Confetti}
	ms = [numAspects]**int{&m.WorkspaceMs, &m.LayoutMs, &m.ZoomMs, &m.OverlaysMs, nil, &m.ScrollMs, nil, nil}
	return
}

// foldOldMotionKeys moves the keys that [motion] replaced into it, and clears
// them: appearance.workspace_transition, workspace_transition_ms and
// zoom_animation. A [motion] key that is set wins.
func foldOldMotionKeys(cfg *UserConfig) {
	a, m := &cfg.Appearance, &cfg.Motion
	if a.WorkspaceTransition != "" && m.Workspace == "" {
		m.Workspace = a.WorkspaceTransition
	}
	if a.WorkspaceTransitionMs != nil && m.WorkspaceMs == nil {
		m.WorkspaceMs = a.WorkspaceTransitionMs
	}
	if a.ZoomAnimation != nil && m.Zoom == "" {
		m.Zoom = MotionSwitchOn
		if !*a.ZoomAnimation {
			m.Zoom = MotionSwitchOff
		}
	}
	a.WorkspaceTransition, a.WorkspaceTransitionMs, a.ZoomAnimation = "", nil, nil
}

// fillMissingMotion folds the old keys, then replaces a bad or missing value
// with the default.
func fillMissingMotion(cfg *UserConfig) {
	foldOldMotionKeys(cfg)
	sw, ms := cfg.Motion.fields()
	for i := range sw {
		ok := MotionSwitches
		if MotionAspect(i) == AspectWorkspace {
			ok = WorkspaceTransitions
		}
		if !slices.Contains(ok, *sw[i]) {
			*sw[i] = ok[0]
		}
		if ms[i] != nil && *ms[i] != nil {
			*ms[i] = new(min(max(**ms[i], 0), MotionMsMax))
		}
	}
}

// applyMotionConfig copies [motion] into the settings. The old appearance keys
// still count when they are set, which only a runtime set-config does after a
// load has cleared them.
func applyMotionConfig(cfg *UserConfig, s *Settings) {
	c := cfg.Motion
	a := &cfg.Appearance
	if a.WorkspaceTransition != "" {
		c.Workspace = a.WorkspaceTransition
	}
	if a.WorkspaceTransitionMs != nil {
		c.WorkspaceMs = a.WorkspaceTransitionMs
	}
	if a.ZoomAnimation != nil {
		c.Zoom = MotionSwitchOn
		if !*a.ZoomAnimation {
			c.Zoom = MotionSwitchOff
		}
	}
	sw, ms := c.fields()
	for i := range sw {
		as := AspectSetting{Off: *sw[i] == MotionSwitchOff || *sw[i] == WorkspaceTransitionNone}
		if ms[i] != nil && *ms[i] != nil {
			as.HasMs, as.Ms = true, min(max(**ms[i], 0), MotionMsMax)
		}
		s.Aspects[i] = as
	}
	s.WorkspaceTransition = WorkspaceTransitionSlide
	if c.Workspace == WorkspaceTransitionFade {
		s.WorkspaceTransition = WorkspaceTransitionFade
	}
	if !s.Aspects[AspectWorkspace].HasMs {
		s.Aspects[AspectWorkspace] = AspectSetting{Off: s.Aspects[AspectWorkspace].Off, HasMs: true, Ms: WorkspaceTransitionMsDefault}
	}
}
