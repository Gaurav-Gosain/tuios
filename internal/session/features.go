package session

import "slices"

// Feature names one thing a client and a daemon can do on the wire beyond
// what the protocol version promises. Each side lists the features it has in
// the handshake (HelloPayload.Features, WelcomePayload.Features), and a
// feature is on for a connection only when both listed it.
//
// gob leaves a field the peer does not know empty, so a peer from a build
// before a feature lists nothing for it, and the feature stays off. That is
// what makes a new feature safe for both pairings of mixed versions without a
// field of its own.
//
// Each feature is a constant in this file, with a doc line that says what it
// changes on the wire, and is in knownFeatures. The lint
// TestFeatureNamesAreDeclaredInFeaturesGo holds every Has call to them.
type Feature string

// knownFeatures is every feature this build has.
var knownFeatures []Feature

// featureNames is knownFeatures as the handshake sends them.
func featureNames() []string {
	names := make([]string, len(knownFeatures))
	for i, f := range knownFeatures {
		names[i] = string(f)
	}
	return names
}

// FeatureSet is the features both sides of a connection listed.
type FeatureSet struct {
	on []Feature
}

// negotiateFeatures keeps the features of knownFeatures that the peer listed.
// A name this build does not know is ignored.
func negotiateFeatures(peer []string) FeatureSet {
	var set FeatureSet
	for _, f := range knownFeatures {
		if slices.Contains(peer, string(f)) {
			set.on = append(set.on, f)
		}
	}
	return set
}

// Has reports whether both sides listed f.
func (s FeatureSet) Has(f Feature) bool { return slices.Contains(s.on, f) }

// HasFeature reports whether this client and the daemon both listed f in the
// handshake.
func (c *TUIClient) HasFeature(f Feature) bool { return c.features.Has(f) }
