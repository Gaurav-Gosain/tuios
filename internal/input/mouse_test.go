package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func TestIsInTerminalContent(t *testing.T) {
	tests := []struct {
		name   string
		x, y   int
		width  int
		height int
		want   bool
	}{
		{
			name: "inside content area",
			x:    5, y: 5,
			width: 80, height: 24,
			want: true,
		},
		{
			name: "at origin",
			x:    0, y: 0,
			width: 80, height: 24,
			want: true,
		},
		{
			name: "at max valid position",
			x:    77, y: 21, // width-2-1, height-2-1
			width: 80, height: 24,
			want: true,
		},
		{
			name: "negative x",
			x:    -1, y: 5,
			width: 80, height: 24,
			want: false,
		},
		{
			name: "negative y",
			x:    5, y: -1,
			width: 80, height: 24,
			want: false,
		},
		{
			name: "x at right border",
			x:    78, y: 5, // width-2
			width: 80, height: 24,
			want: false,
		},
		{
			name: "y at bottom border",
			x:    5, y: 22, // height-2
			width: 80, height: 24,
			want: false,
		},
		{
			name: "small window",
			x:    0, y: 0,
			width: 10, height: 10,
			want: true,
		},
		{
			name: "small window at edge",
			x:    7, y: 7, // 10-2-1
			width: 10, height: 10,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			win := &terminal.Window{
				Width:  tt.width,
				Height: tt.height,
			}
			got := isInTerminalContent(tt.x, tt.y, win)
			if got != tt.want {
				t.Errorf("isInTerminalContent(%d, %d, {Width: %d, Height: %d}) = %v, want %v",
					tt.x, tt.y, tt.width, tt.height, got, tt.want)
			}
		})
	}
}
