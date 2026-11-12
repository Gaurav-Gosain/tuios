-- tuios for Rex: actions in Rex's command palette, and optional keys, that
-- drive the tuios session shown in the focused Rex pane. The actions are the
-- rows of tuios's own command palette and the person's own [[actions]], read
-- from tuios actions --json each time Rex loads its config.
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
M.version = 4

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

-- fallback are the actions Rex gets when tuios cannot list its own: a tuios
-- older than this file, or none found. Each is a tuios action name and its
-- palette row.
local fallback = {
  { name = "goto_picker", title = "Go to: an agent, session, workspace or pane", category = "Session" },
  { name = "prefix_inbox", title = "Agents: what needs you", category = "Agents" },
  { name = "prefix_mail", title = "Agents: mail for you", category = "Agents" },
  { name = "prefix_next_attention", title = "Agents: go to the oldest waiting", category = "Agents" },
}

-- renamed maps the Rex names of version 2 of this file to the names the list
-- gives the same actions, so a key bound to an old name still binds.
M.renamed = {
  tuios_agents = "tuios_prefix_inbox",
  tuios_mail = "tuios_prefix_mail",
  tuios_next_waiting = "tuios_prefix_next_attention",
}

-- The words title_case leaves in lower case inside a title.
local small = {
  a = true, an = true, the = true, of = true, to = true, on = true, ["in"] = true, ["or"] = true,
  ["and"] = true, ["for"] = true, with = true, by = true, at = true, from = true, as = true, tuios = true,
}

