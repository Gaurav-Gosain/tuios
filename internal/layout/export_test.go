package layout

// Seams the tests reach for. Production code calls the functions these wrap.

// CalculateTilingLayout is CalculateMasterStackLayout with the two stacked
// panes of the three pane layout sharing the height equally.
func CalculateTilingLayout(n int, screenWidth int, usableHeight int, topMargin int, masterRatio float64, gap int) []TileLayout {
	return CalculateMasterStackLayout(n, screenWidth, usableHeight, topMargin, masterRatio, 0, gap)
}
