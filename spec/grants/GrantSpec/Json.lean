/-
The JSON the differential test speaks. The Go side writes every field, with
null for none.
-/
import Lean.Data.Json
import GrantSpec.Model

namespace GrantSpec
open Lean (Json ToJson FromJson toJson fromJson?)

private def field [FromJson α] (j : Json) (k : String) : Except String α :=
  match j.getObjVal? k with
  | .ok v => fromJson? v |>.mapError (s!"{k}: " ++ ·)
  | .error _ => .error s!"missing field {k}"

private def opt [FromJson α] (j : Json) (k : String) : Except String (Option α) :=
  match j.getObjVal? k with
  | .ok .null => .ok none
  | .ok v => (some <$> fromJson? v) |>.mapError (s!"{k}: " ++ ·)
  | .error _ => .ok none

/-- The grant names in documented order (config.PaneGrantNames). -/
def Grants.names (g : Grants) : List String :=
  (if g.read then ["read"] else []) ++ (if g.write then ["write"] else []) ++
  (if g.mail then ["mail"] else []) ++
  (if g.fan then ["fan"] else []) ++ (if g.respond then ["respond"] else []) ++
  (if g.files then ["files"] else []) ++ (if g.task then ["task"] else []) ++
  (if g.admin then ["admin"] else [])

def Grants.ofNames (l : List String) : Except String Grants :=
  l.foldlM (fun g n => match n with
    | "read" => pure { g with read := true } | "write" => pure { g with write := true }
    | "mail" => pure { g with mail := true }
    | "fan" => pure { g with fan := true } | "respond" => pure { g with respond := true }
    | "files" => pure { g with files := true } | "task" => pure { g with task := true }
    | "admin" => pure { g with admin := true }
    | _ => throw s!"{n} is not a grant") {}

instance : ToJson Grants := ⟨fun g => toJson g.names⟩
instance : FromJson Grants := ⟨fun j => do Grants.ofNames (← fromJson? j)⟩

instance : FromJson Worktree := ⟨fun j => return ⟨← field j "launched_from", ← field j "group",
  ← field j "managed", ← field j "repo_root", ← field j "fan_id", ← field j "judge_session",
  ← field j "judge_window"⟩⟩
instance : FromJson Session := ⟨fun j => return ⟨← field j "name", ← field j "id", ← field j "focused",
  ← opt j "worktree"⟩⟩
instance : FromJson Link := ⟨fun j => return ⟨← field j "root", ← field j "depth"⟩⟩
instance : FromJson Pane := ⟨fun j => return ⟨← field j "id", ← field j "session", ← field j "started_by",
  ← opt j "link", ← field j "depth", ← opt j "grants", ← field j "needs_input", ← field j "approval"⟩⟩
instance : FromJson Config := ⟨fun j => return ⟨← field j "strict", ← field j "grants", ← field j "max_depth"⟩⟩
instance : FromJson State := ⟨fun j => return ⟨← field j "sessions", ← field j "panes", ← field j "config"⟩⟩
instance : FromJson Restriction := ⟨fun j => return ⟨← field j "own", ← field j "read_only"⟩⟩
instance : FromJson Caller := ⟨fun j => return ⟨← field j "pane", ← opt j "restriction"⟩⟩
instance : FromJson Target := ⟨fun j => return {
  session := ← field j "session", window := ← field j "window", from_ := ← field j "from",
  select := ← field j "select", allSessions := ← field j "all_sessions",
  allowBlocked := ← field j "allow_blocked", grants := ← opt j "grants" }⟩

def Decision.name : Decision → String
  | .allow => "allow" | .forbidden => "forbidden" | .invalid => "invalid"
  | .blocked => "blocked" | .error => "error"

def childJson : Option Child → Json
  | none => .null
  | some .none => .null
  | some .default => .str "default"
  | some (.given g) => toJson g

/-- One state and its calls, answered with the answer to each call. -/
def runCase (line : String) : Except String Json := do
  let j ← Json.parse line
  let s : State ← field j "state"
  let calls : Array Json ← field j "calls"
  let mut out : Array Json := #[]
  for cj in calls do
    let c : Caller ← field cj "caller"
    let v : String ← field cj "verb"
    let t : Target ← field cj "target"
    let a := s.decide c v t
    out := out.push (Json.mkObj [("decision", a.decision.name), ("child", childJson a.child)])
  return Json.mkObj [("answers", Json.arr out)]

end GrantSpec
