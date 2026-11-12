-- Tests tuios.lua against a stub of Rex's Lua API. Run it with check.sh.
--
-- How it could pass wrongly, written down first:
--   - The stub could accept a call real Rex refuses. It holds to the
--     documented shapes: rex.action needs name, title and run; a name has
--     only lowercase letters, digits and underscores; rex.bind takes a key
--     and a defined action.
--   - The session could come from the wrong client. The client list holds
--     the same pid with no session (a CLI call), another pid with a session,
--     and the focused block's client last.
--   - An action could build a command it never runs. Every check reads the
--     argv the stub's rex.exec or rex.exec.start received.

package.path = "./?.lua;" .. package.path

local actions, binds, execs, starts = {}, {}, {}, {}
local clients_json = "CLIENTS"
local clients = {}
local fg_pid = 42
local exec_fail = false
local start_fail = false
local agent_installed = true

rex = {
  action = function(a)
    assert(type(a.name) == "string" and a.name:match("^[a-z0-9_]+$"), "bad action name " .. tostring(a.name))
    assert(type(a.title) == "string" and a.title ~= "", "action without a title")
    assert(type(a.run) == "function", "action without run")
    actions[a.name] = a
  end,
  bind = function(key, name)
    assert(actions[name], "bind to an undefined action " .. tostring(name))
    binds[key] = name
  end,
  exec = setmetatable({
    start = function(argv, o)
      starts[#starts + 1] = { argv = argv, opts = o }
      return { pid = 99 }
    end,
  }, {
    __call = function(_, argv, o)
      execs[#execs + 1] = argv
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
      if argv[2] == "--version" then
        return { ok = true, code = 0, stdout = "tuios version 9.9.9\n", stderr = "" }
      end
      return { ok = true, code = 0, stdout = "", stderr = "" }
    end,
  }),
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
  local same = #got == #want
  for i = 1, #want do
    same = same and got[i] == want[i]
  end
  check(same, what .. ": got {" .. table.concat(got, " ") .. "}")
end

local tuios = require("tuios")
tuios.setup{ bin = "/opt/bin/tuios", keys = { tuios_agents = "cmd+shift+a" } }

-- The actions and the key.
for _, name in ipairs({ "tuios_agents", "tuios_mail", "tuios_next_waiting", "tuios_start_agent", "tuios_check" }) do
  check(actions[name] ~= nil, "action " .. name .. " is defined")
  check(actions[name] and actions[name].category == "tuios", "action " .. name .. " is in the tuios category")
end
check(binds["cmd+shift+a"] == "tuios_agents", "the key from setup is bound")
check(not pcall(tuios.setup, { keys = { tuios_nope = "cmd+x" } }), "a key for an unknown action is refused")
tuios.setup{ bin = "/opt/bin/tuios" }

-- The session of the focused block's client.
clients = {
  { client_id = "c1", pid = 42, session = "" },
  { client_id = "c2", pid = 7, session = "other" },
  { client_id = "c3", pid = 42, session = "work" },
}
check(tuios.session_of({ block_id = "b1" }) == "work", "the session is the one the focused block's client shows")

-- Each client action runs in that client.
local ctx = { block_id = "b1" }
for name, action in pairs({ tuios_agents = "prefix_inbox", tuios_mail = "prefix_mail", tuios_next_waiting = "prefix_next_attention" }) do
  execs = {}
  local res = actions[name].run(ctx, {})
  check(res.session == "work", name .. " names the session")
  argv_is(execs[#execs], { "/opt/bin/tuios", "run-command", "-s", "work", "Action", action }, name)
end

-- start_agent: the default harness, then one with a name and a prompt. It
-- runs in the action, with --no-wait, so the person sees tuios's answer.
execs, starts = {}, {}
local res = actions.tuios_start_agent.run(ctx, {})
argv_is(execs[#execs], { "/opt/bin/tuios", "start-agent", "claude", "-s", "work", "--focus", "--no-wait", "--print-id" }, "start_agent with defaults")
check(res.window == "w-new", "start_agent returns the new pane: got " .. tostring(res.window))
check(#starts == 0, "start_agent no longer runs detached")
actions.tuios_start_agent.run(ctx, { harness = "codex", name = "review", prompt = "Review the diff." })
argv_is(execs[#execs], { "/opt/bin/tuios", "start-agent", "codex", "-s", "work", "--focus", "--no-wait", "--print-id", "--name", "review", "--prompt", "Review the diff." }, "start_agent with args")
start_fail = true
local sok, serr = pcall(actions.tuios_start_agent.run, ctx, {})
check(not sok and tostring(serr):find("nor on the daemon's PATH", 1, true), "a failed start reports tuios's own message: " .. tostring(serr))
start_fail = false

-- tuios_check names the program, its version and the session.
local c = actions.tuios_check.run(ctx, {})
check(c.tuios == "/opt/bin/tuios" and c.version == "tuios version 9.9.9" and c.session == "work", "tuios_check reports the program, version and session")
check(c.agent == "claude" and c.agent_installed == true, "tuios_check says the default agent is found")
agent_installed = false
c = actions.tuios_check.run(ctx, {})
check(c.agent_installed == false, "tuios_check says the default agent is not found")
agent_installed = true

-- A block that shows no tuios client.
fg_pid = 1234
local s, why = tuios.session_of(ctx)
check(s == nil and why:find("does not show a tuios session") and why:find("run tuios in it", 1, true), "a block without a tuios client has no session, and says what to do")
local c2 = actions.tuios_check.run(ctx, {})
check(c2.session == nil and tostring(c2.session_note):find("does not show a tuios session"), "tuios_check says the focused pane has no session")
local ok, err = pcall(actions.tuios_agents.run, ctx, {})
check(not ok and tostring(err):find("does not show a tuios session"), "the action says why it cannot run")

-- tuios failing.
fg_pid = 42
exec_fail = true
ok, err = pcall(actions.tuios_mail.run, ctx, {})
check(not ok and tostring(err):find("no client shows the session"), "a failed tuios call reports its message")

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
--     argv rex.exec and rex.exec.start received.
exec_fail = false
local real_env = tuios.env
local function stub_env(vars, files)
  tuios.env = {
    getenv = function(k) return vars[k] end,
    exists = function(p) return files[p] == true end,
  }
end
tuios.setup{}

stub_env({ HOME = "/home/u", PATH = "/usr/bin:/bin" }, { ["/home/u/.local/bin/tuios"] = true, ["/opt/homebrew/bin/tuios"] = true })
execs = {}
actions.tuios_mail.run(ctx, {})
argv_is(execs[#execs], { "/home/u/.local/bin/tuios", "run-command", "-s", "work", "Action", "prefix_mail" }, "a tuios outside PATH is found in ~/.local/bin")
execs = {}
actions.tuios_start_agent.run(ctx, {})
argv_is(execs[#execs], { "/home/u/.local/bin/tuios", "start-agent", "claude", "-s", "work", "--focus", "--no-wait", "--print-id" }, "start_agent finds tuios outside PATH")

stub_env({ HOME = "/home/u", PATH = "/usr/bin:/srv/bin" }, { ["/srv/bin/tuios"] = true, ["/home/u/.local/bin/tuios"] = true })
execs = {}
actions.tuios_agents.run(ctx, {})
check(execs[#execs][1] == "/srv/bin/tuios", "a tuios in PATH wins over an install folder: got " .. tostring(execs[#execs][1]))

stub_env({ HOME = "/home/u", USER = "u", PATH = "/usr/bin:/bin" }, {})
execs = {}
ok, err = pcall(actions.tuios_mail.run, ctx, {})
check(not ok and #execs == 0, "with no tuios anywhere nothing runs")
check(tostring(err):find("is not in Rex's PATH", 1, true) and tostring(err):find("/home/u/.local/bin", 1, true)
  and tostring(err):find('setup{ bin = "/full/path/tuios" }', 1, true), "the message names where it looked and the fix: " .. tostring(err))
execs = {}
ok, err = pcall(actions.tuios_start_agent.run, ctx, {})
check(not ok and #execs == 0 and tostring(err):find("setup{ bin", 1, true), "start_agent gives the same message")

stub_env({ HOME = "/home/u", PATH = "" }, {})
tuios.setup{ bin = "/elsewhere/tuios" }
execs = {}
actions.tuios_agents.run(ctx, {})
check(execs[#execs][1] == "/elsewhere/tuios", "a bin with a slash runs as given")
tuios.env = real_env

if failed > 0 then
  io.stderr:write(failed .. " checks failed\n")
  os.exit(1)
end
print("tuios_test.lua: all checks passed")
