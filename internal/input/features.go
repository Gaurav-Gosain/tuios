package input

// The action handlers of the features tuios-slim leaves out are registered
// by registerFeatureActions, which features_full.go defines and
// features_slim.go leaves empty. A key bound to one of those actions then
// does what an unbound key does in tuios-slim.
//
// It is a function and not a list each feature appends to from init:
// globalDispatcher is built during package variable initialization, which
// runs before any init function.
