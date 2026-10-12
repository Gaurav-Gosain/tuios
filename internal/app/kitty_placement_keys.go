package app

import (
	"bytes"
	"fmt"
)

// One image can be placed many times. kitty keeps a placement per a=p: an a=p
// with p= replaces that placement, and an a=p without p= adds a new one each
// time. cbirds draws one sprite at several points of every frame, and tuios
// used to keep one placement per image, so all but the last of them vanished.
//
// placements is keyed by image and slot:
//
//   - slot 0 is the placement an upload makes itself (a=T, a file or a video
//     frame). Those paths treat it as the image's own placement, as before.
//   - an a=p with p= has the guest's placement id as its slot, so the next
//     a=p with the same p= finds and replaces it.
//   - an a=p without p= gets a fresh slot from slotFreshStart up.
//
// On the host each record has its own run of placement ids, so one record can
// be moved, cropped or taken down without touching another. A slot 0 record
// keeps the ids it always had (the guest's p=, or 1). Every other record gets
// placementStride ids from placementBaseStart up, one per slice; see
// placeSlices.
type placementKey struct {
	host, slot uint32
}

const (
	slotFreshStart     uint32 = 1 << 31
	placementBaseStart uint32 = 1 << 30
	// placementStride is the most host placement ids one record uses: one per
	// slice of the image left clear by what is over it.
	placementStride uint32 = 64
)

// imageKey is the key of the placement an upload makes itself.
func imageKey(hostID uint32) placementKey {
	return placementKey{host: hostID}
}

// hostHasPlacement reports whether any placement of a host image is tracked
// in a window.
func (kp *KittyPassthrough) hostHasPlacement(windowID string, hostID uint32) bool {
	for k := range kp.placements[windowID] {
		if k.host == hostID {
			return true
		}
	}
	return false
}

// freshSlot picks a slot for an a=p without p=, one no placement of the image
// in this window has.
func (kp *KittyPassthrough) freshSlot(windowID string, hostID uint32) uint32 {
	placements := kp.placements[windowID]
	for slot := slotFreshStart; ; slot++ {
		if _, used := placements[placementKey{hostID, slot}]; !used {
			return slot
		}
	}
}

// placementBase picks the first host placement id for a new a=p record: the
// lowest run of placementStride ids that no other record of the image uses.
func (kp *KittyPassthrough) placementBase(windowID string, hostID uint32) uint32 {
	used := map[uint32]bool{}
	for k, p := range kp.placements[windowID] {
		if k.host == hostID && k.slot != 0 {
			used[p.PlacementID] = true
		}
	}
	for base := placementBaseStart; ; base += placementStride {
		if !used[base] {
			return base
		}
	}
}

// deleteImagePlacements takes down the placements of one host image in a
// window: all of them when guestP is 0, or the one the guest placed with
// p=guestP. It reports whether any placement of the image is left.
func (kp *KittyPassthrough) deleteImagePlacements(windowID string, hostID, guestP uint32) bool {
	placements := kp.placements[windowID]
	if guestP == 0 {
		kp.deleteOnePlacement(&PassthroughPlacement{HostImageID: hostID})
		for k := range placements {
			if k.host == hostID {
				delete(placements, k)
			}
		}
		return false
	}
	left := false
	for k, p := range placements {
		if k.host != hostID {
			continue
		}
		if p.GuestPlacementID == guestP {
			kp.deletePlacementIDs(p)
			delete(placements, k)
			continue
		}
		left = true
	}
	return left
}

// deleteWindowImages takes down every placement in a window, one delete per
// image rather than one per placement, and forgets them. With visibleOnly,
// an image whose placements are all hidden already is left alone.
func (kp *KittyPassthrough) deleteWindowImages(windowID string, visibleOnly bool) {
	done := map[uint32]bool{}
	for _, p := range kp.placements[windowID] {
		if done[p.HostImageID] || (visibleOnly && p.Hidden) {
			continue
		}
		done[p.HostImageID] = true
		kp.deleteOnePlacement(&PassthroughPlacement{HostImageID: p.HostImageID})
	}
}

// deletePlacementIDs takes down the host placements one record put up, and
// leaves the image's other placements alone.
func (kp *KittyPassthrough) deletePlacementIDs(p *PassthroughPlacement) {
	if p.PlacementID == 0 {
		// Never placed.
		return
	}
	n := max(p.placedSlices, 1)
	if !kp.guestCapture {
		kp.dropHeldPlacementRange(p.HostImageID, p.PlacementID, uint32(n))
	}
	var buf bytes.Buffer
	for i := range n {
		fmt.Fprintf(&buf, "\x1b_Ga=d,d=i,i=%d,p=%d,q=2\x1b\\", p.HostImageID, p.PlacementID+uint32(i))
	}
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
}

// sharesImage reports whether a slot 0 record has other placements of its
// image beside it in its window.
func (kp *KittyPassthrough) sharesImage(p *PassthroughPlacement) bool {
	if p.WindowID == "" {
		return false
	}
	for k := range kp.placements[p.WindowID] {
		if k.host == p.HostImageID && k.slot != 0 {
			return true
		}
	}
	return false
}
