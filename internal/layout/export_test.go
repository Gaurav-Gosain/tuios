package layout

func CalculateTilingLayout(n int, screenWidth int, usableHeight int, topMargin int, masterRatio float64, gap int) []TileLayout {
	return CalculateMasterStackLayout(n, screenWidth, usableHeight, topMargin, masterRatio, 0, gap)
}
