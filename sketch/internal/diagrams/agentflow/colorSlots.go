// Ported from pele src/diagrams/agentflow/colorSlots.ts at 8aff183be57021b533f319a29341a529be934b08.

package agentflow

// Two rules, as in Mermaid: a node takes the color of its kind, from a fixed slot, so editing the
// diagram around it never recolors it; containers count through the slots above the kinds, in the
// order they were declared, so a frame does not take the color of a node inside it.

var KIND_SLOT = map[string]int{
	"tool":      0,
	"task":      1,
	"decision":  2,
	"input":     3,
	"refdoc":    4,
	"connector": 5,
	"action":    6,
}

const KIND_COUNT = 7

// containerSlotCount is at least one, so that a palette shorter than the kinds still gives containers a slot.
func containerSlotCount(paletteLength int) int { return max(1, paletteLength-KIND_COUNT) }

func containerSlot(n int, paletteLength int) int {
	slot := KIND_COUNT + n%containerSlotCount(paletteLength)
	if paletteLength > 0 {
		return slot % paletteLength
	}
	return slot
}

func assignColorSlots(nodes []*GraphNode, kindOf func(id string) string, containerOrder map[string]int, paletteLength int) {
	fallback := len(containerOrder)
	for _, node := range nodes {
		// A collapsed container is drawn as a node but keeps its slot, so collapsing one does not
		// recolor the containers after it.
		if node.IsGroup || node.Shape == "collapsedGroup" {
			n, ok := containerOrder[node.ID]
			if !ok {
				n = fallback
				fallback++
			}
			slot := containerSlot(n, paletteLength)
			node.ColorIndex = &slot
			continue
		}
		kind := kindOf(node.ID)
		if _, ok := KIND_SLOT[kind]; kind != "" && ok {
			node.Kind = kind
		}
	}
}
