package app

import (
	"bytes"
	"fmt"
	"slices"
)

// One image can be placed many times. kitty keeps a placement per a=p: an a=p
// with p= replaces that placement, and an a=p without p= adds a new one each
// time. cbirds draws one sprite at several points of every frame, and tuios
// used to keep one placement per image, so all but the last of them vanished.
//
// placements is keyed by image and slot. Slot 0 is the placement an upload
// makes itself (a=T, a file or a video frame); those paths treat it as the
// image's own placement, as before. Every a=p gets a slot of its own from a
// counter. The guest's p= is never a slot or a host id: a table per image
// maps it to the slot it was placed in, so the next a=p with that p= finds
// and replaces it, and no p= the guest picks can collide with tuios's own.
//
// On the host each record has its own run of placementStride placement ids,
// one per slice, from an allocator per image. One record can be moved,
// cropped or taken down without touching another.
//
// placementIndex keeps the lookups that every a=p makes constant time, and
// bounds the records: a guest that places without p= and never deletes adds
// one per a=p, as in kitty. Past maxPlacementsPerImage of one image, or
// maxPlacementsPerPane in a pane, the oldest goes.
type placementKey struct {
	host, slot uint32
}

const (
	// placementStride is the most host placement ids one record uses: one per
	// slice of the image left clear by what is over it.
	placementStride uint32 = 64
	// maxPlacementsPerImage and maxPlacementsPerPane bound the a=p records
	// kept. cbirds places one sprite at most a few times a frame, and every
	// frame starts with d=a.
	maxPlacementsPerImage = 256
	maxPlacementsPerPane  = 4096
)

// placementIndex is the lookups for one window's placements.
type placementIndex struct {
	// byHost holds the slots in use for each host image.
	byHost map[uint32]map[uint32]struct{}
	// guestP maps an image's guest placement id to its slot.
	guestP map[[2]uint32]uint32
	// nextSlot hands out slots for a=p records.
	nextSlot uint32
	// bases hands out runs of host placement ids for each host image.
	bases map[uint32]*placementBases
	// order holds the a=p records oldest first. A key whose record is gone,
	// or was replaced, is skipped when it is reached.
	order []orderedKey
	// placed counts the a=p records.
	placed int
	seq    uint64
}

type orderedKey struct {
	key placementKey
	seq uint64
}

type placementBases struct {
	next uint32
	free []uint32
}

// imageKey is the key of the placement an upload makes itself.
func imageKey(hostID uint32) placementKey {
	return placementKey{host: hostID}
}

func (kp *KittyPassthrough) index(windowID string) *placementIndex {
	if kp.placeIdx == nil {
		kp.placeIdx = make(map[string]*placementIndex)
	}
	ix := kp.placeIdx[windowID]
	if ix == nil {
		ix = &placementIndex{
			byHost: make(map[uint32]map[uint32]struct{}),
			guestP: make(map[[2]uint32]uint32),
			bases:  make(map[uint32]*placementBases),
		}
		kp.placeIdx[windowID] = ix
	}
	return ix
}

// putPlacement records a placement under key, replacing what was there. A
// new a=p record past the caps takes the oldest down.
func (kp *KittyPassthrough) putPlacement(windowID string, key placementKey, p *PassthroughPlacement) {
	if kp.placements[windowID] == nil {
		kp.placements[windowID] = make(map[placementKey]*PassthroughPlacement)
	}
	ix := kp.index(windowID)
	old := kp.placements[windowID][key]
	if old != nil && old.GuestPlacementID != p.GuestPlacementID {
		delete(ix.guestP, [2]uint32{key.host, old.GuestPlacementID})
	}
	if old != nil && old.PlacementID != 0 && old.PlacementID != p.PlacementID {
		kp.freeBase(windowID, key.host, old.PlacementID)
	}
	p.slot = key.slot
	if p.WindowID == "" {
		p.WindowID = windowID
	}
	ix.seq++
	p.seq = ix.seq
	kp.placements[windowID][key] = p
	if ix.byHost[key.host] == nil {
		ix.byHost[key.host] = make(map[uint32]struct{})
	}
	ix.byHost[key.host][key.slot] = struct{}{}
	if p.GuestPlacementID != 0 {
		ix.guestP[[2]uint32{key.host, p.GuestPlacementID}] = key.slot
	}
	if key.slot == 0 {
		return
	}
	ix.order = append(ix.order, orderedKey{key, p.seq})
	if old == nil {
		ix.placed++
	}
	kp.enforcePlacementCaps(windowID, key)
}

// dropPlacement forgets one placement. It sends the host nothing.
func (kp *KittyPassthrough) dropPlacement(windowID string, key placementKey) {
	p := kp.placements[windowID][key]
	if p == nil {
		return
	}
	delete(kp.placements[windowID], key)
	ix := kp.placeIdx[windowID]
	if ix == nil {
		return
	}
	if slotsOf := ix.byHost[key.host]; slotsOf != nil {
		delete(slotsOf, key.slot)
		if len(slotsOf) == 0 {
			delete(ix.byHost, key.host)
			delete(ix.bases, key.host)
		}
	}
	if p.GuestPlacementID != 0 && ix.guestP[[2]uint32{key.host, p.GuestPlacementID}] == key.slot {
		delete(ix.guestP, [2]uint32{key.host, p.GuestPlacementID})
	}
	if p.PlacementID != 0 {
		kp.freeBase(windowID, key.host, p.PlacementID)
	}
	if key.slot != 0 {
		ix.placed--
	}
	// The order list is pruned lazily; keep it from outgrowing the records.
	if len(ix.order) > 2*ix.placed+64 {
		live := ix.order[:0]
		for _, o := range ix.order {
			if q := kp.placements[windowID][o.key]; q != nil && q.seq == o.seq {
				live = append(live, o)
			}
		}
		ix.order = slices.Clip(live)
	}
}

