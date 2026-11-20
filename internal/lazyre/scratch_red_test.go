package lazyre

import "testing"

func TestScratchFailsOnPurpose(t *testing.T) {
	t.Fatal("scratch: fails on purpose, CI must go red")
}
