-- rex-load.lua DIR: loads DIR/tuios.lua as Rex's config load would, against
-- a stand-in for Rex's Lua API whose rex.exec runs the real programs, and
-- prints what setup defined, one line each:
--
--   ACTION <tab> name <tab> title <tab> category
--   ENUM <tab> harness command
--   EXEC <tab> argv joined by spaces
--   LOADED (setup returned) or SETUP-ERROR <tab> message
--   RAN <tab> name, or RUN-ERROR <tab> message, for REX_LOAD_RUN
--
-- With REX_LOAD_RUN=NAME and REX_LOAD_PID=PID it then runs the action NAME as
-- Rex would from its palette, with the focused block's foreground process
-- PID, so the action finds the session the tuios client of that pid shows.
--
-- TestRexActionsComeFromTuios runs it. tuios is found the way tuios.lua finds
-- it in Rex: PATH, then the install folders under HOME.

package.path = arg[1] .. "/?.lua;" .. package.path

-- decode is a small JSON decoder: enough for tuios actions --json.
local function decode(text)
  local pos = 1
  local function ws()
    pos = text:find("[^ \t\r\n]", pos) or (#text + 1)
  end
  local value
  local function str()
    local out = {}
    pos = pos + 1
    while true do
      local c = text:sub(pos, pos)
      if c == "" then
        error("unterminated string")
      elseif c == '"' then
        pos = pos + 1
        return table.concat(out)
      elseif c == "\\" then
        local e = text:sub(pos + 1, pos + 1)
        local map = { n = "\n", t = "\t", r = "\r", b = "\b", f = "\f", ['"'] = '"', ["\\"] = "\\", ["/"] = "/" }
        if e == "u" then
          local n = tonumber(text:sub(pos + 2, pos + 5), 16)
          if n < 128 then
            out[#out + 1] = string.char(n)
          elseif n < 2048 then
            out[#out + 1] = string.char(192 + math.floor(n / 64), 128 + n % 64)
          else
            out[#out + 1] = string.char(224 + math.floor(n / 4096), 128 + math.floor(n / 64) % 64, 128 + n % 64)
          end
          pos = pos + 6
        else
          out[#out + 1] = map[e] or e
          pos = pos + 2
        end
      else
        out[#out + 1] = c
        pos = pos + 1
      end
    end
  end
  value = function()
    ws()
    local c = text:sub(pos, pos)
    if c == "{" then
      local t = {}
      pos = pos + 1
      ws()
      if text:sub(pos, pos) == "}" then
        pos = pos + 1
        return t
      end
      while true do
        ws()
        local k = str()
        ws()
        assert(text:sub(pos, pos) == ":", "expected : at " .. pos)
        pos = pos + 1
        t[k] = value()
        ws()
        local d = text:sub(pos, pos)
        pos = pos + 1
        if d == "}" then
          return t
        end
        assert(d == ",", "expected , at " .. pos)
      end
    elseif c == "[" then
      local t = {}
      pos = pos + 1
      ws()
      if text:sub(pos, pos) == "]" then
        pos = pos + 1
        return t
      end
      while true do
        t[#t + 1] = value()
        ws()
        local d = text:sub(pos, pos)
        pos = pos + 1
        if d == "]" then
          return t
        end
        assert(d == ",", "expected , at " .. pos)
      end
    elseif c == '"' then
      return str()
    elseif text:sub(pos, pos + 3) == "true" then
      pos = pos + 4
      return true
    elseif text:sub(pos, pos + 4) == "false" then
      pos = pos + 5
      return false
    elseif text:sub(pos, pos + 3) == "null" then
      pos = pos + 4
      return nil
    end
    local num = text:match("^-?[%d.eE+-]+", pos)
    assert(num, "unexpected " .. c .. " at " .. pos)
    pos = pos + #num
    return tonumber(num)
  end
  local v = value()
  ws()
  assert(pos > #text, "trailing text at " .. pos)
  return v
end

local function quote(s)
  return "'" .. tostring(s):gsub("'", "'\\''") .. "'"
end

local errfile = os.tmpname()
local defined = {}

rex = {
  action = function(a)
    assert(type(a.name) == "string" and a.name:match("^[a-z0-9_]+$"), "bad action name " .. tostring(a.name))
    assert(type(a.title) == "string" and a.title ~= "", "action without a title")
    assert(type(a.run) == "function", "action without run")
    defined[a.name] = a
    print("ACTION\t" .. a.name .. "\t" .. a.title .. "\t" .. tostring(a.category))
    if a.args and a.args.properties and a.args.properties.harness and a.args.properties.harness.enum then
      for _, h in ipairs(a.args.properties.harness.enum) do
        print("ENUM\t" .. h)
      end
    end
  end,
  bind = function() end,
  block = {
    call = function(kind, method, args)
      assert(kind == "com.superlogical.terminal" and method == "process", "unexpected block call " .. tostring(kind) .. " " .. tostring(method))
      return { foreground = { pid = tonumber(os.getenv("REX_LOAD_PID") or "") } }
    end,
  },
  exec = function(argv, o)
    print("EXEC\t" .. table.concat(argv, " "))
    local parts = {}
    for _, a in ipairs(argv) do
      parts[#parts + 1] = quote(a)
    end
    local p = io.popen(table.concat(parts, " ") .. " 2>" .. quote(errfile) .. "; echo; echo __RC=$?")
    local out = p:read("*a")
    p:close()
    local body, rc = out:match("^(.*)\n__RC=(%d+)\n?$")
    body = (body or out):gsub("\n$", "")
    local f = io.open(errfile, "r")
    local err = f and f:read("*a") or ""
    if f then
      f:close()
    end
    return { ok = rc == "0", code = tonumber(rc), stdout = body, stderr = err }
  end,
  json = {
    decode = decode,
    encode = function() return "{}" end,
  },
}

local ok, err = pcall(function()
  require("tuios").setup{}
end)
if ok then
  print("LOADED")
else
  print("SETUP-ERROR\t" .. tostring(err))
end
local want = os.getenv("REX_LOAD_RUN")
if ok and want ~= nil and want ~= "" then
  local a = defined[want]
  if a == nil then
    print("RUN-ERROR\tno action " .. want)
  else
    local rok, rerr = pcall(a.run, { block_id = "block:test" }, {})
    if rok then
      print("RAN\t" .. want)
    else
      print("RUN-ERROR\t" .. tostring(rerr))
    end
  end
end
os.remove(errfile)
