package config

// SpotlightConfig is the [spotlight] section: a beam that lights one part of
// the screen and carries everything outside it toward the ground.
//
// It is a presentation aid, not a mode. Nothing about it is session state: a
// second client attached to the same session sees its own screen unchanged,
// the same way it does for the showkeys overlay and the theme. What lives here
// is the shape of the beam and where it follows, so a person who wants it can
// keep it across restarts.
//
// Enabled is a pointer so that turning the beam off in the settings page
// survives a reload rather than reading as "unset" and snapping back on.
type SpotlightConfig struct {
	Enabled *bool  `toml:"enabled"` // draw the beam from startup (default: false)
	Follow  string `toml:"follow"`  // what the beam is anchored to (default: cursor)
	Radius  int    `toml:"radius"`  // half the beam's height, in rows (default: 10)
	Dim     int    `toml:"dim"`     // how far an unlit cell goes toward the ground (default: 60)
	Edge    string `toml:"edge"`    // hard cut or soft rim (default: hard)
}

// What the beam follows.
//
// Cursor is the default because it costs nothing: the position is the one
// getRealCursor already resolves for the hardware cursor, and it moves only on
// frames that are being composed anyway. Mouse asks for a frame per pointer
// move, which the motion throttle then caps at the frame rate.
//
// The two never mix. A rule that picked whichever moved last would put two
// sources on one beam and make the screen jump for a reason the user cannot
// see.
const (
	SpotlightFollowCursor = "cursor"
	SpotlightFollowMouse  = "mouse"
)

// How the beam ends.
//
// Hard cuts at the radius. Soft fades over the outer part of it, which reads
// more like a light and costs about three times the bytes each time the beam
// moves: the rim is a ring of sixteen colour steps, and every step is a style
// change the terminal has to be told about. Measured at radius 10 on a
// nine-pane 207x55 screen, one cell of movement is 1.8 KB hard and 4.9 KB
// soft. Hard is the default for that reason, and a local session can afford
// soft.
const (
	SpotlightEdgeHard = "hard"
	SpotlightEdgeSoft = "soft"
)

// Spotlight defaults and bounds, one source for DefaultConfig, the option
// registry and the accessors.
const (
	SpotlightDefaultRadius = 10
	SpotlightMinRadius     = 2
	SpotlightMaxRadius     = 200
	SpotlightDefaultDim    = 60
	SpotlightMinDim        = 10
	SpotlightMaxDim        = 95
	// SpotlightFalloff is how much of the radius is soft rim, as a fraction.
	// It is the screen saver's own BeamFalloff, because the two draw the same
	// light and a beam that faded differently in the two places would read as
	// two features.
	SpotlightFalloff = 0.3
	// SpotlightLevels is how many steps the rim is quantised to. The rim is a
	// continuous ramp, and every distinct colour on it is a style run of its
	// own in the frame, so the step count is what bounds the bytes a moving
	// beam puts on the wire.
	SpotlightLevels = 16
)

// SpotlightFollowModes and SpotlightEdges are what the two enum options accept,
// shared by the registry, the validator and the settings page so one spelling
// serves all three.
var (
	SpotlightFollowModes = []string{SpotlightFollowCursor, SpotlightFollowMouse}
	SpotlightEdges       = []string{SpotlightEdgeHard, SpotlightEdgeSoft}
)

// defaultSpotlightConfig returns the section DefaultConfig carries.
func defaultSpotlightConfig() SpotlightConfig {
	return SpotlightConfig{
		Follow: SpotlightFollowCursor,
		Radius: SpotlightDefaultRadius,
		Dim:    SpotlightDefaultDim,
		Edge:   SpotlightEdgeHard,
	}
}

// fillMissingSpotlight fills empty strings and out-of-range numbers with the
// defaults. The pointer field stays nil on purpose: IsEnabled reads nil as
// "off", which is exactly what an absent key means.
func fillMissingSpotlight(cfg, defaultCfg *UserConfig) {
	s, d := &cfg.Spotlight, &defaultCfg.Spotlight
	if s.Follow == "" {
		s.Follow = d.Follow
	}
	if s.Edge == "" {
		s.Edge = d.Edge
	}
	if s.Radius <= 0 {
		s.Radius = d.Radius
	}
	if s.Dim <= 0 {
		s.Dim = d.Dim
	}
	s.Radius = min(max(s.Radius, SpotlightMinRadius), SpotlightMaxRadius)
	s.Dim = min(max(s.Dim, SpotlightMinDim), SpotlightMaxDim)
}

// IsEnabled reports whether the beam is drawn from startup.
func (s SpotlightConfig) IsEnabled() bool { return s.Enabled != nil && *s.Enabled }

// FollowMode is what the beam is anchored to, defaulting to the cursor.
func (s SpotlightConfig) FollowMode() string {
	if s.Follow == SpotlightFollowMouse {
		return SpotlightFollowMouse
	}
	return SpotlightFollowCursor
}

// RadiusRows is the effective radius in rows.
func (s SpotlightConfig) RadiusRows() int {
	if s.Radius <= 0 {
		return SpotlightDefaultRadius
	}
	return min(max(s.Radius, SpotlightMinRadius), SpotlightMaxRadius)
}

// DimPercent is how far an unlit cell is carried toward the ground.
func (s SpotlightConfig) DimPercent() int {
	if s.Dim <= 0 {
		return SpotlightDefaultDim
	}
	return min(max(s.Dim, SpotlightMinDim), SpotlightMaxDim)
}

// EdgeStyle is the rim style, defaulting to hard.
func (s SpotlightConfig) EdgeStyle() string {
	if s.Edge == SpotlightEdgeSoft {
		return SpotlightEdgeSoft
	}
	return SpotlightEdgeHard
}
