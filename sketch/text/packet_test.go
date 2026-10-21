package text

import "testing"

func TestPacketText(t *testing.T) {
	drawPinned(t, "packet\n0-7: \"Type\"\n8-15: \"Code\"\n16-31: \"Checksum\"\ntitle ICMP", 0, `
0       7      15              31
┌───────┬───────┬───────────────┐
│ Type  │ Code  │   Checksum    │
└───────┴───────┴───────────────┘

              ICMP
`)
	fitsCorpus(t, "packet", 80)
	fitsCorpus(t, "packet", 40)
}
