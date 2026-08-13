// Package skills holds the agent skill files tuios ships, embedded so the copy
// a binary prints is the copy that was built into it.
//
// A skill fetched from anywhere else can describe commands the running build
// does not have. Embedding removes that failure mode: `tuios --skill` and
// skills/tuios/SKILL.md are the same bytes by construction.
package skills

import _ "embed"

// TUIOS is the skill that teaches an agent to drive tuios from inside a pane.
//
//go:embed tuios/SKILL.md
var TUIOS string