// dropWindowPlacements forgets every placement of a window. It sends the
// host nothing.
func (kp *KittyPassthrough) dropWindowPlacements(windowID string) {
	delete(kp.placements, windowID)
	delete(kp.placeIdx, windowID)
}

// enforcePlacementCaps takes the oldest a=p record down while the image or
// the pane holds more than its cap. key is the record just added, which
// stays.
func (kp *KittyPassthrough) enforcePlacementCaps(windowID string, key placementKey) {
	ix := kp.placeIdx[windowID]
	placements := kp.placements[windowID]
	if n := len(ix.byHost[key.host]); n > maxPlacementsPerImage {
		var oldest placementKey
		var oldestSeq uint64
		for slot := range ix.byHost[key.host] {
			k := placementKey{key.host, slot}
			if p := placements[k]; slot != 0 && k != key && p != nil && (oldestSeq == 0 || p.seq < oldestSeq) {
				oldest, oldestSeq = k, p.seq
			}
		}
		if oldestSeq != 0 {
			kp.evictPlacement(windowID, oldest)
		}
	}
	for ix.placed > maxPlacementsPerPane && len(ix.order) > 0 {
		o := ix.order[0]
		ix.order = ix.order[1:]
		if p := placements[o.key]; p != nil && p.seq == o.seq && o.key != key {
			kp.evictPlacement(windowID, o.key)
		}
	}
}

// evictPlacement takes one placement down on the host and forgets it.
func (kp *KittyPassthrough) evictPlacement(windowID string, key placementKey) {
	if p := kp.placements[windowID][key]; p != nil {
		if !p.Hidden {
			kp.deletePlacementIDs(p)
		}
		kp.dropPlacement(windowID, key)
	}
}

// hostHasPlacement reports whether any placement of a host image is tracked
// in a window.
func (kp *KittyPassthrough) hostHasPlacement(windowID string, hostID uint32) bool {
	ix := kp.placeIdx[windowID]
	return ix != nil && len(ix.byHost[hostID]) > 0
}

// guestSlot is the slot an image's guest placement id was placed in.
func (kp *KittyPassthrough) guestSlot(windowID string, hostID, guestP uint32) (uint32, bool) {
	ix := kp.placeIdx[windowID]
	if ix == nil {
		return 0, false
	}
	slot, ok := ix.guestP[[2]uint32{hostID, guestP}]
	return slot, ok
}

// freshSlot picks a slot for an a=p, one no placement of the image in this
// window has.
func (kp *KittyPassthrough) freshSlot(windowID string, hostID uint32) uint32 {
	ix := kp.index(windowID)
	for {
		ix.nextSlot++
		if ix.nextSlot == 0 {
			continue
		}
		if _, used := ix.byHost[hostID][ix.nextSlot]; !used {
			return ix.nextSlot
		}
	}
}

// allocBase hands out the first of a run of placementStride host placement
// ids for one record of an image.
func (kp *KittyPassthrough) allocBase(windowID string, hostID uint32) uint32 {
	ix := kp.index(windowID)
	b := ix.bases[hostID]
	if b == nil {
		b = &placementBases{next: 1}
		ix.bases[hostID] = b
	}
	if n := len(b.free); n > 0 {
		base := b.free[n-1]
		b.free = b.free[:n-1]
		return base
	}
	base := b.next
	b.next += placementStride
	return base
}

func (kp *KittyPassthrough) freeBase(windowID string, hostID, base uint32) {
	if ix := kp.placeIdx[windowID]; ix != nil {
		if b := ix.bases[hostID]; b != nil {
			b.free = append(b.free, base)
		}
	}
}

// deleteImagePlacements takes down the placements of one host image in a
// window: all of them when guestP is 0, or the one the guest placed with
// p=guestP. It reports whether any placement of the image is left.
func (kp *KittyPassthrough) deleteImagePlacements(windowID string, hostID, guestP uint32) bool {
	ix := kp.placeIdx[windowID]
	if guestP == 0 {
		kp.deleteOnePlacement(&PassthroughPlacement{HostImageID: hostID})
		if ix != nil {
			for slot := range ix.byHost[hostID] {
				kp.dropPlacement(windowID, placementKey{hostID, slot})
			}
		}
		return false
	}
	if slot, ok := kp.guestSlot(windowID, hostID, guestP); ok {
		key := placementKey{hostID, slot}
		if p := kp.placements[windowID][key]; p != nil {
			kp.deletePlacementIDs(p)
		}
		kp.dropPlacement(windowID, key)
	}
	return kp.hostHasPlacement(windowID, hostID)
}

// deleteWindowImages takes down every placement in a window, one delete per
// image rather than one per placement. With visibleOnly, an image whose
// placements are all hidden already is left alone.
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

// sharesImage reports whether a record has other placements of its image
// beside it in its window.
func (kp *KittyPassthrough) sharesImage(p *PassthroughPlacement) bool {
	if p.WindowID == "" {
		return false
	}
	ix := kp.placeIdx[p.WindowID]
	return ix != nil && len(ix.byHost[p.HostImageID]) > 1
}

// placementBaseFor gives a record its run of host placement ids the first
// time it is placed.
func (kp *KittyPassthrough) placementBaseFor(p *PassthroughPlacement) uint32 {
	if p.PlacementID == 0 {
		if p.WindowID == "" {
			p.PlacementID = 1
		} else {
			p.PlacementID = kp.allocBase(p.WindowID, p.HostImageID)
		}
	}
	return p.PlacementID
}
