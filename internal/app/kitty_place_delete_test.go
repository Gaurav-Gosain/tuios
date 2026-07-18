package app

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func newTestKittyPassthrough(t *testing.T) *KittyPassthrough {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devnull.Close() })
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{
		ForceEnable: true,
		Output:      devnull,
	})
	kp.enabled = true
	return kp
}

// TestForwardPlaceKeyedByHostID reproduces the transmit->place->delete-by-id
// flow (ratatui-image, timg). forwardPlace previously keyed its tracked
// placement by the guest image ID while every other path (and the by-id delete)
// keys by host ID, so a guest delete-by-id could not find the placement and
// RefreshAllPlacements kept re-emitting a=p forever. After placing then
// deleting an image by its guest id, no placement should remain.
func TestForwardPlaceKeyedByHostID(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	winID := "test-window-id-abcdef12"

	const guestID uint32 = 5

	// Standard flow: a plain a=t direct transmit is passed through raw; the
	// subsequent a=p is where forwardPlace registers the tracked placement.
	place := &vt.KittyCommand{Action: vt.KittyActionPlace, ImageID: guestID, Columns: 10, Rows: 5}
	kp.ForwardCommand(place, nil, winID, 0, 0, 80, 24, 0, 0, 0, 0, 0, false, nil)

	kp.mu.Lock()
	hostID, mapped := kp.imageIDMap[winID][guestID]
	placements := kp.placements[winID]
	_, keyedByHost := placements[hostID]
	kp.mu.Unlock()

	if !mapped {
		t.Fatal("expected guest image id to be mapped to a host id after a=p")
	}
	if len(placements) != 1 {
		t.Fatalf("expected exactly one tracked placement, got %d", len(placements))
	}
	if !keyedByHost {
		t.Fatalf("placement must be keyed by host id %d, got keys %v", hostID, placementKeys(placements))
	}

	// Guest deletes the image by its guest id (a=d,d=i,i=5). forwardDelete
	// resolves guestID->hostID and deletes placements[win][hostID].
	del := &vt.KittyCommand{Action: vt.KittyActionDelete, Delete: vt.KittyDeleteByID, ImageID: guestID}
	kp.ForwardCommand(del, nil, winID, 0, 0, 80, 24, 0, 0, 0, 0, 0, false, nil)

	kp.mu.Lock()
	remaining := len(kp.placements[winID])
	kp.mu.Unlock()

	if remaining != 0 {
		t.Errorf("BUG: delete-by-id left %d stale placement(s); RefreshAllPlacements would re-emit a=p forever", remaining)
	}
}

func placementKeys(m map[uint32]*PassthroughPlacement) []uint32 {
	keys := make([]uint32, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
