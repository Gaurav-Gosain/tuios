package session

import (
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

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

const (
	// FeatureRemoteHistory: the client reads history lines of a pane it does
	// not hold from the daemon by number, with MsgReadHistory. Listed only by
	// a build whose emulator numbers its history. See pty_history.go.
	FeatureRemoteHistory Feature = "remote-history"
	// FeaturePTYFrames: the daemon sends a pane the client is behind on as
	// frames of its screen (MsgPTYFrame) instead of bytes, and the client
	// reports that it is behind with MsgPTYLag. On only with
	// FeatureRemoteHistory, which brings back the lines a frame does not
	// carry. See pty_frames.go.
	FeaturePTYFrames Feature = "pty-frames"
)

// knownFeatures is every feature this build has.
var knownFeatures = []Feature{FeatureRemoteHistory, FeaturePTYFrames}

// featureOn reports whether this build can do f. A feature that depends on
// the emulator backend is off where the backend lacks it.
func featureOn(f Feature) bool {
	switch f {
	case FeatureRemoteHistory:
		// remote_history=off stands for a peer that does not list it.
		return numbersHistory() && testknob.String("remote_history") != "off"
	case FeaturePTYFrames:
		// pty_frames=off stands for a peer that does not list it.
		return numbersHistory() && testknob.String("pty_frames") != "off"
	}
	return true
}

// featureNames is the features of knownFeatures this build can do, as the
// handshake sends them.
func featureNames() []string {
	var names []string
	for _, f := range knownFeatures {
		if featureOn(f) {
			names = append(names, string(f))
		}
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
		if featureOn(f) && slices.Contains(peer, string(f)) {
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
