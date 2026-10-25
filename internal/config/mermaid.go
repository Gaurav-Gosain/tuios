package config

// MermaidConfig is the [mermaid] section: how tuios mermaid colours a diagram.
type MermaidConfig struct {
	// Theme is "tuios" (the default), which builds the colours from the tuios
	// theme, or a named theme of tuios.dev/sketch, such as flexoki, nord or
	// rose-pine. The --theme flag wins over it.
	Theme string `toml:"theme"`
	// Background is "theme" (the default), which paints a kitty or png
	// picture in the theme's background colour, or "none", which leaves it
	// clear so the diagram sits on the terminal's own background. The
	// --background flag wins over it.
	Background string `toml:"background"`
}
