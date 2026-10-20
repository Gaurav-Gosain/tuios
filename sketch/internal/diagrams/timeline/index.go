// Ported from pele src/diagrams/timeline/index.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

// Package timeline draws Mermaid's timeline: time periods in order along an axis, each
// with its events, grouped in sections.
package timeline

import "tuios.dev/sketch/internal/types"

// Timeline is the timeline diagram type.
var Timeline = &types.Diagram{
	Type: "timeline",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := newTimelineDb()
		db.title = types.Or(title, "")
		parseTimeline(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderTimeline(model.(*TimelineDb), config, options)
	},
}
