package session

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// DaemonConfigFromUser maps the parts of the user's config file the daemon
// owns onto the daemon's own config: the [daemon] section, the [hosts] table
// and the hooks the daemon fires.
//
// Every starter calls it: `tuios daemon`, and the SSH and web servers when
// they start a daemon in-process. One mapping keeps them agreeing: a
// hand-written subset per starter lets a daemon started by `tuios ssh` run
// with no agent detection settings and no hosts while the same file gives both
// to a daemon started by `tuios attach`. The fields the starter owns (Version,
// Foreground, LogFile, DisableAutoRestore) stay the starter's to fill.
//
// The log level is applied here as well, when nothing set it first: a flag or
// the environment wins over the file. It is a process global, so it is set
// once whoever starts the daemon.
//
// TestEveryDaemonKeyReachesTheDaemon holds this to every key in the section.
func DaemonConfigFromUser(uc *config.UserConfig) *DaemonConfig {
	cfg := &DaemonConfig{}
	cfg.setTimings(uc)
	if uc == nil {
		cfg.Approvals = ApprovalPolicyFromConfig(config.ApprovalsConfig{})
		return cfg
	}
	if GetDebugLevel() == DebugOff && uc.Daemon.LogLevel != "" {
		SetDebugLevel(ParseDebugLevel(uc.Daemon.LogLevel))
	}
	cfg.AgentsOff = !uc.Agents.On()
	cfg.MachineOfferOff = !uc.Notifications.MachineOfferOn()
	cfg.AgentBinaries = uc.Daemon.AgentBinaries
	cfg.RespondFromShell = uc.Daemon.RespondFromShell
	cfg.ResumeAgents = uc.Daemon.ResumeAgents
	cfg.WindowSize = uc.Daemon.WindowSize
	cfg.SingleClient = uc.Daemon.SingleClient
	cfg.SSHAgent = uc.Daemon.SSHAgent
	cfg.Display = uc.Daemon.Display
	cfg.KeepAwake = uc.Daemon.KeepAwake
	cfg.History = ResolveHistoryPolicy(uc.Daemon.PersistScrollback, uc.Daemon.PersistScrollbackLines, uc.Daemon.PersistScrollbackKB)
	cfg.Hosts = HostsFromConfig(uc)
	cfg.LinkPolicies = uc.Hosts
	if path, err := config.GetConfigPath(); err == nil {
		cfg.ConfigPath = path
	}
	cfg.ScrollbackLines = uc.Appearance.ScrollbackLines
	cfg.ScrollbackDropOnClear = !uc.Appearance.KeepsScrollbackOnClear()
	cfg.NewWindowInheritCwd = newWindowInheritCwd(uc)
	cfg.PreferredShell = uc.Appearance.PreferredShell
	cfg.ShellIntegration = uc.Shell.Integration
	cfg.ShellLogin = uc.Shell.Login
	cfg.StayOnEmptyWorkspace = !uc.Workspaces.ReturnsWhenEmpty()
	cfg.PasteBufferLimit, cfg.PasteBufferMaxBytes = uc.PasteBuffers.Resolved()
	cfg.PasteBuffersOff = cfg.PasteBufferLimit == 0
	cfg.DragAndDrop = uc.Panes.DragAndDrop
	cfg.WorktreePath = uc.Worktree.Path
	cfg.WorktreeSetup, cfg.WorktreeSetupAsync = uc.Worktree.Setup, uc.Worktree.SetupAsync
	cfg.ApplyUserHooks(uc)
	cfg.Approvals = ApprovalPolicyFromConfig(uc.Agents.Approvals)
	cfg.Permissions = PanePermissionsFromConfig(uc.Agents.Permissions)
	cfg.RecapTestPatterns = uc.Agents.Recap.Resolved().TestPatterns
	cfg.QueueMax = uc.Agents.Queue.MaxEntries()
	cfg.AgentSilentAfter = silentAfterFromConfig(uc.Agents)
	cfg.MailWakeOff = !uc.Agents.Mail.WakeOn()
	cfg.ChildQuestionsToPerson = !uc.Agents.Mail.QuestionsToParentOn()
	cfg.TaskAutoStageOff = !uc.Tasks.AutoStageOn()
	cfg.TaskLifecycleOff = !uc.Tasks.LifecycleOn()
	cfg.MachineCap = uc.Tasks.MachineCap
	cfg.Tasks = uc.Tasks
	cfg.OpenedCopyKeep = uc.Files.OpenCopyKeep()
	cfg.Checkpoints = uc.Agents.Checkpoints
	cfg.AgentPresets = uc.Agents.Presets
	cfg.ReplyMarkers = uc.Agents.ReplyMarkersOn()
	cfg.Plugins = uc.Plugins
	cfg.HerdrProtocol = uc.Agents.HerdrProtocol
	cfg.ACPVersion = uc.Agents.ACPVersionOr()
	cfg.Notify = uc.Notify
	return cfg
}

