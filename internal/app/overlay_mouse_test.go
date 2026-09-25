package app

// renderSettingsHit renders the settings panel and records its hit geometry the
// way renderOverlays would, so the mouse routing can be exercised in a test.
func (m *OS) renderSettingsHit() {
	m.reconcileOverlayZOrder()
	content, geo, rows := m.renderSettings()
	_ = content
	x, y := m.overlayOrigin("settings", geo)
	m.OverlayHits = []overlayPanelHit{{Kind: "settings", OriginX: x, OriginY: y, Z: m.overlayZ("settings"), Geo: geo, Rows: rows}}
}

func (m *OS) settingsHit() overlayPanelHit { return m.OverlayHits[0] }
