package i18n

// catalogs holds one table per language. English is the source of truth: every
// key exists here, and the other tables carry the keys they have translated.
//
// Keys are dotted and named after the place they are printed, not after the
// English they hold. The English lives only in this file, so a wording change
// is one edit here and no edit anywhere else, and a call site reads
// T("cli.term.unset.what") rather than carrying a sentence around.
var catalogs = map[Language]map[string]string{
	English:           english,
	SimplifiedChinese: simplifiedChinese,
}

// english is the source table. It is also the fallback: a key absent from the
// selected language renders from here.
var english = map[string]string{
	"cli.error.header": "ERROR",

	"cli.label.cause": "Most likely cause: ",
	"cli.label.fix":   "Fix: ",

	// Terminal capability checks, which run before the TUI takes the screen.
	"cli.term.not_terminal.what":  "Standard output is not a terminal, so the TUIOS interface cannot be displayed.",
	"cli.term.not_terminal.cause": "the command was run in a pipe, a redirect, or a non-interactive environment such as CI.",
	"cli.term.not_terminal.fix":   "run 'tuios attach' from an interactive terminal. For scripted use, drive the session with 'tuios send-keys' and 'tuios capture-pane' instead.",

	"cli.term.too_small.what":  "The terminal is %dx%d, which is too small to render a TUIOS session (minimum %dx%d).",
	"cli.term.too_small.cause": "the window, font size, or split pane leaves too few rows and columns for the dockbar and a usable window.",
	"cli.term.too_small.fix":   "resize the terminal or reduce the font size, then run this command again.",

	"cli.term.unset.what":  "TERM is not set, so TUIOS cannot tell what the terminal can render.",
	"cli.term.unset.cause": "the shell was started without a terminal type, which happens over bare ssh commands, in some CI runners, and inside minimal containers.",
	"cli.term.unset.fix":   "set a terminal type, for example 'TERM=xterm-256color tuios attach'.",

	"cli.term.dumb.what":  `TERM is "dumb", which has no cursor movement or styling, so TUIOS cannot draw its interface.`,
	"cli.term.dumb.cause": "the terminal reports no capabilities. Emacs shell buffers, some CI runners, and minimal containers do this.",
	"cli.term.dumb.fix":   "set a capable terminal type, for example 'TERM=xterm-256color tuios attach'.",

	// Reaching the daemon.
	"cli.daemon.too_many.what":  "The tuios daemon has too many connections.",
	"cli.daemon.too_many.cause": "other clients, commands or linked machines hold all of its connections.",
	"cli.daemon.too_many.fix":   "close some clients or commands, then run this command again.",

	"cli.daemon.dial.what":  "Could not open a control connection to the TUIOS daemon: %v.",
	"cli.daemon.dial.cause": "the daemon is running but rejected or dropped the connection, which usually means it is shutting down or is a different build.",
	"cli.daemon.dial.fix":   "run 'tuios kill-server', then run this command again.",

	// Verb calls.
	"cli.verb.failed":        "%s failed: %s.",
	"cli.verb.unknown":       "The running daemon does not know %s.",
	"cli.verb.unknown.cause": "the daemon is older than this tuios.",
	"cli.verb.unknown.fix":   "run 'tuios restart-server'. Saved sessions come back with new shells.",
	"cli.verb.renamed.cause": "the session has a new name, and %s takes the new name only.",
	"cli.verb.renamed.fix":   "run the command again with %q.",
	"cli.verb.no_hint.cause": "the daemon reported %s.",
	"cli.verb.call_fix":      "call the '%s' verb to see valid targets.",
	"cli.verb.run_fix":       "run '%s'.",

	// Session lookup.
	"cli.session.missing.what":                 "Session %q was not found.",
	"cli.session.missing.cause.not_restored":   "the daemon is running but has not restored it.",
	"cli.session.missing.cause.no_sessions":    "the daemon is running but holds no sessions.",
	"cli.session.missing.cause.no_match":       "the name does not match any live session.",
	"cli.session.missing.saved":                "It has saved state (%s) and can be brought back.",
	"cli.session.missing.fix.resurrect":        "run 'tuios resurrect %s' to restore it and attach.",
	"cli.session.missing.fix.create":           "run 'tuios new %s' to create it, or 'tuios resurrect' to see saved sessions.",
	"cli.session.missing.fix.list_or_create":   "run 'tuios ls' to list sessions, or 'tuios new %s' to create this one.",

	// Lines shared by several diagnostics.
	"cli.common.did_you_mean": "Did you mean %q?",
	"cli.common.sessions":     "Sessions: %s.",
	"cli.common.available":    "Available: %s.",
	"cli.common.accepted":     "Accepted values for %s: %s.",
	"cli.common.and_more":     "and %d more",
	"cli.common.this_param":   "this parameter",

	// Nouns a count is written with. They are keys rather than literals so the
	// counting code can pass one to Count, which knows whether the language
	// inflects them.
	"noun.window": "window",
}