// HostsFromConfig turns the [hosts] config table into the daemon's host list.
// Nothing is validated here; the federation table does that and reports what it
// dropped, so the reason reaches 'tuios hosts' rather than only the log.
func HostsFromConfig(cfg *config.UserConfig) []federation.Host {
	if cfg == nil || len(cfg.Hosts) == 0 {
		return nil
	}
	out := make([]federation.Host, 0, len(cfg.Hosts))
	for name, h := range cfg.Hosts {
		if name == config.LinkPolicyDefaultName || (strings.TrimSpace(h.Addr) == "" && h.HasLinkPolicy()) {
			continue
		}
		out = append(out, federation.Host{
			Name:           name,
			Addr:           h.Addr,
			ConnectTimeout: time.Duration(h.ConnectTimeout) * time.Second,
			Command:        h.Command,
			SSHOptions:     h.SSHOptions,
			TailscaleLogin: h.TailscaleLogin,
		})
	}
	return out
}

func newWindowInheritCwd(uc *config.UserConfig) bool {
	return uc.Appearance.NewWindowInheritCwd == nil || *uc.Appearance.NewWindowInheritCwd
}

func (cfg *DaemonConfig) setTimings(uc *config.UserConfig) {
	uc = config.WithEnv(uc)
	if uc == nil {
		return
	}
	cfg.AgentAutoDetect = uc.Daemon.AgentAutoDetect
	cfg.AgentDetectInterval = optionSeconds(uc, "daemon.agent_detect_seconds")
	cfg.AgentStallTimeout = optionSeconds(uc, "daemon.agent_stall_seconds")
	cfg.SubagentFade = optionSeconds(uc, "daemon.subagent_fade_seconds")
	cfg.CleanupEvery = optionSeconds(uc, "daemon.cleanup_seconds")
	cfg.PRPollEvery = optionSeconds(uc, "daemon.pr_poll_seconds")
	cfg.TransferKeep = optionSeconds(uc, "daemon.transfer_keep_seconds")
	cfg.DispatchReady = optionSeconds(uc, "daemon.dispatch_ready_seconds")
	cfg.SaveEvery = optionSeconds(uc, "daemon.save_seconds")
	cfg.ExecTimeout = optionSeconds(uc, "daemon.exec_timeout_seconds")
	cfg.AskWait = optionSeconds(uc, "agents.ask_wait_seconds")
	cfg.UndoWindow = optionSeconds(uc, "agents.undo_window_seconds")
	cfg.DropKeepAge = time.Duration(config.IntValue(uc, "files.drop_keep_hours")) * time.Hour
	cfg.DropKeepBytes = int64(config.IntValue(uc, "files.drop_keep_mb")) << 20
	cfg.HostHeartbeat = optionSeconds(uc, "daemon.host_heartbeat_seconds")
	cfg.HostHeartbeatTimeout = optionSeconds(uc, "daemon.host_heartbeat_timeout_seconds")
	cfg.HostCallTimeout = optionSeconds(uc, "daemon.host_call_timeout_seconds")
}
