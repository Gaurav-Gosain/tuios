/-
The JSON the differential test speaks. Every field is always written, with
null for none, so the Go side compares two encodings of one schema.
-/
import Lean.Data.Json
import TaskSpec.Model

namespace TaskSpec
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

instance : ToJson Stage := ⟨fun s => .str s.name⟩
instance : FromJson Stage := ⟨fun j => do
  let s ← j.getStr?
  match Stage.ofName? s with
  | some st => pure st
  | none => throw s!"{s} is not a stage"⟩

instance : ToJson Block := ⟨fun b => Json.mkObj [("reason", b.reason), ("by", b.by_), ("at", b.at_)]⟩
instance : FromJson Block := ⟨fun j => return ⟨← field j "reason", ← field j "by", ← field j "at"⟩⟩

instance : ToJson Work := ⟨fun w => Json.mkObj
  [("host", w.host), ("session", w.session), ("window", w.window), ("dir", w.dir)]⟩
instance : FromJson Work := ⟨fun j =>
  return ⟨← field j "host", ← field j "session", ← field j "window", ← field j "dir"⟩⟩

instance : ToJson Verify := ⟨fun v => Json.mkObj
  [("state", v.state), ("exit", toJson v.exit), ("note", v.note), ("tail", v.tail),
   ("started_at", v.startedAt), ("finished_at", v.finishedAt)]⟩
instance : FromJson Verify := ⟨fun j =>
  return ⟨← field j "state", ← opt j "exit", ← field j "note", ← field j "tail",
    ← field j "started_at", ← field j "finished_at"⟩⟩

instance : ToJson Attachment := ⟨fun a => Json.mkObj
  [("name", a.name), ("kind", a.kind), ("media_type", a.mediaType), ("size", a.size),
   ("hash", a.hash), ("url", a.url), ("note", a.note), ("by", a.by_), ("at", a.at_)]⟩
instance : FromJson Attachment := ⟨fun j =>
  return ⟨← field j "name", ← field j "kind", ← field j "media_type", ← field j "size",
    ← field j "hash", ← field j "url", ← field j "note", ← field j "by", ← field j "at"⟩⟩

instance : ToJson Event := ⟨fun e => Json.mkObj
  [("action", e.action), ("by", e.by_), ("at", e.at_), ("stage", toJson e.stage),
   ("holder", e.holder), ("reason", e.reason)]⟩
instance : FromJson Event := ⟨fun j =>
  return ⟨← field j "action", ← field j "by", ← field j "at", ← field j "stage",
    ← field j "holder", ← field j "reason"⟩⟩

instance : ToJson Card := ⟨fun c => Json.mkObj
  [("stage", toJson c.stage), ("stage_at", c.stageAt), ("holder", c.holder), ("held_at", c.heldAt),
   ("holder_name", c.holderName), ("holder_harness", c.holderHarness),
   ("dispatched", c.dispatched), ("blocked", toJson c.blocked), ("attempts", c.attempts),
   ("note", c.note), ("work", toJson c.work), ("verify", toJson c.verify),
   ("attachments", toJson c.attachments), ("history", toJson c.history)]⟩
instance : FromJson Card := ⟨fun j =>
  return ⟨← field j "stage", ← field j "stage_at", ← field j "holder", ← field j "held_at",
    ← field j "holder_name", ← field j "holder_harness", ← field j "dispatched",
    ← opt j "blocked", ← field j "attempts", ← field j "note", ← opt j "work",
    ← opt j "verify", ← field j "attachments", ← field j "history"⟩⟩

def Kind.ofName? : String → Option Kind
  | "claim" => some .claim | "release" => some .release | "block" => some .block
  | "unblock" => some .unblock | "block_errored" => some .blockErrored
  | "unblock_errored" => some .unblockErrored | "hand_in" => some .handIn
  | "finish" => some .finish | "stage" => some .stage | "auto" => some .auto
  | "verified" => some .verified | "attach" => some .attach | "detach" => some .detach
  | _ => none

/-- A move and the time it is made at. -/
def parseMove (j : Json) : Except String (Move × Int) := do
  let k : String ← field j "kind"
  let some kind := Kind.ofName? k | throw s!"{k} is not a move kind"
  let m : Move := {
    kind, by_ := ← field j "by", reason := ← field j "reason", window := ← field j "window",
    dispatched := ← field j "dispatched", names := ← field j "names", name := ← field j "name",
    harness := ← field j "harness", refresh := ← field j "refresh",
    stage := (← opt j "stage").getD .todo, work := ← opt j "work",
    verifyRun := ← field j "verify_run", autoFrom := ← field j "auto_from",
    autoTo := (← opt j "auto_to").getD .todo, token := ← field j "token",
    verify := ← opt j "verify", note := ← field j "note", attachments := ← field j "attachments" }
  return (m, ← field j "now")

def resultJson (r : Result) : Json := Json.mkObj
  [("action", r.action), ("holder", r.holder),
   ("refusal", match r.refusal with | some x => Json.str x.name | none => Json.null),
   ("card", toJson r.card)]

/-- One stream: a start card and its moves, answered with the result of each
move in turn. -/
def runStream (line : String) : Except String Json := do
  let j ← Json.parse line
  let card : Card ← field j "card"
  let moves : Array Json ← field j "moves"
  let mut c := card
  let mut out : Array Json := #[]
  for mj in moves do
    let (m, now) ← parseMove mj
    let r := apply c m now
    out := out.push (resultJson r)
    c := r.card
  return Json.mkObj [("steps", Json.arr out)]

end TaskSpec
