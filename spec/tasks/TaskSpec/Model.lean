/-
The task card lifecycle, as an executable model of
internal/session/task_move.go (TaskState.apply). The Go code is checked
against this model by the differential test
internal/session/task_move_spec_test.go (build tag spec).

The model keeps the Go names and the Go order of the steps, so a reader can
hold the two side by side. Strings stay strings: a holder is a window id and
"" is no holder, as in Go.
-/
namespace TaskSpec

/-- The stages of a card, in board order (config.TaskStages). -/
inductive Stage where
  | todo | plan | doing | verify | review | shipped | done
  deriving DecidableEq, Repr, Inhabited

def Stage.name : Stage → String
  | .todo => "todo" | .plan => "plan" | .doing => "doing" | .verify => "verify"
  | .review => "review" | .shipped => "shipped" | .done => "done"

def Stage.all : List Stage := [.todo, .plan, .doing, .verify, .review, .shipped, .done]

def Stage.ofName? (s : String) : Option Stage := Stage.all.find? (·.name == s)

/-- TaskBlock: why a card is stuck, who said so and when. -/
structure Block where
  reason : String
  by_ : String
  at_ : Int
  deriving DecidableEq, Repr, Inhabited

/-- TaskWork: where the card's agent did the work. -/
structure Work where
  host : String
  session : String
  window : String
  dir : String
  deriving DecidableEq, Repr, Inhabited

/-- TaskVerify: one run of the board's verify command. -/
structure Verify where
  state : String
  exit : Option Int
  note : String
  tail : String
  startedAt : Int
  finishedAt : Int
  deriving DecidableEq, Repr, Inhabited

/-- TaskAttachment. -/
structure Attachment where
  name : String
  kind : String
  mediaType : String
  size : Int
  hash : String
  url : String
  note : String
  by_ : String
  at_ : Int
  deriving DecidableEq, Repr, Inhabited

/-- TaskEvent: one entry of a card's history. -/
structure Event where
  action : String
  by_ : String
  at_ : Int
  stage : Stage
  holder : String
  reason : String
  deriving DecidableEq, Repr, Inhabited

/-- The lifecycle fields of TaskState: the fields apply reads or writes. -/
structure Card where
  stage : Stage
  stageAt : Int
  holder : String
  heldAt : Int
  holderName : String
  holderHarness : String
  dispatched : Bool
  blocked : Option Block
  attempts : Int
  note : String
  work : Option Work
  verify : Option Verify
  attachments : List Attachment
  history : List Event
  deriving DecidableEq, Repr, Inhabited

/-- cardMoveKind. -/
inductive Kind where
  | claim | release | block | unblock | blockErrored | unblockErrored
  | handIn | finish | stage | auto | verified | attach | detach
  deriving DecidableEq, Repr, Inhabited

/-- cardMove. A field a kind does not use is ignored. auto is the taskMove
of moveAuto: autoFrom and autoTo. -/
structure Move where
  kind : Kind
  by_ : String := ""
  reason : String := ""
  window : String := ""
  dispatched : Bool := false
  names : Bool := false
  name : String := ""
  harness : String := ""
  refresh : Bool := false
  stage : Stage := .todo
  work : Option Work := none
  verifyRun : Bool := false
  autoFrom : List Stage := []
  autoTo : Stage := .todo
  token : Int := 0
  verify : Option Verify := none
  note : String := ""
  attachments : List Attachment := []
  deriving Repr, Inhabited

/-- The history actions (TaskAction*). -/
inductive Action where
  | claimed | released | blocked | unblocked | finished | staged | verified | attached | detached
  deriving DecidableEq, Repr

def Action.name : Action → String
  | .claimed => "claimed" | .released => "released" | .blocked => "blocked"
  | .unblocked => "unblocked" | .finished => "finished" | .staged => "staged"
  | .verified => "verified" | .attached => "attached" | .detached => "detached"

