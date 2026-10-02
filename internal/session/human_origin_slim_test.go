//go:build slim && (linux || darwin)

package session

// helperAttachHost: tuios-slim attaches to no session on another machine.
func helperAttachHost(string) string { return "attach-host is not in tuios-slim" }
