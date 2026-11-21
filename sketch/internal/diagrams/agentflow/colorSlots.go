package agentflow

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
