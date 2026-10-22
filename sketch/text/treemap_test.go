package text

import "testing"

func TestTreemapText(t *testing.T) {
	src := "treemap-beta\n\"Fruit\"\n    \"Apple\": 30\n    \"Pear\": 10\n\"Veg\"\n    \"Leek\": 20"
	drawPinned(t, src, 0, `
┌─ Fruit  40 ──────────────────────┬─ Veg  20 ───────┐
├─────────────────────────┬────────┼─────────────────┤
│                         │        │                 │
│          Apple          │  Pear  │      Leek       │
│           30            │   10   │       20        │
│                         │        │                 │
└─────────────────────────┴────────┴─────────────────┘
`)
	// Narrower than every label needs: the labels are shortened.
	drawPinned(t, src, 30, `
┌─ Fruit  40 ──────┬─ Veg ───┐
├─────────────┬────┼─────────┤
│             │    │         │
│    Apple    │Pear│  Leek   │
│     30      │ 10 │   20    │
│             │    │         │
└─────────────┴────┴─────────┘
`)
	// Too narrow for the boxes: the tree.
	drawPinned(t, src, 16, `
Fruit  40
├ Apple  30
└ Pear  10
Veg  20
└ Leek  20
`)
	fitsCorpus(t, "treemap", 80)
	fitsCorpus(t, "treemap", 40)
}
