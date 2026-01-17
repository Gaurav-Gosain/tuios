package app

import "github.com/Gaurav-Gosain/tuios/internal/vt"

type VisiblePlacement struct {
	GuestImageID uint32
	Placement    *vt.KittyPlacement
	HostX        int
	HostY        int
	ClipLeft     int
	ClipTop      int
	ClipRight    int
	ClipBottom   int
	Visible      bool
}

func TransformPlacements(
	placements []*vt.KittyPlacement,
	windowX, windowY int,
	windowWidth, windowHeight int,
	contentX, contentY int,
	scrollbackLen int,
	scrollOffset int,
	screenWidth, screenHeight int,
) []VisiblePlacement {
	var result []VisiblePlacement

	for _, p := range placements {
		hostX := windowX + contentX + p.ScreenX

		currentScrollbackLen := scrollbackLen
		viewportTop := currentScrollbackLen - scrollOffset
		relativeY := p.AbsoluteLine - viewportTop
		hostY := windowY + contentY + relativeY

		cols := p.Columns
		if cols <= 0 {
			cols = 1
		}
		rows := p.Rows
		if rows <= 0 {
			rows = 1
		}

		placementRight := hostX + cols
		placementBottom := hostY + rows

		windowRight := windowX + windowWidth
		windowBottom := windowY + windowHeight

		if placementRight <= windowX || hostX >= windowRight ||
			placementBottom <= windowY || hostY >= windowBottom {
			continue
		}

		if hostX >= screenWidth || hostY >= screenHeight ||
			placementRight <= 0 || placementBottom <= 0 {
			continue
		}

		vp := VisiblePlacement{
			GuestImageID: p.ImageID,
			Placement:    p,
			HostX:        hostX,
			HostY:        hostY,
			Visible:      true,
		}

		if hostX < windowX {
			vp.ClipLeft = windowX - hostX
			vp.HostX = windowX
		}
		if hostY < windowY {
			vp.ClipTop = windowY - hostY
			vp.HostY = windowY
		}
		if placementRight > windowRight {
			vp.ClipRight = placementRight - windowRight
		}
		if placementBottom > windowBottom {
			vp.ClipBottom = placementBottom - windowBottom
		}

		if hostX < 0 {
			vp.ClipLeft += -hostX
			vp.HostX = 0
		}
		if hostY < 0 {
			vp.ClipTop += -hostY
			vp.HostY = 0
		}
		if placementRight > screenWidth {
			vp.ClipRight += placementRight - screenWidth
		}
		if placementBottom > screenHeight {
			vp.ClipBottom += placementBottom - screenHeight
		}

		result = append(result, vp)
	}

	return result
}
