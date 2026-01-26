// Package pool provides object pooling for reusable components to reduce memory allocations.
package pool

import (
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
)

var (
	stringBuilderPool = sync.Pool{
		New: func() any {
			return &strings.Builder{}
		},
	}

	layerPool = sync.Pool{
		New: func() any {
			layers := make([]*lipgloss.Layer, 0, 16)
			return &layers
		},
	}

	// Pool for byte slices used in I/O operations
	byteSlicePool = sync.Pool{
		New: func() any {
			buf := make([]byte, 32*1024)
			return &buf
		},
	}

	// Pool for lipgloss.Style objects to reduce allocations
	stylePool = sync.Pool{
		New: func() any {
			style := lipgloss.NewStyle()
			return &style
		},
	}

	// Pool for highlight grids used in terminal rendering
	highlightGridPool = sync.Pool{
		New: func() any {
			return &HighlightGrid{}
		},
	}
)

// HighlightGrid is a sparse grid for tracking cell highlights.
// It uses a slice of slices instead of nested maps to reduce allocations.
type HighlightGrid struct {
	rows   [][]bool
	maxY   int
	maxX   int
	inited bool
}

// Init initializes the grid for the given dimensions.
// This should be called before using the grid.
func (g *HighlightGrid) Init(maxY, maxX int) {
	g.maxY = maxY
	g.maxX = maxX
	g.inited = true

	// Reuse existing slices if possible
	if cap(g.rows) >= maxY {
		g.rows = g.rows[:maxY]
		for i := range g.rows {
			if g.rows[i] != nil {
				// Clear the row
				clear(g.rows[i])
			}
		}
	} else {
		g.rows = make([][]bool, maxY)
	}
}

// Set marks a cell as highlighted.
func (g *HighlightGrid) Set(y, x int) {
	if y < 0 || y >= g.maxY || x < 0 || x >= g.maxX {
		return
	}

	if g.rows[y] == nil {
		g.rows[y] = make([]bool, g.maxX)
	}
	g.rows[y][x] = true
}

// Get returns whether a cell is highlighted.
func (g *HighlightGrid) Get(y, x int) bool {
	if y < 0 || y >= g.maxY || x < 0 || x >= g.maxX {
		return false
	}
	if g.rows[y] == nil {
		return false
	}
	return g.rows[y][x]
}

// HasRow returns whether a row has any highlights.
func (g *HighlightGrid) HasRow(y int) bool {
	if y < 0 || y >= g.maxY {
		return false
	}
	return g.rows[y] != nil
}

// Reset clears the grid for reuse.
func (g *HighlightGrid) Reset() {
	for i := range g.rows {
		g.rows[i] = nil
	}
	g.inited = false
}

// GetHighlightGrid retrieves a highlight grid from the pool.
func GetHighlightGrid() *HighlightGrid {
	return highlightGridPool.Get().(*HighlightGrid)
}

// PutHighlightGrid returns a highlight grid to the pool after resetting it.
func PutHighlightGrid(g *HighlightGrid) {
	g.Reset()
	highlightGridPool.Put(g)
}

// GetStringBuilder retrieves a string builder from the pool.
func GetStringBuilder() *strings.Builder {
	return stringBuilderPool.Get().(*strings.Builder)
}

// PutStringBuilder returns a string builder to the pool after resetting it.
func PutStringBuilder(sb *strings.Builder) {
	sb.Reset()
	stringBuilderPool.Put(sb)
}

// GetLayerSlice retrieves a layer slice from the pool.
func GetLayerSlice() *[]*lipgloss.Layer {
	return layerPool.Get().(*[]*lipgloss.Layer)
}

// PutLayerSlice returns a layer slice to the pool.
func PutLayerSlice(layers *[]*lipgloss.Layer) {
	layerPool.Put(layers)
}

// GetByteSlice retrieves a byte slice from the pool.
func GetByteSlice() *[]byte {
	return byteSlicePool.Get().(*[]byte)
}

// PutByteSlice returns a byte slice to the pool.
func PutByteSlice(b *[]byte) {
	byteSlicePool.Put(b)
}

// GetStyle retrieves a lipgloss.Style from the pool.
func GetStyle() *lipgloss.Style {
	return stylePool.Get().(*lipgloss.Style)
}

// PutStyle returns a lipgloss.Style to the pool.
func PutStyle(style *lipgloss.Style) {
	stylePool.Put(style)
}
