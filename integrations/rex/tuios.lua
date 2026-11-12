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

-- M.version is the version of this file. tuios integration status compares it
-- with the one the installed tuios ships.
M.version = 2

-- The Rex block kind that runs a terminal.
local TERMINAL = "com.superlogical.terminal"

-- The default options of setup.
local defaults = {
  -- The tuios program. A name without a slash is looked up in PATH, then in
  -- the folders tuios is installed to (see install_dirs).
  bin = "tuios",
  -- The agent start_agent starts when the action gets no harness.
  harness = "claude",
  -- Keys to bind, by action name. Empty by default: Rex's own keys stay
  -- as they are until you choose some.
  keys = {},
}

local opts = {}

-- M.env is how program reads the environment and the file system. The test
-- replaces it with a stub.
M.env = {
  getenv = os.getenv,
  -- exists says whether a file is there and can be read.
  exists = function(path)
    local f = io.open(path, "r")
    if f == nil then
      return false
    end
    f:close()
    return true
  end,
}

-- install_dirs are the folders tuios is installed to, in the order program
-- looks in them: scripts/install.sh, Homebrew on Apple silicon and on Intel,
-- go install, and Nix. Rex's server starts from the app, so its PATH often
-- has none of the folders a login shell adds.
local function install_dirs()
  local home = M.env.getenv("HOME") or ""
  local dirs = {}
  local function add(d)
    if d ~= nil and d ~= "" then
      dirs[#dirs + 1] = d
    end
  end
  if home ~= "" then
    add(home .. "/.local/bin")
  end
  add("/opt/homebrew/bin")
  add("/usr/local/bin")
  add(M.env.getenv("GOBIN"))
  local gopath = M.env.getenv("GOPATH")
  if gopath ~= nil and gopath ~= "" then
    add(gopath:match("^[^:]+") .. "/bin")
  end
  if home ~= "" then
    add(home .. "/go/bin")
    add(home .. "/.nix-profile/bin")
  end
  add("/etc/profiles/per-user/" .. (M.env.getenv("USER") or "") .. "/bin")
  add("/nix/var/nix/profiles/default/bin")
  add("/run/current-system/sw/bin")
  return dirs
end

-- M.program returns the tuios program to run: bin as given when it has a
-- slash, else the first folder in PATH and then in install_dirs that holds
-- it. When none does it raises an error that names the fix.
function M.program()
  local bin = opts.bin
  if bin:find("/", 1, true) then
    return bin
  end
  local looked = {}
  for d in (M.env.getenv("PATH") or ""):gmatch("[^:]+") do
    if M.env.exists(d .. "/" .. bin) then
      return d .. "/" .. bin
    end
  end
  for _, d in ipairs(install_dirs()) do
    looked[#looked + 1] = d
    if M.env.exists(d .. "/" .. bin) then
      return d .. "/" .. bin
    end
  end
  error(bin .. " is not in Rex's PATH or in " .. table.concat(looked, ", ")
    .. ". Give its full path in init.lua: require(\"tuios\").setup{ bin = \"/full/path/tuios\" }."
    .. " Run 'command -v tuios' in a shell to find it.", 0)
end

-- run runs the tuios CLI with args and returns its result, or raises an
-- error with what it printed.
local function run(args)
  local argv = { M.program() }
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
  return nil, "The focused Rex pane does not show a tuios session on this machine. Focus a pane that runs tuios, or run tuios in it."
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
      local argv = { "start-agent", args.harness or opts.harness, "-s", s, "--focus", "--no-wait", "--print-id" }
      if args.name ~= nil and args.name ~= "" then
        argv[#argv + 1] = "--name"
        argv[#argv + 1] = args.name
      end
      if args.prompt ~= nil and args.prompt ~= "" then
        argv[#argv + 1] = "--prompt"
        argv[#argv + 1] = args.prompt
      end
      -- --no-wait answers once the agent runs, well inside the 30 seconds
      -- Rex gives an action, so a failure reaches the person as tuios's own
      -- message. The prompt is typed once the agent is ready.
      local r = run(argv)
      return { session = s, window = (r.stdout or ""):match("^%s*(%S+)") }
    end,
  },
  {
    name = "tuios_check",
    title = "tuios: Check the Rex Integration",
    description = "Say which tuios the actions run, its version, and the session of the focused pane.",
    keywords = "tuios check doctor integration path",
    run = function(ctx, args)
      local out = { tuios = M.program(), lua_version = M.version }
      local r = run({ "--version" })
      out.version = (r.stdout or ""):match("^%s*([^\n]-)%s*\n") or (r.stdout or ""):match("^%s*(.-)%s*$")
      local ok, s, why = pcall(M.session_of, ctx)
      if ok then
        out.session, out.session_note = s, why
      else
        out.session_note = tostring(s)
      end
      -- Whether Start an Agent would find its agent: the daemon answers for
      -- the PATH start-agent looks on, its own when Rex's lacks it.
      out.agent = opts.harness
      local rok, r = pcall(run, { "call", "agent-readiness", rex.json.encode({ harnesses = { opts.harness } }) })
      if not rok then
        out.agent_note = tostring(r)
      else
        local ans = rex.json.decode(r.stdout)
        for _, a in ipairs((type(ans) == "table" and ans.agents) or {}) do
          out.agent_installed = a.installed == true
        end
        if out.agent_installed == nil then
          out.agent_note = "tuios does not know a harness named " .. tostring(opts.harness)
        end
      end
      return out
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
