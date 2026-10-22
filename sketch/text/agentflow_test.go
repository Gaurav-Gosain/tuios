package text

import "testing"

func TestAgentflowText(t *testing.T) {
	drawPinned(t, "agentflow-beta TB\n  flow f[\"Agent\"]\n    a[\"Plan\"]@{ shape: task }\n    t[\"search\"]@{ shape: tool }\n    a --> t\n  end\n  ok[\"Done?\"]@{ shape: decision }\n  t --> ok", 0, `
 ┌─ Agent ──────────────┐
 │                      │
 │   ╭───────────────╮  │
 │   │     Plan      │  │
 │   ╰───────────────╯  │
 │           │          │
 │           ▼          │
 │  ┌─────────────────┐ │
 │  │     search      │ │
 │  └─────────────────┘ │
 │           │          │
 └───────────┼──────────┘
             │
             ▼
        ╱▔▔▔▔▔▔▔▔▔╲
       ╱           ╲
       ▏   Done?   ▕
       ╲           ╱
        ╲▁▁▁▁▁▁▁▁▁╱
`)
	fitsCorpus(t, "agentflow", 80)
}
