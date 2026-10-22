package text

import "testing"

func TestWardleyText(t *testing.T) {
	src := "wardley-beta\ntitle Tea\nanchor Shop [0.9, 0.8]\ncomponent Tea [0.5, 0.6]\ncomponent Water [0.2, 0.3]\nShop -> Tea\nTea -> Water; boiled\nevolve Water 0.7\n"
	drawPinned(t, src, 50, `
                       Tea
↑ Visibility
 │           ┆           ┆           ┆
 │           ┆           ┆           ┆  ◆ Shop
 │           ┆           ┆           ┆⡠⠊
 │           ┆           ┆          ⡠┆
 │           ┆           ┆        ⡠⠊ ┆
 │           ┆           ┆      ⡠⠊   ┆
 │           ┆           ┆   ⣀●⠊Tea  ┆
 │           ┆          ⢀┆⠔⠒⠉        ┆
 │           ┆     ⢀⣀⠤⠒⠉⠁┆           ┆
 │        Water ●┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄○ ┆
 │           ┆           ┆           ┆
 └────────────────────────────────────────────────
    Genesis  Custom Bui…   Product    Commodity
                                       Evolution →

Tea → Water: boiled
`)
	drawPinned(t, src, 30, `
Tea
Components
  Shop
    visibility 90
    evolution 80
  Tea
    visibility 50
    evolution 60
  Water
    visibility 20
    evolution 30
Links
  Shop → Tea
  Tea → Water: boiled
`)
	fitsCorpus(t, "wardley", 80)
	fitsCorpus(t, "wardley", 30)
}
