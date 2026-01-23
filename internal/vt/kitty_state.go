package vt

import (
	"sync"
	"time"
)

type KittyState struct {
	mu            sync.RWMutex
	images        map[uint32]*KittyImage
	imagesByNum   map[uint32]uint32
	placements    []*KittyPlacement
	nextID        uint32
	pending       *KittyPendingChunk
	dirty         bool
	clearCallback func() // Called when placements/images are cleared
}

func NewKittyState() *KittyState {
	return &KittyState{
		images:      make(map[uint32]*KittyImage),
		imagesByNum: make(map[uint32]uint32),
		nextID:      1,
	}
}

// SetClearCallback sets a callback that will be called when placements are cleared.
// This is used by passthrough mode to clear images on the host terminal.
func (s *KittyState) SetClearCallback(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearCallback = fn
}

func (s *KittyState) AllocateID() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	if s.nextID == 0 {
		s.nextID = 1
	}
	return id
}

func (s *KittyState) AddImage(img *KittyImage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.images[img.ID] = img
	if img.Number > 0 {
		s.imagesByNum[img.Number] = img.ID
	}
	s.dirty = true
}

func (s *KittyState) GetImage(id uint32) *KittyImage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.images[id]
}

func (s *KittyState) GetImageByNumber(num uint32) *KittyImage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if id, ok := s.imagesByNum[num]; ok {
		return s.images[id]
	}
	return nil
}

func (s *KittyState) DeleteImage(id uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if img, ok := s.images[id]; ok {
		if img.Number > 0 {
			delete(s.imagesByNum, img.Number)
		}
		delete(s.images, id)
	}
	s.removePlacementsForImage(id)
	s.dirty = true
}

func (s *KittyState) DeleteImageByNumber(num uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.imagesByNum[num]; ok {
		delete(s.images, id)
		s.removePlacementsForImage(id)
	}
	delete(s.imagesByNum, num)
	s.dirty = true
}

func (s *KittyState) removePlacementsForImage(imageID uint32) {
	filtered := s.placements[:0]
	for _, p := range s.placements {
		if p.ImageID != imageID {
			filtered = append(filtered, p)
		}
	}
	s.placements = filtered
}

func (s *KittyState) AddPlacement(p *KittyPlacement) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.PlacementID > 0 {
		for i, existing := range s.placements {
			if existing.ImageID == p.ImageID && existing.PlacementID == p.PlacementID {
				s.placements[i] = p
				s.dirty = true
				return
			}
		}
	}
	s.placements = append(s.placements, p)
	s.dirty = true
}

func (s *KittyState) DeletePlacement(imageID, placementID uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.placements[:0]
	for _, p := range s.placements {
		if p.ImageID != imageID || p.PlacementID != placementID {
			filtered = append(filtered, p)
		}
	}
	s.placements = filtered
	s.dirty = true
}

func (s *KittyState) DeletePlacementsAtCursor(x, y int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.placements[:0]
	for _, p := range s.placements {
		if p.ScreenX != x || p.ScreenY != y {
			filtered = append(filtered, p)
		}
	}
	s.placements = filtered
	s.dirty = true
}

func (s *KittyState) DeletePlacementsInColumn(x int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.placements[:0]
	for _, p := range s.placements {
		if p.ScreenX != x {
			filtered = append(filtered, p)
		}
	}
	s.placements = filtered
	s.dirty = true
}

func (s *KittyState) DeletePlacementsInRow(y int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.placements[:0]
	for _, p := range s.placements {
		if p.ScreenY != y {
			filtered = append(filtered, p)
		}
	}
	s.placements = filtered
	s.dirty = true
}

func (s *KittyState) DeletePlacementsByZIndex(z int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.placements[:0]
	for _, p := range s.placements {
		if p.ZIndex != z {
			filtered = append(filtered, p)
		}
	}
	s.placements = filtered
	s.dirty = true
}

func (s *KittyState) Clear() {
	s.mu.Lock()
	callback := s.clearCallback
	s.images = make(map[uint32]*KittyImage)
	s.imagesByNum = make(map[uint32]uint32)
	s.placements = nil
	s.pending = nil
	s.dirty = true
	s.mu.Unlock()
	// Call callback outside the lock to avoid deadlocks
	if callback != nil {
		callback()
	}
}

func (s *KittyState) ClearPlacements() {
	s.mu.Lock()
	callback := s.clearCallback
	hadPlacements := len(s.placements) > 0
	if hadPlacements {
		s.placements = nil
		s.dirty = true
	}
	s.mu.Unlock()
	// Always call callback (needed for passthrough mode where placements
	// are stored externally, not in KittyState)
	if callback != nil {
		callback()
	}
}

func (s *KittyState) GetImages() []*KittyImage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*KittyImage, 0, len(s.images))
	for _, img := range s.images {
		result = append(result, img)
	}
	return result
}

func (s *KittyState) GetPlacements() []*KittyPlacement {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*KittyPlacement, len(s.placements))
	copy(result, s.placements)
	return result
}

func (s *KittyState) IsDirty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dirty
}

func (s *KittyState) ClearDirty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = false
}

func (s *KittyState) SetPending(chunk *KittyPendingChunk) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = chunk
}

func (s *KittyState) GetPending() *KittyPendingChunk {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pending
}

func (s *KittyState) ClearPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = nil
}

func (s *KittyState) AppendToPending(data []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return false
	}
	s.pending.DataBuffer = append(s.pending.DataBuffer, data...)
	return true
}

func (s *KittyState) FinalizePending() *KittyImage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return nil
	}
	img := &KittyImage{
		ID:           s.pending.ImageID,
		Number:       s.pending.ImageNumber,
		Width:        s.pending.Width,
		Height:       s.pending.Height,
		Format:       s.pending.Format,
		Compression:  s.pending.Compression,
		Data:         s.pending.DataBuffer,
		TransmitTime: time.Now(),
	}
	s.pending = nil
	return img
}
