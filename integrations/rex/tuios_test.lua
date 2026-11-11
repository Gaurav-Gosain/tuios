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
      assert(text == clients_json, "decode of unexpected text")
      return clients
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
for _, name in ipairs({ "tuios_agents", "tuios_mail", "tuios_next_waiting", "tuios_start_agent" }) do
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

-- start_agent: the default harness, then one with a name and a prompt.
starts = {}
actions.tuios_start_agent.run(ctx, {})
argv_is(starts[1].argv, { "/opt/bin/tuios", "start-agent", "claude", "-s", "work", "--focus" }, "start_agent with defaults")
check(starts[1].opts.detach == true, "start_agent runs detached")
actions.tuios_start_agent.run(ctx, { harness = "codex", name = "review", prompt = "Review the diff." })
argv_is(starts[2].argv, { "/opt/bin/tuios", "start-agent", "codex", "-s", "work", "--focus", "--name", "review", "--prompt", "Review the diff." }, "start_agent with args")

-- A block that shows no tuios client.
fg_pid = 1234
local s, why = tuios.session_of(ctx)
check(s == nil and why:find("does not show a tuios session"), "a block without a tuios client has no session")
local ok, err = pcall(actions.tuios_agents.run, ctx, {})
check(not ok and tostring(err):find("does not show a tuios session"), "the action says why it cannot run")

-- tuios failing.
fg_pid = 42
exec_fail = true
ok, err = pcall(actions.tuios_mail.run, ctx, {})
check(not ok and tostring(err):find("no client shows the session"), "a failed tuios call reports its message")

if failed > 0 then
  io.stderr:write(failed .. " checks failed\n")
  os.exit(1)
end
print("tuios_test.lua: all checks passed")
