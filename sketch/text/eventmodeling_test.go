package text

import "testing"

func TestEventModelText(t *testing.T) {
	src := "eventmodeling\ntf 01 ui Cart\ntf 02 cmd Add { a: 1 }\ntf 03 evt Added\nrf 04 rmo Items ->> 01\nnote 02 {\n  checks stock\n}\n"
	drawPinned(t, src, 0, `
┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄

UI/Automation        ┌ 01 ────┐
                     │ Cart   │
                     └────────┘
                            ││
┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┼┼┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄
                            ▼└──────────▼
Command/Read Model        ┌ 02 ────┐   ┌ 04 ↺ ──┐
                          │ Add    │   │ Items  │
                          │ a: 1   │   └────────┘
                          └────────┘
                                 │
┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┼┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄
                                 ▼
Events                         ┌ 03 ────┐
                               │ Added  │
                               └────────┘

┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄

┌───────────────┐
│ Note · 02 Add │
│ checks stock  │
└───────────────┘
`)
	// The lanes, tighter: lane names on the rules.
	drawPinned(t, src, 30, `
┄┄ UI/Automation ┄┄┄┄┄┄┄┄┄┄┄┄

 ┌ 01 ────┐
 │ Cart   │
 └────────┘
        ││
┄┄ Command/Read Model ┄┄┄┄┄┄┄
        ▼└─────────▼
     ┌ 02 ────┐   ┌ 04 ↺ ──┐
     │ Add    │   │ Items  │
     │ a: 1   │   └────────┘
     └────────┘
            │
┄┄ Events ┄┄┼┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄
            ▼
         ┌ 03 ────┐
         │ Added  │
         └────────┘

┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄

┌───────────────┐
│ Note · 02 Add │
│ checks stock  │
└───────────────┘
`)
	// Narrower still: the list.
	drawPinned(t, src, 24, `
UI/Automation
┌ 01 ──┐
│ Cart │
└──────┘
  ▼
Command/Read Model
┌ 02 ──┐
│ Add  │
│ a: 1 │
└──────┘
  ▼
Events
┌ 03 ───┐
│ Added │
└───────┘
Command/Read Model
┌ 04 ↺ ───┐
│ Items   │
│ from 01 │
└─────────┘

┌───────────────┐
│ Note · 02 Add │
│ checks stock  │
└───────────────┘
`)
	fitsCorpus(t, "eventmodeling", 80)
	fitsCorpus(t, "eventmodeling", 30)
}
