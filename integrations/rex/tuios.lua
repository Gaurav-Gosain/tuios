-- tuios for Rex: actions in Rex's command palette, and optional keys, that
-- drive the tuios session shown in the focused Rex pane.
--
-- Put this file where Rex's init.lua can require it (next to init.lua), then:
--
--   require("tuios").setup{}
--
-- See README.md in this folder for the options and the keys.
--
-- Rex runs Lua 5.1. This file uses nothing newer.

local M = {}

-- The Rex block kind that runs a terminal.
local TERMINAL = "com.superlogical.terminal"

-- The default options of setup.
local defaults = {
  -- The tuios program. A name without a slash is looked up in PATH.
  bin = "tuios",
  -- The agent start_agent starts when the action gets no harness.
  harness = "claude",
  -- Keys to bind, by action name. Empty by default: Rex's own keys stay
  -- as they are until you choose some.
  keys = {},
}

local opts = {}

-- run runs the tuios CLI with args and returns its result, or raises an
-- error with what it printed.
local function run(args)
  local argv = { opts.bin }
  for _, a in ipairs(args) do
    argv[#argv + 1] = a
  end
  local r, err = rex.exec(argv, { timeout = 20 })
  if r == nil then
    error("tuios did not run: " .. tostring(err), 0)
  end
  if not r.ok then
    local why = r.stderr
    if why == nil or why == "" then
      why = r.stdout
    end
    error("tuios " .. args[1] .. " failed: " .. tostring(why), 0)
  end
  return r
end

-- foreground returns the pid of the focused block's foreground process, or
-- nil when the block is not a terminal.
local function foreground(ctx)
  local p = rex.block.call(TERMINAL, "process", { block_id = ctx.block_id })
  if p == nil or p.foreground == nil then
    return nil
  end
  return p.foreground.pid
end

-- M.session_of returns the name of the tuios session that the client in the
-- focused block shows, or nil and why not. The client is the block's
-- foreground process, so its pid is in tuios list-clients with the session
-- it shows. A block that runs ssh, or a shell, shows no local tuios client.
function M.session_of(ctx)
  local pid = foreground(ctx)
  if pid == nil then
    return nil, "the focused pane is not a terminal"
  end
  local r = run({ "list-clients", "--json" })
  local clients = rex.json.decode(r.stdout)
  if type(clients) ~= "table" then
    return nil, "tuios list-clients gave no list"
  end
  for _, c in ipairs(clients) do
    if c.pid == pid and type(c.session) == "string" and c.session ~= "" then
      return c.session
    end
  end
  return nil, "the focused pane does not show a tuios session on this machine"
end

-- session_or_error is session_of for an action: it raises the reason.
local function session_or_error(ctx)
  local s, why = M.session_of(ctx)
  if s == nil then
    error(why, 0)
  end
  return s
end

-- client_action runs a tuios keybinding action in the client that shows the
-- session, as its key would.
local function client_action(name)
  return function(ctx, args)
    local s = session_or_error(ctx)
    run({ "run-command", "-s", s, "Action", name })
    return { session = s }
  end
end

-- M.actions are the actions setup defines, in the order the palette lists
-- them.
M.actions = {
  {
    name = "tuios_agents",
    title = "tuios: Open the Agents View",
    description = "Open the agents view of the tuios session in the focused pane.",
    keywords = "tuios agents inbox",
    run = client_action("prefix_inbox"),
  },
  {
    name = "tuios_mail",
    title = "tuios: Open the Mail for You",
    description = "Open the agents view on the mail agents left for you.",
    keywords = "tuios mail messages inbox",
    run = client_action("prefix_mail"),
  },
  {
    name = "tuios_next_waiting",
    title = "tuios: Jump to the Agent That Waits",
    description = "Move the tuios focus to the oldest item that needs you.",
    keywords = "tuios attention needs input approval",
    run = client_action("prefix_next_attention"),
  },
  {
    name = "tuios_start_agent",
    title = "tuios: Start an Agent",
    description = "Start an agent in a new tuios pane and focus it.",
    keywords = "tuios agent claude codex start",
    args = { harness = "string?", prompt = "string?", name = "string?" },
    run = function(ctx, args)
      local s = session_or_error(ctx)
      local argv = { opts.bin, "start-agent", args.harness or opts.harness, "-s", s, "--focus" }
      if args.name ~= nil and args.name ~= "" then
        argv[#argv + 1] = "--name"
        argv[#argv + 1] = args.name
      end
      if args.prompt ~= nil and args.prompt ~= "" then
        argv[#argv + 1] = "--prompt"
        argv[#argv + 1] = args.prompt
      end
      -- start-agent returns when the agent is ready, which can take longer
      -- than the 30 seconds Rex gives an action, so it runs on its own.
      local h, err = rex.exec.start(argv, { detach = true })
      if h == nil then
        error("tuios did not run: " .. tostring(err), 0)
      end
      return { session = s }
    end,
  },
}

-- M.setup defines the actions, and binds the keys given in o.keys, such as
-- { tuios_agents = "cmd+shift+a" }. Call it from init.lua, at load.
function M.setup(o)
  opts = {}
  for k, v in pairs(defaults) do
    opts[k] = v
  end
  for k, v in pairs(o or {}) do
    opts[k] = v
  end
  local known = {}
  for _, a in ipairs(M.actions) do
    rex.action{
      name = a.name,
      title = a.title,
      description = a.description,
      category = "tuios",
      keywords = a.keywords,
      args = a.args,
      run = a.run,
    }
    known[a.name] = true
  end
  for name, key in pairs(opts.keys) do
    if not known[name] then
      error("tuios.setup: no action named " .. tostring(name), 2)
    end
    rex.bind(key, name)
  end
  return M
end

return M
