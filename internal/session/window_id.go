package session

// shortWindowID renders a window id the way list-windows does, so an error
// message and a listing name the same pane the same way.
func shortWindowID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