/-- Why apply refuses a move: the verbError it returns. A claim is refused
by workable, or with noWindow when it names no pane; a block of a done card
is refused with done. -/
inductive Refusal where
  | held | done | blocked | noWindow
  deriving DecidableEq, Repr

def Refusal.name : Refusal → String
  | .held => "held" | .done => "done" | .blocked => "blocked" | .noWindow => "no_window"

/-- taskByDaemon. -/
def byDaemon : String := "tuios"
/-- taskBlockErrored. -/
def blockErroredReason : String := "agent errored"
/-- taskMaxHistory. -/
def maxHistory : Nat := 100

/-- The block is the one dispatch puts on a card when its agent errored. -/
def erroredBlock : Option Block → Bool
  | some b => b.by_ == byDaemon && b.reason.startsWith blockErroredReason
  | none => false

/-- blockedOnErrored: the card's block is the one dispatch put on it when its
agent errored. -/
def Card.blockedOnErrored (c : Card) : Bool := erroredBlock c.blocked

/-- letGo: no holder, and none of the holder's fields. A block dispatch put
on the holder's errored agent goes with the holder. -/
def Card.letGo (c : Card) : Card :=
  { c with holder := "", heldAt := 0, holderName := "", holderHarness := "",
           blocked := if erroredBlock c.blocked then none else c.blocked }

/-- workable: the refusal of a claim by window. -/
def Card.workable (c : Card) (window : String) : Option Refusal :=
  if c.holder ≠ "" ∧ c.holder ≠ window then some .held
  else if c.stage = .done then some .done
  else if c.blocked.isSome then some .blocked
  else none

/-- note: append ev, keeping the first entry and the newest after it within
maxHistory. -/
def noteEvent (h : List Event) (ev : Event) : List Event :=
  if h.length ≥ maxHistory then h.take 1 ++ h.drop (h.length - maxHistory + 2) ++ [ev]
  else h ++ [ev]

/-- taskMove.applies. -/
def autoApplies (c : Card) (fromStages : List Stage) (to : Stage) : Bool :=
  c.blocked.isNone && c.stage != .done && c.stage != to && fromStages.contains c.stage

/-- A verify record that runs from now. -/
def running (now : Int) : Verify :=
  { state := "running", exit := none, note := "", tail := "", startedAt := now, finishedAt := 0 }

/-- moveAttach: each attachment takes the place of the ones with its name. -/
def attachAll (list new : List Attachment) : List Attachment :=
  new.foldl (fun l a => l.filter (fun b => b.name != a.name) ++ [a]) list

/-- moveDetach: take off the first attachment named n. -/
def detachFirst (list : List Attachment) (n : String) : Option (List Attachment) :=
  match list.findIdx? (fun a => a.name == n) with
  | some i => some (list.eraseIdx i)
  | none => none

/-- What one move does before the stage and the history are written:
nothing (the card may still change, for a claim refresh), a refusal, or a
change with its action, the stage after and the holder for the history. -/
inductive Step where
  | none (c : Card)
  | refused (r : Refusal)
  | moved (a : Action) (to : Stage) (evHolder : String) (c : Card)

