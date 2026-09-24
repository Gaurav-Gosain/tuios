package input

import "github.com/Gaurav-Gosain/tuios/internal/listnav"

// listKey moves a list when key is one of the movement keys, and reports
// whether it was. Every list overlay reads its movement through here, so the
// arrows, ctrl+p and ctrl+n, home, end, page up and page down mean the same
// thing in each of them.
//
// letters adds k, j, g and G, for a list that has no filter to type into. A
// list with a filter keeps its letters for the filter. page is how many rows
// a page key moves.
func listKey(key string, letters bool, page int, move func(delta int)) bool {
	motion := listnav.Keys(key, letters)
	if motion == listnav.None {
		return false
	}
	move(listnav.Delta(motion, page))
	return true
}

// listPage is the page a list moves by when it has no measured height of its
// own to offer.
const listPage = 10
