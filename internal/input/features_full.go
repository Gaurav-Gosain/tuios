//go:build !slim

package input

// registerFeatureActions registers the actions of the features tuios-slim
// leaves out. See features.go.
func registerFeatureActions(d *ActionDispatcher) {
	registerTapeActions(d)
}