// simplifiedChinese is the zh-CN table.
//
// It follows the printed layout rather than the English word order: the cause
// and the fix are separate lines under their own labels, so a clause that
// English writes mid-sentence is written here as a sentence of its own. The
// fix lines keep their commands inside straight quotes for the same reason the
// English does, so a reader can pick the command out of the prose.
var simplifiedChinese = map[string]string{
	"cli.error.header": "错误",

	"cli.label.cause": "最可能的原因：",
	"cli.label.fix":   "解决办法：",

	"cli.term.not_terminal.what":  "标准输出不是终端，无法显示 TUIOS 界面。",
	"cli.term.not_terminal.cause": "命令是在管道、重定向或 CI 这类非交互环境中运行的。",
	"cli.term.not_terminal.fix":   "请在交互式终端里运行 'tuios attach'。若要以脚本驱动，改用 'tuios send-keys' 和 'tuios capture-pane'。",

	"cli.term.too_small.what":  "终端尺寸为 %dx%d，太小，无法渲染 TUIOS 会话（最小 %dx%d）。",
	"cli.term.too_small.cause": "窗口、字号或分屏留出的行列数不足以容纳停靠栏和一个可用窗格。",
	"cli.term.too_small.fix":   "请把终端调大或减小字号，然后重新运行此命令。",

	"cli.term.unset.what":  "没有设置 TERM，TUIOS 无法判断终端能渲染什么。",
	"cli.term.unset.cause": "启动 shell 时没有指定终端类型，裸 ssh 命令、部分 CI 环境和精简容器里会出现这种情况。",
	"cli.term.unset.fix":   "请设置终端类型，例如 'TERM=xterm-256color tuios attach'。",

	"cli.term.dumb.what":  "TERM 的值是 \"dumb\"，不支持光标移动和样式，TUIOS 无法绘制界面。",
	"cli.term.dumb.cause": "终端报告自己没有任何能力。Emacs 的 shell buffer、部分 CI 环境和精简容器会这样。",
	"cli.term.dumb.fix":   "请改设一个可用的终端类型，例如 'TERM=xterm-256color tuios attach'。",

	"cli.daemon.too_many.what":  "tuios 守护进程的连接数已满。",
	"cli.daemon.too_many.cause": "其他客户端、命令或已连接的机器占满了全部连接。",
	"cli.daemon.too_many.fix":   "请关掉一些客户端或命令，然后重新运行此命令。",

	"cli.daemon.dial.what":  "无法与 TUIOS 守护进程建立控制连接：%v。",
	"cli.daemon.dial.cause": "守护进程正在运行，但拒绝或断开了这次连接，通常意味着它正在关闭，或者是另一个构建版本。",
	"cli.daemon.dial.fix":   "请先运行 'tuios kill-server'，然后重新运行此命令。",

	"cli.verb.failed":        "%s 失败：%s。",
	"cli.verb.unknown":       "正在运行的守护进程不认识 %s。",
	"cli.verb.unknown.cause": "守护进程比当前的 tuios 旧。",
	"cli.verb.unknown.fix":   "请运行 'tuios restart-server'。已保存的会话会以新的 shell 恢复。",
	"cli.verb.renamed.cause": "该会话已经改名，%s 只接受新名字。",
	"cli.verb.renamed.fix":   "请用 %q 重新运行此命令。",
	"cli.verb.no_hint.cause": "守护进程报告了 %s。",
	"cli.verb.call_fix":      "请调用 '%s' 动词查看可用的目标。",
	"cli.verb.run_fix":       "请运行 '%s'。",

	"cli.session.missing.what":               "找不到会话 %q。",
	"cli.session.missing.cause.not_restored": "守护进程正在运行，但还没有恢复它。",
	"cli.session.missing.cause.no_sessions":  "守护进程正在运行，但其中没有任何会话。",
	"cli.session.missing.cause.no_match":     "这个名字没有对应的活动会话。",
	"cli.session.missing.saved":              "它有已保存的状态（%s），可以恢复。",
	"cli.session.missing.fix.resurrect":      "请运行 'tuios resurrect %s' 恢复并接入。",
	"cli.session.missing.fix.create":         "请运行 'tuios new %s' 创建它，或运行 'tuios resurrect' 查看已保存的会话。",
	"cli.session.missing.fix.list_or_create": "请运行 'tuios ls' 列出会话，或运行 'tuios new %s' 创建这一个。",

	"cli.common.did_you_mean": "你是指 %q？",
	"cli.common.sessions":     "会话：%s。",
	"cli.common.available":    "可用项：%s。",
	"cli.common.accepted":     "%s 可接受的取值：%s。",
	"cli.common.and_more":     "另有 %d 个",
	"cli.common.this_param":   "该参数",

	"noun.window": "窗口",
}
