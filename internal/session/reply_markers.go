package session

const replyMarkerGuidance = "Mark the state of your work with a reply marker in double square brackets, beside the thing it marks. " +
	"Use these words. A problem: [[Failed]], [[Blocked]], [[Broken]]. For the person: [[Needs you]], [[Unverified]], [[Skipped]], [[Pending]]. " +
	"Settled: [[Done]], [[Passed]], [[Observed]], [[Recommended]]. A consequence: [[Destructive]], [[Irreversible]], [[Costs money]]. " +
	"The kind of work: [[Investigate]], [[Implement]]. Ownership: [[Owned]], [[Shared]]. Context: [[New]], [[Changed]], [[Inferred]], [[Decision]]. " +
	"Add a short detail after a colon, as in [[Failed: 3 tests]]. Keep a marker under 32 characters. Do not put a marker inside code. " +
	"Start a reply, and each part of a long one, with a bold lead label such as **Finding:**, **Result:**, **Cause:**, **Evidence:**, **Risk:**, **Plan:** or **Next:**. " +
	"Do not type icons. The tuios agents view draws each marker as a coloured pill and adds the icons. " +
	"It draws a fenced mermaid block as a diagram and a fenced tml block as a static TML picture."

var replyMarkerFlags = map[string]string{
	"claude-code": "--append-system-prompt",
}

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
