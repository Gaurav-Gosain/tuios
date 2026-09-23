package config

// PinPreV080Appearance writes into a the seven appearance values v0.8.0
// changed, as they were before it:
//
//	sidebar.enabled        false   (now true)
//	sidebar.position       left    (now right)
//	sidebar.width          28      (now 24)
//	dockbar_position       bottom  (now top)
//	window_title_position  bottom  (now top)
//	zoom_size              100     (now 95)
//	click_to_type          single  (now double)
//	scrollbar.style        thin    (now track)
//
// The Learn tour runs with them, because its lessons describe that screen.
// Tests whose fixtures were measured against that screen use it too, so
// they keep testing what they were written to test.
func PinPreV080Appearance(a *AppearanceConfig) {
	a.Sidebar.Enabled = new(false)
	a.Sidebar.Position = "left"
	a.Sidebar.Width = 28
	a.DockbarPosition = "bottom"
	a.WindowTitlePosition = "bottom"
	a.ZoomSize = 100
	a.ClickToType = ClickToTypeSingle
	a.Scrollbar.Style = ScrollbarStyleThin
}
