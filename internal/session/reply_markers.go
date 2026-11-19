package session

// Reply markers are short labels an agent writes in its reply, such as
// [[Done]] or [[Failed: 3 tests]], which the agents view draws as pills (see
// internal/app/cockpit_markers.go). With [agents] reply_markers on,
// start-agent tells the agents it starts about them, through the flag a
// harness takes an added system prompt with. A harness with no such flag
// starts as it would with the option off.

// replyMarkerGuidance is what an agent is told. It is plain text, the same for
// every harness.
const replyMarkerGuidance = "Mark the state of your work with a reply marker in double square brackets, beside the thing it marks. " +
	"Use these words. A problem: [[Failed]], [[Blocked]], [[Broken]]. For the person: [[Needs you]], [[Unverified]], [[Skipped]], [[Pending]]. " +
	"Settled: [[Done]], [[Passed]], [[Observed]], [[Recommended]]. A consequence: [[Destructive]], [[Irreversible]], [[Costs money]]. " +
	"The kind of work: [[Investigate]], [[Implement]]. Ownership: [[Owned]], [[Shared]]. Context: [[New]], [[Changed]], [[Inferred]], [[Decision]]. " +
	"Add a short detail after a colon, as in [[Failed: 3 tests]]. Keep a marker under 32 characters. Do not put a marker inside code. " +
	"Start a reply, and each part of a long one, with a bold lead label such as **Finding:**, **Result:**, **Cause:**, **Evidence:**, **Risk:**, **Plan:** or **Next:**. " +
	"Do not type icons. The tuios agents view draws each marker as a coloured pill and adds the icons."

// replyMarkerFlags are the flags that add to a harness's system prompt, by
// harness id. Only a flag that appends is listed: one that replaces the
// system prompt would take the harness's own away.
var replyMarkerFlags = map[string]string{
	"claude-code": "--append-system-prompt",
}

// replyMarkerArgs is the flag and the guidance for an agent start-agent
// starts, or nil when the option is off, the agent runs over a protocol, or
// its harness takes no added system prompt.
func (d *Daemon) replyMarkerArgs(harness, protocol string) []string {
	if !d.replyMarkers.Load() || protocol != "" {
		return nil
	}
	flag, ok := replyMarkerFlags[harness]
	if !ok {
		return nil
	}
	return []string{flag, replyMarkerGuidance}
}
