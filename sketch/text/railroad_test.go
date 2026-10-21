package text

import "testing"

func TestRailroadText(t *testing.T) {
	src := "railroad-ebnf-beta\nnumber = [ \"-\" ] digit { digit } ;\n"
	drawPinned(t, src, 0, `
number
   ┌─────┐               ┌─────────────┐
   │╭───╮│   ┌───────┐   │  ┌───────┐  │
●──┴┤ - ├┴───┤ digit ├───┴─┬┤ digit ├┬─┴──●
    ╰───╯    └───────┘     │└───────┘│
                           └────◀────┘
`)
	drawPinned(t, src, 16, `
number
  [ "-" ] digit
  { digit }
`)
	drawPinned(t, src, 34, `
number
     ┌─────┐
     │╭───╮│   ┌───────┐
●────┴┤ - ├┴───┤ digit ├─┐┌──●
      ╰───╯    └───────┘ ││
   ┌─────────────────────┘│
   │ ┌─────────────┐      │
   │ │  ┌───────┐  │      │
   └─┴─┬┤ digit ├┬─┴──────┘
       │└───────┘│
       └────◀────┘
`)
	fitsCorpus(t, "railroad", 80)
	fitsCorpus(t, "railroad", 30)
}
