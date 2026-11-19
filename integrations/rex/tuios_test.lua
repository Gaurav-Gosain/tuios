-- Tests tuios.lua against a stub of Rex's Lua API. Run it with check.sh.
--
-- How it could pass wrongly, written down first:
--   - The stub could accept a call real Rex refuses. It holds to the
--     documented shapes: rex.action needs name, title and run; a name has
--     only lowercase letters, digits and underscores, and is defined once;
--     rex.bind takes a key and a defined action.
--   - The session could come from the wrong client. The client list holds
--     the same pid with no session (a CLI call), another pid with a session,
--     and the focused block's client last.
--   - An action could build a command it never runs. Every check reads the
--     argv the stub's rex.exec received.
--   - The actions could still be a list in Lua: the stub's tuios answers
--     actions --json with a list, and a second load with one more entry must
--     define it, with no change to tuios.lua.
--   - setup could raise at Rex's config load, which costs the person every
--     line of their init.lua: tuios missing, tuios too old for --json, an
--     answer that is not JSON, rex.exec raising, and a key for an action
--     that is not there each load with no error, and tuios_check says why.
--   - A fallback could hide the failure: with each failure above, Rex must
--     have the fallback actions and tuios_check an actions_note.
--   - A changed tuios could go unnoticed: tuios_check compares the list Rex
--     loaded with the one tuios gives now.

package.path = "./?.lua;" .. package.path

local actions, order, binds, execs = {}, {}, {}, {}
local clients_json = "CLIENTS"
local clients = {}
local fg_pid = 42
local exec_fail = false
local start_fail = false
local agent_installed = true
-- list_mode is how the stub's tuios answers actions --json: "ok" with
-- list_tbl, "old" (an unknown flag), "garbage", "raise" (rex.exec raises).
local list_mode = "ok"
local list_tbl
-- The answer side: what list-attention, peek-prompt and respond give.
-- respond_mode is "ok", "not_human" for a daemon without
-- respond_from_shell, or "changed" for a prompt that changed. Each refusal
-- is JSON on stdout, as tuios prints it with --json.
local att_tbl, peek_tbl, respond_mode = { items = {} }, {}, "ok"
-- ui_answers is the queue of what the person answers to each rex.ui call,
-- as { value, reason, detail }. ui_asked records each call.
local ui_answers, ui_asked = {}, {}
local icons = {}
for _, n in ipairs({ "document", "folder", "branch", "terminal", "code", "server", "database", "package", "globe", "link", "person", "tag", "clock", "star", "gear", "plus" }) do
  icons[n] = true
