package text

import "testing"

func TestRadarText(t *testing.T) {
	drawPinned(t, "radar-beta\naxis a[\"Speed\"], b[\"Power\"], c[\"Range\"]\ncurve x[\"Car\"]{4, 2, 3}\ncurve y[\"Bike\"]{2, 1, 5}\nmax 5", 0, `
from 0 to 5

Speed
  Car  ━━━━━━━━━━━━━━━━━━━━━━━━······ 4
  Bike ━━━━━━━━━━━━·················· 2

Power
  Car  ━━━━━━━━━━━━·················· 2
  Bike ━━━━━━························ 1

Range
  Car  ━━━━━━━━━━━━━━━━━━············ 3
  Bike ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 5
`)
	fitsCorpus(t, "radar", 80)
	fitsCorpus(t, "radar", 40)
}
