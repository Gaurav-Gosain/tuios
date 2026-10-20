// Ported from pele src/diagrams/sequence/index.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

package sequence

import "tuios.dev/sketch/internal/types"

var Sequence = &types.Diagram{
	Type: "sequence",
	Parse: func(source string, config types.Config, title *string, _ int, _ types.ParseLimits) any {
		seq, _ := config["sequence"].(map[string]any)
		opts := SeqDbOptions{}
		if w, ok := config["wrap"].(bool); ok {
			opts.Wrap = &w
		}
		if w, ok := seq["wrap"].(bool); ok && w {
			opts.SequenceWrap = true
		}
		db := NewSeqDb(opts)
		if title != nil && *title != "" {
			db.SetDiagramTitle(*title)
		}
		parseSequence(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderSequence(model.(*SeqDb), config, options)
	},
}
