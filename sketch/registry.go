// Ported from pele src/diagrams/registry.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// Only the types ported so far are listed.

package sketch

import "tuios.dev/sketch/internal/diagrams/flowchart"

func init() {
	register(flowchart.Flowchart)
}
