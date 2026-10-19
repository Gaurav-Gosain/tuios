// Ported from pele src/text/icons.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
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

type iconMeasurer struct {
	TextMeasurer
	icons *iconState
}

// WithIcons gives a measurer that also carries the host's icons for one render.
// A label leaves room for an icon only when the host has one by that name, and
// layout has to know that before anything is drawn.
func WithIcons(m TextMeasurer, icons IconResolver) TextMeasurer {
	if im, ok := m.(iconMeasurer); ok {
		m = im.TextMeasurer
	}
	return iconMeasurer{m, &iconState{resolver: icons, found: map[string]string{}}}
}

// IconMarkup is the host's markup for an icon, through the resolver the measurer carries.
func IconMarkup(m TextMeasurer, name string) string {
	im, ok := m.(iconMeasurer)
	if !ok || im.icons.resolver == nil {
		return ""
	}
	markup, ok := im.icons.found[name]
	if !ok {
		markup = im.icons.resolver(name)
		im.icons.found[name] = markup
	}
	return markup
}
