// Ported from pele src/diagrams/architecture/icons.ts at 8aff183be57021b533f319a29341a529be934b08.

package architecture

// glyph: outlines for the icon names Mermaid's architecture diagrams know without an icon pack, drawn on
// a 24 by 24 grid. Any other name is left to the host's icon resolver.
// It is a switch rather than a map, which would allocate at init.
func glyph(name string) (string, bool) {
	switch name {
	case "database":
		return "M4 6a8 3 0 1 0 16 0a8 3 0 1 0-16 0M4 6v12a8 3 0 0 0 16 0V6M4 12a8 3 0 0 0 16 0", true
	case "server":
		return "M3 4h18v7H3zM3 13h18v7H3zM7 7.5h.01M7 16.5h.01M11 7.5h6M11 16.5h6", true
	case "disk":
		return "M5 3h14v18H5zM12 6.5a4 4 0 1 0 0 8a4 4 0 1 0 0-8M12 10.5h.01M8 18h3", true
	case "internet":
		return "M3 12a9 9 0 1 0 18 0a9 9 0 1 0-18 0M3 12h18M12 3a13 13 0 0 0 0 18M12 3a13 13 0 0 1 0 18", true
	case "cloud":
		return "M7 18a4 4 0 0 1-.5-7.97A6 6 0 0 1 18 9.5a4.25 4.25 0 0 1-.5 8.5z", true
	}
	return "", false
}

func builtinIcon(name string) string {
	d, ok := glyph(name)
	if !ok {
		return ""
	}
	return `<path d="` + d + `" stroke="var(--_fg)" stroke-width="1.5"/>`
}
