package text

import "testing"

func TestCynefinText(t *testing.T) {
	src := "---\nconfig:\n  cynefin:\n    showDomainDescriptions: false\n---\ncynefin-beta\n  complex\n    \"Probe\"\n  clear\n    \"Fix\"\n  confusion\n    \"Unknown\"\n  clear --> chaotic : \"Fall\"\n"
	drawPinned(t, src, 0, `
┌──────────────┬──────────────┐
│ Complex      │ Complicated  │
│ • Probe      │              │
│        ┌─────┴─────┐        │
│        │ Confusion │        │
├────────┤ • Unknown ├────────┤
│        └─────┬─────┘        │
│              ┃              │
│ Chaotic      ┃ Clear        │
│              ┃ • Fix        │
└──────────────┻──────────────┘

Clear → Chaotic: Fall
`)
	drawPinned(t, src, 24, `
Complex
  • Probe

Complicated

Chaotic

Clear
  • Fix

Confusion
  • Unknown

Clear → Chaotic: Fall
`)
	fitsCorpus(t, "cynefin", 80)
	fitsCorpus(t, "cynefin", 30)
}