def step (c : Card) (m : Move) (now : Int) : Step :=
  match m.kind with
  | .claim =>
    -- A claim names the pane that takes the card.
    if m.window = "" then .refused .noWindow
    else if c.holder = m.window then
      if m.refresh then
        let c1 := { c with dispatched := m.dispatched }
        .none (if m.names then { c1 with holderName := m.name, holderHarness := m.harness } else c1)
      else .none c
    -- workable, written out so each refusal is one branch.
    else if c.holder ≠ "" ∧ c.holder ≠ m.window then .refused .held
    else if c.stage = .done then .refused .done
    else if c.blocked.isSome then .refused .blocked
    else
        let c1 := { c with holder := m.window, heldAt := now, dispatched := m.dispatched }
        let c2 := if m.names then { c1 with holderName := m.name, holderHarness := m.harness } else c1
        let to := if c.stage = .todo ∨ c.stage = .plan then Stage.doing else c.stage
        .moved .claimed to m.window c2
  | .release =>
    if c.blockedOnErrored then .moved .released .todo c.holder c.letGo
    else .moved .released c.stage c.holder c.letGo
  | .block =>
    -- A done card is closed: no block waits on it (task_done).
    if c.stage = .done then .refused .done
    else .moved .blocked c.stage c.holder { c with blocked := some ⟨m.reason, m.by_, now⟩ }
  | .blockErrored =>
    if c.holder = "" ∨ c.dispatched = false ∨ c.blocked.isSome then .none c
    else .moved .blocked c.stage c.holder
      { c with attempts := c.attempts + 1, note := m.note, blocked := some ⟨m.reason, m.by_, now⟩ }
  | .unblock =>
    if c.blocked.isNone then .none c
    else .moved .unblocked c.stage c.holder { c with blocked := none }
  | .unblockErrored =>
    if c.blocked.isNone ∨ c.blockedOnErrored = false then .none c
    else .moved .unblocked c.stage c.holder { c with blocked := none }
  | .handIn =>
    let c1 := { c.letGo with blocked := none }
    let c2 := match m.work with
      | some w => { c1 with work := some w }
      | none => c1
    let c3 := if m.stage = .verify then { c2 with verify := some (running now) } else c2
    .moved .finished m.stage c.holder c3
  | .finish =>
    if c.stage = .done ∧ c.holder = "" ∧ c.blocked.isNone then .none c
    else .moved .finished .done c.holder { c.letGo with blocked := none }
  | .stage =>
    if c.stage = m.stage then .none c
    else if m.verifyRun then
      let c1 := match m.work with
        | some w => { c with work := some w }
        | none => c
      .moved .staged m.stage c.holder { c1 with verify := some (running now) }.letGo
    else .moved .staged m.stage c.holder c
  | .auto =>
    if autoApplies c m.autoFrom m.autoTo then .moved .staged m.autoTo c.holder c
    else .none c
  | .verified =>
    let tokenOk := match c.verify with
      | some v => v.startedAt == m.token
      | none => false
    if c.stage ≠ .verify ∨ tokenOk = false then .none c
    else
      -- A pane that took the card in verify lets it go with the result.
      let c1 := { c.letGo with verify := m.verify }
      -- A blocked card never moves on its own: it keeps its stage, and its
      -- note says what the verify found.
      if c1.blocked.isSome then .moved .verified c.stage "" { c1 with note := m.reason }
      else
        let c2 := if m.stage = .todo then { c1 with attempts := c1.attempts + 1, note := m.note } else c1
        .moved .verified m.stage "" c2
  | .attach =>
    .moved .attached c.stage c.holder { c with attachments := attachAll c.attachments m.attachments }
  | .detach =>
    match detachFirst c.attachments m.reason with
    | some l => .moved .detached c.stage c.holder { c with attachments := l }
    | none => .none c

/-- What apply returns: the action ("" when nothing changed), the holder the
card had before, the refusal of a claim, and the card after. -/
structure Result where
  action : String
  holder : String
  refusal : Option Refusal
  card : Card

/-- TaskState.apply. -/
def apply (c : Card) (m : Move) (now : Int) : Result :=
  match step c m now with
  | .none c' => ⟨"", c.holder, none, c'⟩
  | .refused r => ⟨"", c.holder, some r, c⟩
  | .moved a to evHolder c' =>
    let c'' := if c'.stage ≠ to then { c' with stage := to, stageAt := now } else c'
    ⟨a.name, c.holder, none,
      { c'' with history := noteEvent c''.history ⟨a.name, m.by_, now, to, evHolder, m.reason⟩ }⟩

end TaskSpec