-- M.title_case turns a tuios palette row ("Agents: what needs you") into a
-- title in the style of Rex's own ("Agents: What Needs You"). The first word,
-- and a word next to a colon, is always capitalised.
function M.title_case(s)
  return (s:gsub("()(%a[%w']*)", function(pos, w)
    local first = pos == 1 or s:sub(pos - 2, pos - 1) == ": " or s:sub(pos + #w, pos + #w) == ":"
    if (small[w:lower()] and not first) or w == "tuios" then
      return w
    end
    return w:sub(1, 1):upper() .. w:sub(2)
  end))
end

-- M.list asks tuios for its actions. It returns the decoded answer of tuios
-- actions --json, or nil and why not. It never raises, since setup runs at
-- Rex's config load and an error there would cost the person every line of
-- their init.lua.
function M.list()
  local ok, prog = pcall(M.program)
  if not ok then
    return nil, tostring(prog)
  end
  local rok, r, err = pcall(rex.exec, { prog, "actions", "--json" }, { timeout = 5 })
  if not rok then
    return nil, "tuios did not run: " .. tostring(r)
  end
  if r == nil then
    return nil, "tuios did not run: " .. tostring(err)
  end
  if not r.ok then
    local why = r.stderr
    if why == nil or why == "" then
      why = r.stdout
    end
    return nil, prog .. " actions --json failed, so that tuios is older than this tuios.lua. Update tuios, then run tuios integration install rex. It said: "
      .. tostring(why):gsub("%s+$", "")
  end
  local dok, list = pcall(rex.json.decode, r.stdout or "")
  if not dok or type(list) ~= "table" or type(list.actions) ~= "table" then
    return nil, prog .. " actions --json gave no list of actions"
  end
  return list
end

-- signature is the names of a list of actions, sorted, so tuios_check can
-- tell whether the list Rex loaded is the one tuios gives now.
local function signature(actions)
  local names = {}
  for _, a in ipairs(actions) do
    if type(a) == "table" and type(a.name) == "string" then
      names[#names + 1] = a.name
    end
  end
  table.sort(names)
  return table.concat(names, " ")
end

-- loaded is what setup read: the list's signature, how many actions it
-- defined from it, why it fell back, and the keys it could not bind.
local loaded = {}

-- start_args is the form of tuios_start_agent: a JSON Schema, with the
-- harnesses tuios knows as the choices for harness.
local function start_args(harnesses)
  local harness = { type = "string", description = "The agent to start, as you type it at a shell. Without it: " .. tostring(opts.harness) .. "." }
  local names = {}
  for _, h in ipairs(harnesses or {}) do
    if type(h) == "table" and type(h.command) == "string" and h.command ~= "" then
      names[#names + 1] = h.command
    end
  end
  if #names > 0 then
    harness.enum = names
  end
  return {
    type = "object",
    properties = {
      harness = harness,
      name = { type = "string", description = "The pane's name, which list-agents shows and -w takes." },
      prompt = { type = "string", description = "The first prompt, typed once the agent is ready." },
      folder = { type = "string", description = "The folder to start in. Without it: the focused tuios pane's folder." },
      machine = { type = "string", description = "A machine of tuios's [hosts] table to start it on." },
    },
  }
end

-- filled reports whether a form field was given.
local function filled(v)
  return v ~= nil and v ~= ""
end

-- start_agent is tuios_start_agent. With no field given and a tuios that has
-- the new_agent action, it opens tuios's own New agent sheet, which has every
-- field and checks them as the person types. Otherwise it runs start-agent
-- with --no-wait, which answers once the agent runs, well inside the 30
-- seconds Rex gives an action, so a failure reaches the person as tuios's
-- own message. The prompt is typed once the agent is ready.
local function start_agent(ctx, args)
  args = args or {}
  local s = session_or_error(ctx)
  if loaded.sheet and not (filled(args.harness) or filled(args.name) or filled(args.prompt) or filled(args.folder) or filled(args.machine)) then
    run({ "run-command", "-s", s, "Action", "new_agent" })
    return { session = s, opened = "the New agent sheet" }
  end
  local target = s
  if filled(args.machine) then
    target = args.machine .. ":" .. s
  end
  local harness = args.harness
  if not filled(harness) then
    harness = opts.harness
  end
  local argv = { "start-agent", harness, "-s", target, "--focus", "--no-wait", "--print-id" }
  if filled(args.name) then
    argv[#argv + 1] = "--name"
    argv[#argv + 1] = args.name
  end
  if filled(args.prompt) then
    argv[#argv + 1] = "--prompt"
    argv[#argv + 1] = args.prompt
  end
  if filled(args.folder) then
    argv[#argv + 1] = "--cwd"
    argv[#argv + 1] = args.folder
  end
  local r = run(argv)
  return { session = s, window = (r.stdout or ""):match("^%s*(%S+)") }
end

-- check is tuios_check: which tuios the actions run, its version, the
-- focused pane's session, whether Start an Agent finds its agent, and
-- whether the actions Rex loaded are the ones that tuios lists now.
local function check(ctx, args)
  local out = { tuios = M.program(), lua_version = M.version, actions = loaded.count or 0 }
  local r = run({ "--version" })
  out.version = (r.stdout or ""):match("^%s*([^\n]-)%s*\n") or (r.stdout or ""):match("^%s*(.-)%s*$")
  if loaded.note ~= nil then
    out.actions_note = loaded.note
  else
    local now = M.list()
    if now ~= nil then
      out.actions_current = signature(now.actions) == loaded.signature
    end
  end
  if loaded.keys ~= nil then
    out.keys_note = loaded.keys
  end
  if loaded.config_note ~= nil then
    out.config_note = loaded.config_note
  end
  if loaded.clash ~= nil then
    out.clash_note = loaded.clash
  end
  local ok, s, why = pcall(M.session_of, ctx)
  if ok then
    out.session, out.session_note = s, why
  else
    out.session_note = tostring(s)
  end
  -- Whether Start an Agent would find its agent: the daemon answers for
  -- the PATH start-agent looks on, its own when Rex's lacks it.
  out.agent = opts.harness
  local rok, rr = pcall(run, { "call", "agent-readiness", rex.json.encode({ harnesses = { opts.harness } }) })
  if not rok then
    out.agent_note = tostring(rr)
  else
    local ans = rex.json.decode(rr.stdout)
    for _, a in ipairs((type(ans) == "table" and ans.agents) or {}) do
      out.agent_installed = a.installed == true
    end
    if out.agent_installed == nil then
      out.agent_note = "tuios does not know a harness named " .. tostring(opts.harness)
    end
  end
  return out
end

-- M.rex_name is the Rex action name of a tuios action: tuios_<action>. A
-- user action, user:NAME, is tuios_user_<NAME> with each dash an underscore,
-- since a Rex action name has only lowercase letters, digits and
-- underscores. It is nil for a name Rex cannot take.
function M.rex_name(name)
  local own = name:match("^user:(.+)$")
  local rexname = "tuios_" .. name
  if own ~= nil then
    rexname = "tuios_user_" .. own:gsub("-", "_")
  end
  if rexname:match("^[a-z0-9_]+$") then
    return rexname
  end
  return nil
end

-- M.actions builds the actions setup defines from list, the answer of tuios
-- actions --json, or from the fallback when list is nil: one per tuios
-- action, then Start an Agent and the check. The new_agent action is not
-- one of its own: Start an Agent opens its sheet. The second result names
-- the actions left out because another one has their Rex name, such as
-- user:a-b after user:a_b.
function M.actions(list)
  local rows = fallback
  if list ~= nil then
    rows = list.actions
  end
  local out, seen, clash = {}, { tuios_start_agent = true, tuios_check = true }, {}
  for _, a in ipairs(rows) do
    local name = type(a) == "table" and type(a.name) == "string" and a.name or ""
    local rexname = name ~= "" and M.rex_name(name) or nil
    if rexname ~= nil and seen[rexname] then
      clash[#clash + 1] = name
    elseif rexname ~= nil and name ~= "new_agent" then
      seen[rexname] = true
      local title = type(a.title) == "string" and a.title ~= "" and a.title or name
      local category = type(a.category) == "string" and a.category or ""
      -- A user action's label is the person's own words, kept as written.
      if a.user ~= true then
        title = M.title_case(title)
      end
      out[#out + 1] = {
        name = rexname,
        title = "tuios: " .. title,
        description = type(a.description) == "string" and a.description or title,
        keywords = ("tuios " .. category:lower() .. " " .. name:gsub("[_:-]", " ")),
        run = client_action(name),
      }
    end
  end
  out[#out + 1] = {
    name = "tuios_start_agent",
    title = "tuios: Start an Agent",
    description = "Start an agent in a new tuios pane and focus it. With no field given, open tuios's New agent sheet.",
    keywords = "tuios agent start new claude codex",
    args = start_args(list and list.harnesses),
    run = start_agent,
  }
  out[#out + 1] = {
    name = "tuios_check",
    title = "tuios: Check the Rex Integration",
    description = "Say which tuios the actions run, its version, the session of the focused pane, and whether Rex has the actions that tuios lists.",
    keywords = "tuios check doctor integration path",
    run = check,
  }
  return out, clash
end

-- M.setup defines the actions, and binds the keys given in o.keys, such as
-- { tuios_prefix_inbox = "cmd+shift+a" }. Call it from init.lua, at load.
-- It does not raise when tuios is missing or old: Rex then has the fallback
-- actions, and tuios_check says why. A key for an action Rex does not have
-- is left unbound, and tuios_check names it.
function M.setup(o)
  opts = {}
  for k, v in pairs(defaults) do
    opts[k] = v
  end
  for k, v in pairs(o or {}) do
    opts[k] = v
  end
  loaded = {}
  local list, why = M.list()
  if list == nil then
    loaded.note = why
  else
    loaded.signature = signature(list.actions)
    for _, a in ipairs(list.actions) do
      if type(a) == "table" and a.name == "new_agent" then
        loaded.sheet = true
      end
    end
  end
  if list ~= nil and type(list.config_note) == "string" and list.config_note ~= "" then
    loaded.config_note = list.config_note
  end
  local known = {}
  local defined, clash = M.actions(list)
  if #clash > 0 then
    loaded.clash = "left out, since another action has the same Rex name: " .. table.concat(clash, ", ")
  end
  for _, a in ipairs(defined) do
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
  loaded.count = #defined
  local unbound = {}
  for name, key in pairs(opts.keys or {}) do
    local target = name
    if not known[target] and M.renamed[name] ~= nil then
      target = M.renamed[name]
    end
    if not known[target] then
      unbound[#unbound + 1] = tostring(name)
    else
      local ok, err = pcall(rex.bind, key, target)
      if not ok then
        unbound[#unbound + 1] = tostring(name) .. " (" .. tostring(err) .. ")"
      end
    end
  end
  if #unbound > 0 then
    table.sort(unbound)
    loaded.keys = "not bound, since Rex has no such tuios action: " .. table.concat(unbound, ", ")
  end
  return M
end

return M
