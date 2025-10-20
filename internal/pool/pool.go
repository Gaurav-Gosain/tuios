package pool

import (
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss/v2"
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
)

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
