package text

import "testing"

func TestSwimlaneText(t *testing.T) {
	drawPinned(t, "swimlane-beta LR\n  subgraph Customer\n    browse[Browse] --> pay[Pay]\n  end\n  subgraph Warehouse\n    pick[Pick] --> ship[Ship]\n  end\n  pay -- paid --> pick", 0, `
 ┌─ Customer ──────────────────────────────────────────────────┐
 │                                                             │
 │       ┌─────────┐     ┌──────┐                              │
 │       │ Browse  │────►│ Pay  │                              │
 │       └─────────┘     └──────┘                              │
 └───────────────────────────┼─────────────────────────────────┘
 ┌─ Warehouse ───────────────┼─────────────────────────────────┐
 │                         paid                                │
 │                           │         ┌──────┐      ┌──────┐  │
 │                           └────────►│ Pick │─────►│ Ship │  │
 │                                     └──────┘      └──────┘  │
 └─────────────────────────────────────────────────────────────┘
`)
	// A node outside every lane gets a lane without a title.
	drawPinned(t, "swimlane-beta TD\n  start --> a\n  subgraph Team\n    a --> b\n  end", 0, `
 ┌──────────────┐┌─ Team ───┐
 │              ││          │
 │   ┌───────┐  ││          │
 │   │ start │──┼┼─────┐    │
 │   └───────┘  ││     │    │
 │              ││     ▼    │
 │              ││  ┌────┐  │
 │              ││  │ a  │  │
 │              ││  └────┘  │
 │              ││     │    │
 │              ││     ▼    │
 │              ││  ┌────┐  │
 │              ││  │ b  │  │
 │              ││  └────┘  │
 └──────────────┘└──────────┘
`)
}
