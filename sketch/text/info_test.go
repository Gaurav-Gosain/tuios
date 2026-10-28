package text

import "testing"

func TestInfoText(t *testing.T) {
	drawPinned(t, "info\ntitle About this build", 0, `
About this build
  Pele v1.12.0
`)
	drawPinned(t, "info\ntitle About this build", 12, `
 About this
   build
Pele v1.12.0
`)
	fitsCorpus(t, "info", 80)
	fitsCorpus(t, "info", 20)
}
