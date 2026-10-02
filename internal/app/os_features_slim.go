//go:build slim

package app

// osFeatures is empty in tuios-slim, which has no screenshot panel, project
// tapes, Inbox or hosts settings. See os_features_full.go.
type osFeatures struct{} //nolint:unused // embedded in OS so both builds share one struct
