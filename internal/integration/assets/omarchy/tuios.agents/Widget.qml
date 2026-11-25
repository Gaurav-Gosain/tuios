import QtQuick
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// tuios agents in the Omarchy bar. The widget runs `tuios bar-status --follow`,
// which prints one JSON line each time the state changes, and draws the last
// line. It does no work while nothing changes. See docs/STATUS_BAR.md in the
// tuios repository for the fields of a line.
BarWidget {
  id: root
  moduleName: "tuios.agents"

  readonly property var offState: ({ "class": "off", "text": "", "rows": [], "needs_you": 0, "mail": 0, "working": 0, "review": 0 })
  property var agentState: offState
  readonly property string stateClass: agentState["class"] || "off"
  readonly property string command: String(root.setting("command", "tuios"))
  // The tuios-menu script of this plugin, its path filled in at install.
  readonly property string menu: String(root.setting("menu", "tuios-menu"))
  readonly property var glyphs: ({
    "needs-you": "\uf0f3",
    "mail": "\uf0e0",
    "working": "\uf110",
    "review": "\uf00c",
    "idle": "\uf120",
    "error": "\uf071",
    "off": "\uf120"
  })

  visible: stateClass === "needs-you" || stateClass === "mail" || stateClass === "working" || stateClass === "review" || stateClass === "error"
    || (stateClass === "idle" && root.setting("showIdle", false) === true)
  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  function plural(n, one, many) {
    return n === 1 ? "1 " + one : n + " " + many
  }

  function kindWord(kind) {
    if (kind === "approval") return "Approval"
    if (kind === "plan") return "Plan"
    if (kind === "question" || kind === "ask") return "Question"
    if (kind === "errored") return "Error"
    return kind
  }

  function tooltip() {
    var s = root.agentState
    if (root.stateClass === "error") return String(s.tooltip || "")
    var lines = []
    if (s.needs_you > 0) {
      lines.push(plural(s.needs_you, "item needs you.", "items need you."))
      var rows = s.rows || []
      for (var i = 0; i < rows.length; i++) {
        var r = rows[i]
        var where = (r.host ? r.host + ":" : "") + r.session + (r.name ? "/" + r.name : "")
        lines.push(kindWord(r.kind) + "  " + where + (r.summary ? "  " + r.summary : ""))
      }
    }
    if (s.mail > 0) lines.push(plural(s.mail, "message for you.", "messages for you."))
    if (s.working > 0) lines.push(plural(s.working, "agent works.", "agents work."))
    if (s.review > 0) lines.push(plural(s.review, "turn to review.", "turns to review."))
    if (lines.length === 0) return root.stateClass === "off" ? "tuios is not running." : "Nothing needs you."
    return lines.join("\n")
  }

  Process {
    id: feed
    command: [root.command, "bar-status", "--follow"]
    running: true
    stdout: SplitParser {
      onRead: function(line) {
        try {
          root.agentState = JSON.parse(line)
        } catch (e) {
          root.agentState = root.offState
        }
      }
    }
    // bar-status --follow runs until it is stopped, and waits for a daemon
    // itself. It ends only when tuios is missing or was replaced, so it is
    // started again after a pause.
    onExited: function(exitCode) {
      root.agentState = root.offState
      restart.start()
    }
  }

  Timer {
    id: restart
    interval: 10000
    onTriggered: feed.running = true
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: root.glyphs[root.stateClass] || root.glyphs["off"]
    labelText: root.vertical ? "" : String(root.agentState.text || "")
    slotSize: Style.bar.statusSlot
    active: root.stateClass === "needs-you"
    tooltipText: root.tooltip()
    onPressed: function(mouseButton) {
      if (!root.bar) return
      root.bar.run(Util.shellQuote(root.menu) + (mouseButton === Qt.RightButton ? " sessions" : ""))
    }
  }
}
