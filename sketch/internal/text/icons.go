// Ported from pele src/text/icons.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele keeps the resolver of the render in progress in module state. The port
// carries it on the measurer instead (WithIcons), so that renders can run at
// the same time.

package text

// IconResolver gives the SVG markup of an icon by name, or "" for none.
type IconResolver func(name string) string

type iconState struct {
	resolver IconResolver
	found    map[string]string
}

// hosted is a measurer that carries what the host gave one render: its icons
// and its math resolver.
type hosted struct {
	TextMeasurer
	icons *iconState
	math  *mathState
}

func hostOf(m TextMeasurer) hosted {
	if h, ok := m.(hosted); ok {
		return h
	}
	return hosted{TextMeasurer: m}
}

// WithIcons gives a measurer that also carries the host's icons for one render.
// A label leaves room for an icon only when the host has one by that name, and
// layout has to know that before anything is drawn. It starts a render: a math
// resolver the measurer carried is dropped.
func WithIcons(m TextMeasurer, icons IconResolver) TextMeasurer {
	if h, ok := m.(hosted); ok {
		m = h.TextMeasurer
	}
	return hosted{TextMeasurer: m, icons: &iconState{resolver: icons, found: map[string]string{}}}
}

// IconMarkup is the host's markup for an icon, through the resolver the measurer carries.
func IconMarkup(m TextMeasurer, name string) string {
	h, ok := m.(hosted)
	if !ok || h.icons == nil || h.icons.resolver == nil {
		return ""
	}
	markup, ok := h.icons.found[name]
	if !ok {
		markup = h.icons.resolver(name)
		h.icons.found[name] = markup
	}
	return markup
}
