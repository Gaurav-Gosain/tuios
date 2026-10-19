// Package loom draws TML documents into terminal cells.
//
// A Surface holds one document (a tuios.dev/tml tree and its state store)
// and draws it into a cell Buffer at any size:
//
//	doc, _, err := tml.Check(src, nil)
//	s, err := loom.New(doc, &loom.Options{Palette: loom.DefaultPalette(loom.Color256)})
//	buf := s.Render(80, 24)
//	fmt.Print(buf.Render()) // or buf.String() for plain text
//
// Layout is a flex box for cells: row, col, grid, stack, spacer, scroll and
// split, with w and h as cells, percent or fill weights, min and max, pad,
// gap, align, justify and grow. Bindings may read view.width, view.height
// and view.band (narrow, medium or wide) to change the layout with the
// width.
//
// Every colour is a role, such as success or series2. The host passes a
// Palette at the depth of the client: truecolor, 256, 16 or ASCII. At 16
// colours there are no grounds, so the cursor row and a primary button go to
// reverse video. In ASCII loom also draws its own marks in ASCII.
//
// The tree is retained. A state patch marks only the boxes whose bindings
// read the changed paths; Render evaluates those again, lays the tree
// out and repaints the boxes whose content or place changed. Damage lists
// the rectangles.
//
// A click on a link comes back as a link Effect for the host to confirm.
// NeedsClock and Tick update the bindings that call ago().
//
// Key and Mouse take input. Tabs, sorting, the cursor, folding, scrolling
// and set run locally with no round trip. emit, close, copy, open and pane
// come back to the host as Effects.
//
// Charts draw through tuios.dev/plot. loom imports no tuios package.
package loom
