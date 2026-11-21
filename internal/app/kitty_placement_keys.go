package app

import (
	"bytes"
	"fmt"
	"slices"
)

type placementKey struct {
	host, slot uint32
}

const (
	placementStride uint32 = 64

	maxPlacementsPerImage = 256
	maxPlacementsPerPane  = 4096
)

type placementIndex struct {
	byHost   map[uint32]map[uint32]struct{}
	guestP   map[[2]uint32]uint32
	nextSlot uint32
	bases    map[uint32]*placementBases
	order    []orderedKey
	placed   int
	seq      uint64
}

type orderedKey struct {
	key placementKey
	seq uint64
}

type placementBases struct {
	next uint32
	free []uint32
}

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
	kp.pruneOrder(windowID, ix)
}

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
			kp.forgetUnnamedKept(windowID, key.host)
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
	kp.pruneOrder(windowID, ix)
}

func (kp *KittyPassthrough) pruneOrder(windowID string, ix *placementIndex) {
	if len(ix.order) <= 2*ix.placed+64 {
		return
	}
	live := ix.order[:0]
	for _, o := range ix.order {
		if q := kp.placements[windowID][o.key]; q != nil && q.seq == o.seq {
			live = append(live, o)
		}
	}
	ix.order = slices.Clip(live)
}

func (kp *KittyPassthrough) dropWindowPlacements(windowID string) {
	delete(kp.placements, windowID)
	delete(kp.placeIdx, windowID)
	kp.forgetUnnamedWindowKept(windowID)
}

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

func (kp *KittyPassthrough) evictPlacement(windowID string, key placementKey) {
	if p := kp.placements[windowID][key]; p != nil {
		if !p.Hidden {
			kp.deletePlacementIDs(p)
		}
		kp.dropPlacement(windowID, key)
	}
}

func (kp *KittyPassthrough) hostHasPlacement(windowID string, hostID uint32) bool {
	ix := kp.placeIdx[windowID]
	return ix != nil && len(ix.byHost[hostID]) > 0
}

func (kp *KittyPassthrough) guestSlot(windowID string, hostID, guestP uint32) (uint32, bool) {
	ix := kp.placeIdx[windowID]
	if ix == nil {
		return 0, false
	}
	slot, ok := ix.guestP[[2]uint32{hostID, guestP}]
	return slot, ok
}

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

func (kp *KittyPassthrough) deletePlacementIDs(p *PassthroughPlacement) {
	if p.PlacementID == 0 {
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

func (kp *KittyPassthrough) sharesImage(p *PassthroughPlacement) bool {
	if p.WindowID == "" {
		return false
	}
	ix := kp.placeIdx[p.WindowID]
	return ix != nil && len(ix.byHost[p.HostImageID]) > 1
}

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