end
-- ui is the stub of rex.ui.input and rex.ui.pick. It holds to Custom UI:
-- a title, at most 2,000 items with unique ids, known icons, an answer that
-- is one of the ids, and failed without asking inside pcall or xpcall,
-- where Rex cannot pause the action.
local function ui(kind)
  return function(spec)
    assert(type(spec) == "table" and type(spec.title) == "string" and spec.title ~= "", "rex.ui." .. kind .. " without a title")
    local ids = {}
    if kind == "pick" then
      assert(type(spec.items) == "table" and #spec.items <= 2000, "rex.ui.pick without items")
      for _, it in ipairs(spec.items) do
        local id = type(it) == "table" and it.id or it
        assert(type(id) == "string" and id ~= "", "an item without an id")
        assert(not ids[id], "two items with the id " .. id)
        assert(type(it) ~= "table" or it.icon == nil or icons[it.icon], "an unknown icon " .. tostring(it.icon))
        ids[id] = true
      end
    end
    ui_asked[#ui_asked + 1] = { kind = kind, spec = spec }
    local tb = debug.traceback()
    if tb:find("'pcall'", 1, true) or tb:find("'xpcall'", 1, true) then
      return nil, "failed", "a prompt inside pcall"
    end
    local a = table.remove(ui_answers, 1)
    assert(a ~= nil, "rex.ui." .. kind .. " asked with no answer queued: " .. spec.title)
    if a[1] ~= nil and kind == "pick" and type(a[1]) == "string" and not spec.allow_other then
      assert(ids[a[1]], "the answer " .. a[1] .. " is not an item of " .. spec.title)
    end
    return a[1], a[2], a[3]
  end
end

local function row(name, title, category)
  return { name = name, title = title, category = category, description = "desc of " .. name }
end
local function base_list()
  return {
    actions = {
      row("new_window", "New window", "Window"),
      row("prefix_inbox", "Agents: what needs you", "Agents"),
      row("new_agent", "Agents: start an agent", "Agents"),
      row("prefix_mail", "Agents: mail for you", "Agents"),
      row("prefix_next_attention", "Agents: go to the oldest waiting", "Agents"),
      row("goto_picker", "Go to: an agent, session, workspace or pane", "Session"),
      row("prefix_inbox", "Agents: what needs you, again", "Agents"),
      row("Bad-Name", "Refused by Rex", "Window"),
      row("prefix_selection", "Copy mode", "Clipboard"),
    },
    harnesses = { { id = "claude-code", name = "Claude Code", command = "claude" }, { id = "codex", name = "Codex", command = "codex" } },
  }
end
list_tbl = base_list()

local function reset_rex()
  actions, order, binds = {}, {}, {}
end

rex = {
  action = function(a)
    assert(type(a.name) == "string" and a.name:match("^[a-z0-9_]+$"), "bad action name " .. tostring(a.name))
    assert(type(a.title) == "string" and a.title ~= "", "action without a title")
    assert(type(a.run) == "function", "action without run")
    assert(actions[a.name] == nil, "action defined twice: " .. a.name)
    actions[a.name] = a
    order[#order + 1] = a.name
  end,
  bind = function(key, name)
    assert(actions[name], "bind to an undefined action " .. tostring(name))
    binds[key] = name
  end,
  exec = function(argv, o)
    execs[#execs + 1] = argv
    if argv[2] == "actions" and argv[3] == "--json" then
      if list_mode == "raise" then
        error("exec is not allowed here")
      elseif list_mode == "old" then
        return { ok = false, code = 1, stdout = "", stderr = "Error: unknown flag: --json\n" }
      elseif list_mode == "garbage" then
        return { ok = true, code = 0, stdout = "GARBAGE", stderr = "" }
      end
      return { ok = true, code = 0, stdout = "LIST", stderr = "" }
    end
    if exec_fail then
      return { ok = false, code = 1, stdout = "", stderr = "no client shows the session" }
    end
    if argv[2] == "list-clients" then
      return { ok = true, code = 0, stdout = clients_json, stderr = "" }
    end
    if argv[2] == "start-agent" then
      if start_fail then
        return { ok = false, code = 1, stdout = "", stderr = "Claude Code is not installed: claude is not on the PATH the caller sent, nor on the daemon's PATH" }
      end
      return { ok = true, code = 0, stdout = "w-new\n", stderr = "" }
    end
    if argv[2] == "call" and argv[3] == "agent-readiness" then
      assert(argv[4] == "ENC", "readiness params not encoded by rex.json.encode")
      return { ok = true, code = 0, stdout = "READY", stderr = "" }
    end
    if argv[2] == "list-attention" then
      return { ok = true, code = 0, stdout = "ATT", stderr = "" }
    end
    if argv[2] == "peek-prompt" then
      return { ok = true, code = 0, stdout = "PEEK", stderr = "" }
    end
    if argv[2] == "respond" then
      if respond_mode == "not_human" then
        return { ok = false, code = 1, stdout = "REFUSED", stderr = "" }
      end
      if respond_mode == "changed" then
        return { ok = false, code = 1, stdout = "CHANGED", stderr = "" }
      end
      return { ok = true, code = 0, stdout = "RESP", stderr = "" }
    end
    if argv[2] == "--version" then
      return { ok = true, code = 0, stdout = "tuios version 9.9.9\n", stderr = "" }
    end
    return { ok = true, code = 0, stdout = "", stderr = "" }
  end,
  ui = { input = ui("input"), pick = ui("pick") },
  block = {
    call = function(creator, method, args)
      assert(creator == "com.superlogical.terminal" and method == "process", "unexpected block call")
      return { foreground = { pid = fg_pid, name = "tuios" } }
    end,
  },
  json = {
    decode = function(text)
      if text == "READY" then
        return { agents = { { harness = "claude-code", command = "claude", installed = agent_installed } } }
      end
      if text == "LIST" then
        return list_tbl
      end
      if text == "ATT" then
        return att_tbl
      end
      if text == "PEEK" then
        return peek_tbl
      end
      if text == "RESP" then
        return { window = peek_tbl.window, sent = "the answer", settled_by = "prompt", state = "working" }
      end
      if text == "REFUSED" then
        return { success = false, error = "respond failed: respond is for the person at an attached client.\nMost likely cause: ..." }
      end
      if text == "CHANGED" then
        return { success = false, error = "respond failed: nothing was pressed: the prompt changed\n" }
      end
      if text == "GARBAGE" then
        error("invalid JSON at byte 1")
      end
      assert(text == clients_json, "decode of unexpected text")
      return clients
    end,
    encode = function(v)
      assert(type(v) == "table" and v.harnesses and v.harnesses[1] == "claude", "unexpected encode")
      return "ENC"
    end,
  },
}

local failed = 0
local function check(cond, what)
  if not cond then
    failed = failed + 1
    io.stderr:write("FAIL: " .. what .. "\n")
  end
end
local function argv_is(got, want, what)
  got = got or {}
  local same = #got == #want
  for i = 1, #want do
    same = same and got[i] == want[i]
  end
  check(same, what .. ": got {" .. table.concat(got, " ") .. "}")
end

local tuios = require("tuios")
local ctx = { block_id = "b1" }
clients = {
  { client_id = "c1", pid = 42, session = "" },
  { client_id = "c2", pid = 7, session = "other" },
  { client_id = "c3", pid = 42, session = "work" },
}

-- The actions come from tuios actions --json.
tuios.setup{ bin = "/opt/bin/tuios", keys = { tuios_prefix_inbox = "cmd+shift+a", tuios_mail = "cmd+shift+m" } }
argv_is(execs[1], { "/opt/bin/tuios", "actions", "--json" }, "setup asks tuios for its actions")
argv_is(order, { "tuios_new_window", "tuios_prefix_inbox", "tuios_prefix_mail", "tuios_prefix_next_attention", "tuios_goto_picker",
  "tuios_prefix_selection", "tuios_start_agent", "tuios_answer", "tuios_check" }, "one action per entry, in its order, once, without new_agent or a bad name")
check(actions.tuios_prefix_inbox.title == "tuios: Agents: What Needs You", "title in Rex's style: " .. tostring(actions.tuios_prefix_inbox.title))
check(actions.tuios_goto_picker.title == "tuios: Go To: An Agent, Session, Workspace or Pane", "go-to title: " .. tostring(actions.tuios_goto_picker.title))
check(actions.tuios_new_window.description == "desc of new_window" and actions.tuios_new_window.category == "tuios", "description and category")
check(actions.tuios_prefix_selection.keywords:find("clipboard", 1, true) and actions.tuios_prefix_selection.keywords:find("prefix selection", 1, true), "keywords: " .. actions.tuios_prefix_selection.keywords)
check(binds["cmd+shift+a"] == "tuios_prefix_inbox", "a key by the new name binds")
check(binds["cmd+shift+m"] == "tuios_prefix_mail", "a key by a version 2 name binds to the same action")
check(tuios.title_case("Open the tuios menu: the tuios keys") == "Open the tuios Menu: The tuios Keys", "title_case keeps tuios and small words: " .. tuios.title_case("Open the tuios menu: the tuios keys"))

-- A new entry shows with no Lua change.
list_tbl.actions[#list_tbl.actions + 1] = row("layout_scrolling", "Layout: scrolling", "Layout")
reset_rex()
tuios.setup{ bin = "/opt/bin/tuios" }
check(actions.tuios_layout_scrolling ~= nil and actions.tuios_layout_scrolling.title == "tuios: Layout: Scrolling", "a new tuios action shows in Rex")

-- The session of the focused block's client.
check(tuios.session_of(ctx) == "work", "the session is the one the focused block's client shows")

-- Each listed action runs in that client.
for rexname, name in pairs({ tuios_prefix_inbox = "prefix_inbox", tuios_goto_picker = "goto_picker", tuios_prefix_selection = "prefix_selection" }) do
  execs = {}
  local res = actions[rexname].run(ctx, {})
  check(res.session == "work", rexname .. " names the session")
  argv_is(execs[#execs], { "/opt/bin/tuios", "run-command", "-s", "work", "Action", name }, rexname)
end

-- Start an Agent: the form, the sheet, and start-agent.
local form = actions.tuios_start_agent.args
check(type(form) == "table" and form.type == "object" and form.properties.harness.enum[1] == "claude" and form.properties.harness.enum[2] == "codex",
  "the harness choices come from tuios")
for _, f in ipairs({ "harness", "name", "prompt", "folder", "machine" }) do
  check(form.properties[f] ~= nil and form.properties[f].type == "string", "the form has " .. f)
end
execs = {}
local res = actions.tuios_start_agent.run(ctx, {})
argv_is(execs[#execs], { "/opt/bin/tuios", "run-command", "-s", "work", "Action", "new_agent" }, "start with no field opens the New agent sheet")
check(res.opened == "the New agent sheet", "start says it opened the sheet")
execs = {}
res = actions.tuios_start_agent.run(ctx, { harness = "codex", name = "review", prompt = "Review the diff.", folder = "/src/x" })
argv_is(execs[#execs], { "/opt/bin/tuios", "start-agent", "codex", "-s", "work", "--focus", "--no-wait", "--print-id",
  "--name", "review", "--prompt", "Review the diff.", "--cwd", "/src/x" }, "start with fields")
check(res.window == "w-new", "start returns the new pane: got " .. tostring(res.window))
actions.tuios_start_agent.run(ctx, { prompt = "Hi.", machine = "arch" })
argv_is(execs[#execs], { "/opt/bin/tuios", "start-agent", "claude", "-s", "arch:work", "--focus", "--no-wait", "--print-id", "--prompt", "Hi." },
  "a machine starts it there, the default harness when none is given")
start_fail = true
local sok, serr = pcall(actions.tuios_start_agent.run, ctx, { harness = "claude" })
check(not sok and tostring(serr):find("nor on the daemon's PATH", 1, true), "a failed start reports tuios's own message: " .. tostring(serr))
start_fail = false

-- tuios_check: the program, the version, the session, the list.
local c = actions.tuios_check.run(ctx, {})
check(c.tuios == "/opt/bin/tuios" and c.version == "tuios version 9.9.9" and c.session == "work", "tuios_check reports the program, version and session")
check(c.agent == "claude" and c.agent_installed == true, "tuios_check says the default agent is found")
check(c.actions == 10 and c.actions_current == true and c.actions_note == nil, "tuios_check says Rex has the list tuios gives: " .. tostring(c.actions))
list_tbl.actions[#list_tbl.actions + 1] = row("layout_off", "Layout: off", "Layout")
c = actions.tuios_check.run(ctx, {})
check(c.actions_current == false, "tuios_check sees a list that changed since Rex loaded it")
agent_installed = false
c = actions.tuios_check.run(ctx, {})
check(c.agent_installed == false, "tuios_check says the default agent is not found")
agent_installed = true
list_tbl = base_list()

-- A key for an action Rex does not have: no error, and the check names it.
reset_rex()
local ok = pcall(tuios.setup, { bin = "/opt/bin/tuios", keys = { tuios_nope = "cmd+x" } })
check(ok, "a key for an unknown action does not fail the config load")
check(next(binds) == nil, "the unknown key is not bound")
c = actions.tuios_check.run(ctx, {})
check(tostring(c.keys_note):find("tuios_nope", 1, true), "tuios_check names the unbound key: " .. tostring(c.keys_note))

-- Each way the list can fail: no error, the fallback, and a note.
local fallback = { "tuios_goto_picker", "tuios_prefix_inbox", "tuios_prefix_mail", "tuios_prefix_next_attention", "tuios_start_agent", "tuios_answer", "tuios_check" }
for _, case in ipairs({
  { mode = "old", note = "older than this tuios.lua" },
  { mode = "garbage", note = "gave no list" },
  { mode = "raise", note = "exec is not allowed here" },
}) do
  list_mode = case.mode
  reset_rex()
  ok = pcall(tuios.setup, { bin = "/opt/bin/tuios", keys = { tuios_agents = "cmd+shift+a" } })
  check(ok, case.mode .. ": setup does not raise")
  argv_is(order, fallback, case.mode .. ": the fallback actions")
  check(binds["cmd+shift+a"] == "tuios_prefix_inbox", case.mode .. ": a version 2 key still binds")
  check(actions.tuios_start_agent.args.properties.harness.enum == nil, case.mode .. ": no harness choices without the list")
  c = actions.tuios_check.run(ctx, {})
  check(tostring(c.actions_note):find(case.note, 1, true), case.mode .. ": tuios_check says why: " .. tostring(c.actions_note))
  -- With no list, start has no sheet to open, and starts the default.
  execs = {}
  actions.tuios_start_agent.run(ctx, {})
  argv_is(execs[#execs], { "/opt/bin/tuios", "start-agent", "claude", "-s", "work", "--focus", "--no-wait", "--print-id" }, case.mode .. ": start with no sheet")
end
list_mode = "ok"

-- A block that shows no tuios client.
reset_rex()
tuios.setup{ bin = "/opt/bin/tuios" }
fg_pid = 1234
local s, why = tuios.session_of(ctx)
check(s == nil and why:find("does not show a tuios session") and why:find("run tuios in it", 1, true), "a block without a tuios client has no session, and says what to do")
local c2 = actions.tuios_check.run(ctx, {})
check(c2.session == nil and tostring(c2.session_note):find("does not show a tuios session"), "tuios_check says the focused pane has no session")
local err
ok, err = pcall(actions.tuios_prefix_inbox.run, ctx, {})
check(not ok and tostring(err):find("does not show a tuios session"), "the action says why it cannot run")

-- tuios failing.
fg_pid = 42
exec_fail = true
ok, err = pcall(actions.tuios_prefix_mail.run, ctx, {})
check(not ok and tostring(err):find("no client shows the session"), "a failed tuios call reports its message")
exec_fail = false

-- A bare bin when Rex's PATH lacks tuios. Rex's server starts from the app,
-- so its PATH has none of the folders a login shell adds, such as
-- ~/.local/bin where scripts/install.sh puts tuios.
--
-- How it could pass wrongly, written down first:
--   - The lookup could find the real tuios of the machine that runs the
--     test: the environment and the file system are the stub's, and the
--     stub's PATH has no tuios.
--   - A folder in PATH could lose to an install folder: the case with both
--     wants the PATH one.
--   - The argv could still start with the bare name: each case reads the
--     argv rex.exec received.
local real_env = tuios.env
local function stub_env(vars, files)
  tuios.env = {
    getenv = function(k) return vars[k] end,
    exists = function(p) return files[p] == true end,
  }
end

stub_env({ HOME = "/home/u", PATH = "/usr/bin:/bin" }, { ["/home/u/.local/bin/tuios"] = true, ["/opt/homebrew/bin/tuios"] = true })
reset_rex()
execs = {}
tuios.setup{}
argv_is(execs[1], { "/home/u/.local/bin/tuios", "actions", "--json" }, "setup finds tuios outside PATH for the list")
execs = {}
actions.tuios_prefix_mail.run(ctx, {})
argv_is(execs[#execs], { "/home/u/.local/bin/tuios", "run-command", "-s", "work", "Action", "prefix_mail" }, "a tuios outside PATH is found in ~/.local/bin")
execs = {}
actions.tuios_start_agent.run(ctx, { harness = "claude" })
argv_is(execs[#execs], { "/home/u/.local/bin/tuios", "start-agent", "claude", "-s", "work", "--focus", "--no-wait", "--print-id" }, "start_agent finds tuios outside PATH")

stub_env({ HOME = "/home/u", PATH = "/usr/bin:/srv/bin" }, { ["/srv/bin/tuios"] = true, ["/home/u/.local/bin/tuios"] = true })
execs = {}
actions.tuios_prefix_inbox.run(ctx, {})
check(execs[#execs][1] == "/srv/bin/tuios", "a tuios in PATH wins over an install folder: got " .. tostring(execs[#execs][1]))

stub_env({ HOME = "/home/u", USER = "u", PATH = "/usr/bin:/bin" }, {})
reset_rex()
execs = {}
ok = pcall(tuios.setup, {})
check(ok and #execs == 0, "with no tuios anywhere setup loads and runs nothing")
argv_is(order, fallback, "with no tuios, the fallback actions")
c = nil
ok, err = pcall(actions.tuios_prefix_mail.run, ctx, {})
check(not ok and #execs == 0, "with no tuios anywhere nothing runs")
check(tostring(err):find("is not in Rex's PATH", 1, true) and tostring(err):find("/home/u/.local/bin", 1, true)
  and tostring(err):find('setup{ bin = "/full/path/tuios" }', 1, true), "the message names where it looked and the fix: " .. tostring(err))
ok, err = pcall(actions.tuios_start_agent.run, ctx, {})
check(not ok and #execs == 0 and tostring(err):find("setup{ bin", 1, true), "start_agent gives the same message")
ok, err = pcall(actions.tuios_check.run, ctx, {})
check(not ok and tostring(err):find("is not in Rex's PATH", 1, true), "tuios_check gives the same message")

stub_env({ HOME = "/home/u", PATH = "" }, {})
reset_rex()
tuios.setup{ bin = "/elsewhere/tuios" }
execs = {}
actions.tuios_prefix_inbox.run(ctx, {})
check(execs[#execs][1] == "/elsewhere/tuios", "a bin with a slash runs as given")
tuios.env = real_env

-- Answer an agent from Rex: tuios_answer.
--
-- How it could pass wrongly, written down first:
--   - It could answer what the person did not pick: respond must come after
--     both picks, with the answer picked, and a pick with no answer must
--     stop before respond, for every reason on every question.
--   - It could answer a prompt other than the one it showed: respond must
--     carry the prompt id of the peek, so tuios refuses a changed prompt.
--   - The stub could answer where Rex cannot ask: it fails a question asked
--     inside pcall, as Rex does, so these runs go through a coroutine, and
--     a run through pcall must fail.
--   - A reason could read as another: each message is checked for its own
--     words and its reason.
reset_rex()
tuios.setup{ bin = "/opt/bin/tuios" }
-- try runs an action as Rex does, outside pcall, and returns ok and what it
-- returned or raised.
local function try(f, ...)
  local co = coroutine.create(f)
  return coroutine.resume(co, ...)
end
local function find(argvs, verb)
  for i = #argvs, 1, -1 do
    if argvs[i][2] == verb then
      return argvs[i]
    end
  end
end
local approval_peek = {
  blocked = true, found = true, answerable = true, window = "w-aaaa", name = "review", kind = "approval",
  message = "Bash: make", prompt_id = "P1", options = {}, actions = { "approve", "approve_always", "deny" }, state = "needs_input",
}
local question_peek = {
  blocked = true, found = true, answerable = true, window = "w-bbbb", name = "fixer", kind = "question",
  message = "Which change?", prompt_id = "P2", options = { { n = 1, label = "Bug fix" }, { n = 2, label = "New feature" } },
  actions = { "deny", "choose", "text" }, state = "needs_input",
}
local now_ns = os.time() * 1e9
att_tbl = { items = {
  { kind = "approval", session = "work", window = "w-aaaa", name = "review", summary = "approve Bash: make", since = now_ns - 180e9 },
  { kind = "question", host = "build", session = "api", window = "w-bbbb", name = "fixer", summary = "Which change?", since = now_ns - 7200e9 },
  { kind = "approval", session = "work", window = "", name = "no pane" },
} }

execs, ui_asked, peek_tbl = {}, {}, approval_peek
ui_answers = { { "1" }, { "deny" } }
local aok, ares = try(actions.tuios_answer.run, ctx, {})
check(aok and ares.answered == true and ares.state == "working", "an approval is answered: " .. tostring(ares))
argv_is(find(execs, "list-attention"), { "/opt/bin/tuios", "list-attention", "--json", "--kind", "approval", "--kind", "plan", "--kind", "question" }, "only what waits on a prompt is listed")
local agents = ui_asked[1] and ui_asked[1].spec.items or {}
check(#agents == 2 and agents[1].title == "review" and agents[1].icon == "terminal" and agents[1].subtitle == "approve Bash: make"
  and agents[1].detail == "work  3m" and agents[2].detail == "build:api  2h" and agents[2].icon == "person",
  "the agents show name, question, session and wait, without the item that has no pane")
argv_is(find(execs, "peek-prompt"), { "/opt/bin/tuios", "peek-prompt", "--json", "-w", "w-aaaa", "-s", "work" }, "the picked agent's prompt is read")
local answers = ui_asked[2] and ui_asked[2].spec.items or {}
check(#answers == 3 and answers[1].id == "approve" and answers[1].title == "Allow" and answers[2].id == "approve_always" and answers[3].id == "deny",
  "an approval offers allow, allow always and deny")
check(ui_asked[2] and ui_asked[2].spec.title == "review: Bash: make", "the answer pick names the agent and its question: " .. tostring(ui_asked[2] and ui_asked[2].spec.title))
argv_is(execs[#execs], { "/opt/bin/tuios", "respond", "deny", "-w", "w-aaaa", "-s", "work", "--prompt-id", "P1", "--json" }, "the picked answer goes with the prompt id it showed")

execs, ui_asked, peek_tbl = {}, {}, question_peek
ui_answers = { { "2" }, { "choose:2" } }
aok, ares = try(actions.tuios_answer.run, ctx, {})
answers = ui_asked[2] and ui_asked[2].spec.items or {}
check(aok and #answers == 3 and answers[1].id == "choose:1" and answers[1].title == "Bug fix" and answers[2].id == "choose:2" and answers[3].id == "text",
  "a question offers its options as shown, and a typed answer")
argv_is(find(execs, "peek-prompt"), { "/opt/bin/tuios", "peek-prompt", "--json", "-w", "build:api:w-bbbb" }, "an agent on a linked machine is read there")
argv_is(execs[#execs], { "/opt/bin/tuios", "respond", "choose", "2", "-w", "build:api:w-bbbb", "--prompt-id", "P2", "--json" }, "an option is chosen by its number")

execs, ui_asked = {}, {}
ui_answers = { { "2" }, { "text" }, { "Use the fix." } }
aok, ares = try(actions.tuios_answer.run, ctx, {})
check(aok and ui_asked[3] and ui_asked[3].kind == "input", "a typed answer asks for the text")
argv_is(execs[#execs], { "/opt/bin/tuios", "respond", "text", "Use the fix.", "-w", "build:api:w-bbbb", "--prompt-id", "P2", "--json" }, "the typed answer is sent")

execs, ui_asked = {}, {}
ui_answers = { { "2" }, { "text" }, { "" } }
aok, ares = try(actions.tuios_answer.run, ctx, {})
check(aok and ares.answered == false and find(execs, "respond") == nil, "an empty typed answer sends nothing")

-- Every no-answer reason, on each question: nothing is sent.
for _, step in ipairs({ { pre = {} }, { pre = { { "2" } } }, { pre = { { "2" }, { "text" } } } }) do
  for _, case in ipairs({
    { why = "cancelled" },
    { why = "timeout", words = "Nobody answered the question in time." },
    { why = "no_client", words = "No Rex app is connected" },
    { why = "unsupported", words = "Update the Rex app." },
    { why = "busy", words = "already shows another question" },
    { why = "failed", detail = "the app went away", words = "Rex could not show the question. the app went away" },
  }) do
    execs, ui_asked = {}, {}
    ui_answers = {}
    for _, a in ipairs(step.pre) do
      ui_answers[#ui_answers + 1] = a
    end
    ui_answers[#ui_answers + 1] = { nil, case.why, case.detail }
    aok, ares = try(actions.tuios_answer.run, ctx, {})
    local at = "question " .. (#step.pre + 1) .. ", " .. case.why
    check(find(execs, "respond") == nil, at .. ": nothing is sent")
    if case.why == "cancelled" then
      check(aok and ares.answered == false and ares.reason == "cancelled", at .. ": stops with no error")
    else
      check(not aok and tostring(ares):find(case.words, 1, true) and tostring(ares):find("(" .. case.why .. ")", 1, true), at .. ": " .. tostring(ares))
      check(case.why == "busy" or tostring(ares):find("prompt_id", 1, true), at .. ": says how to answer with args")
    end
  end
end

-- Asked inside pcall, where Rex cannot pause: the stub refuses, as Rex does.
execs, ui_answers = {}, { { "1" } }
aok, ares = pcall(actions.tuios_answer.run, ctx, {})
check(not aok and tostring(ares):find("(failed)", 1, true) and find(execs, "respond") == nil, "the stub refuses a question inside pcall: " .. tostring(ares))
ui_answers = {}

-- Nothing waits, the agent moved on, the prompt has no answers.
att_tbl = { items = {} }
aok, ares = try(actions.tuios_answer.run, ctx, {})
check(not aok and tostring(ares):find("No agent waits for an answer.", 1, true), "nothing waits: " .. tostring(ares))
execs, ui_asked = {}, {}
peek_tbl = { blocked = false, state = "working", name = "review", window = "w-aaaa" }
aok, ares = try(actions.tuios_answer.run, ctx, { window = "w-aaaa", session = "work" })
check(not aok and tostring(ares):find("review no longer waits", 1, true) and #ui_asked == 0 and find(execs, "respond") == nil, "an agent that moved on: " .. tostring(ares))
peek_tbl = { blocked = true, found = true, answerable = false, reason = "the rule that reads this prompt declares no answers", name = "review", window = "w-aaaa" }
aok, ares = try(actions.tuios_answer.run, ctx, { window = "w-aaaa" })
check(not aok and tostring(ares):find("declares no answers. Answer it in tuios.", 1, true), "a prompt with no answers: " .. tostring(ares))

-- With args: the window skips the first question, an answer needs its
-- prompt id and skips both.
execs, ui_asked, peek_tbl = {}, {}, approval_peek
ui_answers = { { "approve" } }
aok, ares = try(actions.tuios_answer.run, ctx, { window = "review", session = "work" })
check(aok and #ui_asked == 1 and ui_asked[1].spec.items[1].id == "approve", "a window in args asks only the answer")
argv_is(execs[#execs], { "/opt/bin/tuios", "respond", "approve", "-w", "review", "-s", "work", "--prompt-id", "P1", "--json" }, "the answer to a window in args")
execs, ui_asked = {}, {}
aok, ares = try(actions.tuios_answer.run, ctx, { window = "review", session = "work", answer = "approve", prompt_id = "OLD" })
check(aok and #ui_asked == 0, "an answer in args asks nothing")
argv_is(execs[#execs], { "/opt/bin/tuios", "respond", "approve", "-w", "review", "-s", "work", "--prompt-id", "OLD", "--json" }, "an answer in args keeps its prompt id, so tuios can refuse a changed prompt")
execs = {}
aok, ares = try(actions.tuios_answer.run, ctx, { window = "review", answer = "approve" })
check(not aok and tostring(ares):find("prompt_id", 1, true) and #execs == 0, "an answer in args without its prompt id runs nothing: " .. tostring(ares))
aok, ares = try(actions.tuios_answer.run, ctx, { window = "build:api:w-bbbb", session = "ignored", answer = "choose", value = "1", prompt_id = "P2" })
argv_is(execs[#execs], { "/opt/bin/tuios", "respond", "choose", "1", "-w", "build:api:w-bbbb", "--prompt-id", "P2", "--json" }, "a HOST:SESSION:WINDOW in args carries its own session")

-- A daemon without respond_from_shell refuses Rex: the message names it.
respond_mode = "not_human"
aok, ares = try(actions.tuios_answer.run, ctx, { window = "review", answer = "deny", prompt_id = "P1" })
check(not aok and tostring(ares):find("respond_from_shell = true under [daemon]", 1, true) and tostring(ares):find("Nothing was sent.", 1, true), "a refusal names the setting: " .. tostring(ares))
respond_mode = "changed"
aok, ares = try(actions.tuios_answer.run, ctx, { window = "review", answer = "approve", prompt_id = "OLD" })
check(not aok and ares == "respond failed: nothing was pressed: the prompt changed", "a changed prompt gives tuios's own line: " .. tostring(ares))
respond_mode = "ok"
check(tuios.waited(now_ns - 30e9) == "30s" and tuios.waited(now_ns - 90000e9) == "1d", "waited reads seconds and days")

if failed > 0 then
  io.stderr:write(failed .. " checks failed\n")
  os.exit(1)
end
print("tuios_test.lua: all checks passed")
