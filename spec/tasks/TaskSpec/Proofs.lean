/-
The invariants of the task card lifecycle, proved of the model in
TaskSpec.Model. The rules cardRulesBroken checks in Go come first, then the
rules the docs (tuios --skill tasks) promise, then counterexamples: a
concrete card and move that break a rule, where the Go code does not stop
them.
-/
import TaskSpec.Model
namespace TaskSpec

/-! ## The rules, as predicates on a card -/

/-- cardRulesBroken: "a done card is held or blocked". -/
def Card.doneFree (c : Card) : Prop :=
  c.stage = .done → c.holder = "" ∧ c.blocked = none

/-- cardRulesBroken: "a card with no holder keeps its holder's time or name". -/
def Card.holderClean (c : Card) : Prop :=
  c.holder = "" → c.heldAt = 0 ∧ c.holderName = "" ∧ c.holderHarness = ""

/-- cardRulesBroken: "a card blocked on its errored agent has no holder". -/
def Card.erroredHeld (c : Card) : Prop :=
  c.blockedOnErrored = true → c.holder ≠ ""

/-! ## How step and apply relate -/

@[simp] theorem erroredBlock_none : erroredBlock none = false := rfl

@[simp] theorem erroredBlock_some (b : Block) :
    erroredBlock (some b) = (b.by_ == byDaemon && b.reason.startsWith blockErroredReason) := rfl

/-- Letting the holder go leaves no errored block. -/
@[simp] theorem erroredBlock_lift (o : Option Block) :
    erroredBlock (if erroredBlock o = true then none else o) = false := by
  cases h : erroredBlock o <;> simp [h]

/-- c' with the fields a refresh claim writes taken from c. -/
def Card.sansRefresh (c' c : Card) : Card :=
  { c' with dispatched := c.dispatched, holderName := c.holderName, holderHarness := c.holderHarness }

theorem Action.name_ne_empty (a : Action) : a.name ≠ "" := by
  cases a <;> decide

variable {c c' : Card} {m : Move} {now : Int} {a : Action} {to : Stage} {e : String} {r : Refusal}

theorem apply_none (h : step c m now = .none c') : apply c m now = ⟨"", c.holder, none, c'⟩ := by
  unfold apply; rw [h]

theorem apply_refused (h : step c m now = .refused r) : apply c m now = ⟨"", c.holder, some r, c⟩ := by
  unfold apply; rw [h]

theorem apply_moved (h : step c m now = .moved a to e c') :
    apply c m now = ⟨a.name, c.holder, none,
      { c' with stage := to, stageAt := if c'.stage = to then c'.stageAt else now,
                history := noteEvent c'.history ⟨a.name, m.by_, now, to, e, m.reason⟩ }⟩ := by
  unfold apply; rw [h]; simp only
  split <;> rename_i hst <;> simp [hst]
  cases c'; simp_all

/-- A move that changes the card leaves its history, stage and stage time to
apply. -/
theorem step_moved (h : step c m now = .moved a to e c') :
    c'.history = c.history ∧ c'.stage = c.stage ∧ c'.stageAt = c.stageAt := by
  (unfold step autoApplies at h; cases hk : m.kind <;> simp only [hk] at h <;> (repeat' split at h) <;> (try cases h)) <;> simp_all [Card.letGo]

theorem step_moved_attempts (h : step c m now = .moved a to e c') : c.attempts ≤ c'.attempts := by
  (unfold step autoApplies at h; cases hk : m.kind <;> simp only [hk] at h <;> (repeat' split at h) <;> (try cases h)) <;> simp_all [Card.letGo] <;> omega

/-- Only a claim is refused by workable, and only a block of a done card
otherwise. -/
theorem step_refused (h : step c m now = .refused r) :
    (m.kind = .claim ∧ c.workable m.window = some r) ∨ (m.kind = .block ∧ c.stage = .done ∧ r = .done) := by
  unfold step at h
  cases hk : m.kind <;> simp only [hk] at h <;> (repeat' split at h) <;> simp_all [Card.workable]

/-- A move that changes nothing returns the card as it was, except a claim
the holder makes again with refresh, which writes dispatched and the
holder's names and nothing else. -/
theorem step_none (h : step c m now = .none c') :
    c' = c ∨ (m.kind = .claim ∧ m.refresh = true ∧ c.holder = m.window ∧ c'.sansRefresh c = c) := by
  unfold step at h
  cases hk : m.kind <;> simp only [hk] at h <;> (repeat' split at h) <;> (try cases h) <;>
    (try (left; rfl)) <;> (right; cases c; simp_all [Card.sansRefresh])

/-- A move that changes nothing keeps every field but dispatched and the
holder's names. -/
theorem step_none_keeps (h : step c m now = .none c') :
    c'.stage = c.stage ∧ c'.stageAt = c.stageAt ∧ c'.holder = c.holder ∧ c'.heldAt = c.heldAt ∧
    c'.blocked = c.blocked ∧ c'.attempts = c.attempts ∧ c'.history = c.history := by
  (unfold step autoApplies at h; cases hk : m.kind <;> simp only [hk] at h <;> (repeat' split at h) <;> (try cases h)) <;> simp_all

theorem step_none_history (h : step c m now = .none c') : c'.history = c.history ∧ c'.attempts = c.attempts :=
  ⟨(step_none_keeps h).2.2.2.2.2.2, (step_none_keeps h).2.2.2.2.2.1⟩

/-! ## What every move keeps -/

/-- A refused move changes nothing. It is a claim workable refuses, or a
block of a done card. -/
theorem refused_changes_nothing (h : (apply c m now).refusal = some r) :
    (apply c m now).card = c ∧ (apply c m now).action = "" ∧
      ((m.kind = .claim ∧ c.workable m.window = some r) ∨ (m.kind = .block ∧ c.stage = .done ∧ r = .done)) := by
  cases hs : step c m now
  · rw [apply_none hs] at h; simp at h
  · rw [apply_refused hs] at h ⊢; simp at h; subst h; exact ⟨rfl, rfl, step_refused hs⟩
  · rw [apply_moved hs] at h; simp at h

/-- A move that returns no action changes nothing, except the refresh of a
claim by the holder (dispatched and the holder's names). -/
theorem noop_changes_nothing (h : (apply c m now).action = "") :
    (apply c m now).card = c ∨
      (m.kind = .claim ∧ m.refresh = true ∧ c.holder = m.window ∧
        (apply c m now).card.sansRefresh c = c) := by
  cases hs : step c m now
  · rw [apply_none hs]; exact step_none hs
  · rw [apply_refused hs]; left; rfl
  · rw [apply_moved hs] at h; exact absurd h (Action.name_ne_empty _)

/-- The move's entry goes last in the history. -/
theorem noteEvent_last (h : List Event) (ev : Event) : (noteEvent h ev).getLast? = some ev := by
  unfold noteEvent; split <;> simp

/-- The history never grows past maxHistory. -/
theorem noteEvent_length (h : List Event) (ev : Event) (hl : h.length ≤ maxHistory) :
    (noteEvent h ev).length ≤ maxHistory := by
  unfold noteEvent maxHistory at *
  split <;> simp <;> omega

/-- A move that changes the card notes one history entry: its action, m.by,
the time, the stage after and m.reason. -/
theorem change_notes_one (h : (apply c m now).action ≠ "") :
    ∃ ev, (apply c m now).card.history = noteEvent c.history ev ∧
      ev.action = (apply c m now).action ∧ ev.by_ = m.by_ ∧ ev.at_ = now ∧
      ev.stage = (apply c m now).card.stage ∧ ev.reason = m.reason := by
  cases hs : step c m now
  · rw [apply_none hs] at h; simp at h
  · rw [apply_refused hs] at h; simp at h
  · rw [apply_moved hs]; rw [(step_moved hs).1]; exact ⟨_, rfl, rfl, rfl, rfl, rfl, rfl⟩

/-- The history stays within maxHistory. -/
theorem history_bounded (hl : c.history.length ≤ maxHistory) :
    (apply c m now).card.history.length ≤ maxHistory := by
  cases hs : step c m now
  · rw [apply_none hs]; simp only; rw [(step_none_history hs).1]; exact hl
  · rw [apply_refused hs]; exact hl
  · rw [apply_moved hs]; simp only; rw [(step_moved hs).1]; exact noteEvent_length _ _ hl

/-- StageAt is the time of the move that changed the stage, and only such a
move writes it. -/
theorem stageAt_follows_stage (h : (apply c m now).action ≠ "") :
    ((apply c m now).card.stage ≠ c.stage → (apply c m now).card.stageAt = now) ∧
    ((apply c m now).card.stage = c.stage → (apply c m now).card.stageAt = c.stageAt) := by
  cases hs : step c m now
  · rw [apply_none hs] at h; simp at h
  · rw [apply_refused hs] at h; simp at h
  · rename_i a to ev c'
    rw [apply_moved hs]; simp only
    obtain ⟨-, h2, h3⟩ := step_moved hs
    rw [h2, h3]
    by_cases e : c.stage = to
    · subst e; simp
    · have e' : to ≠ c.stage := fun h => e h.symm
      simp [e, e']

/-- Attempts never go down. -/
theorem attempts_monotone : c.attempts ≤ (apply c m now).card.attempts := by
  cases hs : step c m now
  · rw [apply_none hs]; simp only; rw [(step_none_history hs).2]; exact Int.le_refl _
  · rw [apply_refused hs]; exact Int.le_refl _
  · rw [apply_moved hs]; simpa using step_moved_attempts hs

/-- apply returns the holder the card had before. -/
theorem holder_before : (apply c m now).holder = c.holder := by
  unfold apply; split <;> rfl

/-! ## The rules cardRulesBroken checks -/

/-- "The card is still held after a move that lets it go": never. A hand in,
a verify result and a move to verify that runs the board's command leave the
card with no holder. -/
theorem letting_go_moves_let_go (h : (apply c m now).action ≠ "")
    (hk : m.kind = .handIn ∨ m.kind = .verified ∨ (m.kind = .stage ∧ m.verifyRun = true)) :
    (apply c m now).card.holder = "" := by
  cases hs : step c m now
  · rw [apply_none hs] at h; simp at h
  · rw [apply_refused hs] at h; simp at h
  · rw [apply_moved hs]; simp only
    (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all [Card.letGo]

/-- The moves the verbs make. Each condition is one the Go code relies on;
the comment says where it holds, or that nothing holds it. -/
structure Admissible (c : Card) (m : Move) : Prop where
  /-- set-task-stage sends done as a finish (verbSetTaskStage). -/
  stageNotDone : m.kind = .stage → m.stage ≠ .done
  /-- applyVerify sends todo, review or verify. -/
  verifiedNotDone : m.kind = .verified → m.stage ≠ .done
  /-- Every taskMove goes to a stage other than done (task_auto.go). -/
  autoNotDone : m.kind = .auto → m.autoTo ≠ .done

/-- "A done card is held or blocked": never, for the moves the verbs make. -/
theorem doneFree_preserved (hc : c.doneFree) (ha : Admissible c m) : (apply c m now).card.doneFree := by
  have h1 := ha.stageNotDone; have h2 := ha.verifiedNotDone; have h3 := ha.autoNotDone
  unfold Card.doneFree at *
  cases hs : step c m now
  · rw [apply_none hs]
    obtain ⟨e1, -, e3, -, e5, -⟩ := step_none_keeps hs
    simp only [e1, e3, e5]; exact hc
  · rw [apply_refused hs]; exact hc
  · rw [apply_moved hs]; simp only
    (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all [Card.letGo]

/-- "A card with no holder keeps its holder's time or name": never, when
every claim names a pane. Nothing in the Go code holds that: a claim with
window "" and refresh on a card no pane holds writes the names. -/
theorem holderClean_preserved (hc : c.holderClean) (hw : m.kind = .claim → m.window ≠ "") :
    (apply c m now).card.holderClean := by
  unfold Card.holderClean at *
  cases hs : step c m now
  · rw [apply_none hs]
    unfold step at hs
    cases hk : m.kind <;> simp only [hk] at hs hw <;> (repeat' split at hs) <;> (try cases hs) <;>
      simp_all
  · rw [apply_refused hs]; exact hc
  · rw [apply_moved hs]; simp only
    (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all [Card.letGo]

/-- What "a card blocked on its errored agent has a holder" needs from the
verbs: block-task never blocks as tuios a card no pane holds. Its by is the
caller's, and no caller is tuios. -/
structure KeepsErroredHeld (c : Card) (m : Move) : Prop where
  block : m.kind = .block → m.by_ = byDaemon → c.holder ≠ ""

/-- "A card blocked on its errored agent has no holder": never, for moves
that keep KeepsErroredHeld. -/
theorem erroredHeld_preserved (hc : c.erroredHeld) (ha : KeepsErroredHeld c m) :
    (apply c m now).card.erroredHeld := by
  have h3 := ha.block
  unfold Card.erroredHeld Card.blockedOnErrored at *
  cases hs : step c m now
  · rw [apply_none hs]
    obtain ⟨-, -, e3, -, e5, -⟩ := step_none_keeps hs
    simp only [e3, e5]; exact hc
  · rw [apply_refused hs]; exact hc
  · rw [apply_moved hs]; simp only
    (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all [Card.letGo, Card.blockedOnErrored]

/-! ## What the docs promise (tuios --skill tasks) -/

/-- "A done card cannot be blocked": block-task of a done card is refused
with task_done and changes nothing. -/
theorem block_of_done_refused (hk : m.kind = .block) (hd : c.stage = .done) :
    (apply c m now).refusal = some .done ∧ (apply c m now).card = c := by
  have hs : step c m now = .refused .done := by unfold step; simp [hk, hd]
  rw [apply_refused hs]; exact ⟨rfl, rfl⟩

/-- "Mark it stuck. It keeps its holder and stage." -/
theorem block_keeps_holder_and_stage (hk : m.kind = .block) (hd : c.stage ≠ .done) :
    (apply c m now).card.holder = c.holder ∧ (apply c m now).card.stage = c.stage ∧
    (apply c m now).card.blocked = some ⟨m.reason, m.by_, now⟩ := by
  cases hs : step c m now
  all_goals first
    | (rw [apply_moved hs]; simp only; (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all)
    | (unfold step at hs; simp [hk, hd] at hs)

/-- "Let it go without finishing. It keeps its stage": a release keeps the
stage, except that a card blocked on its errored agent goes back to todo with
the block lifted. -/
theorem release_keeps_stage (hk : m.kind = .release) :
    (apply c m now).card.holder = "" ∧
    (c.blockedOnErrored = false → (apply c m now).card.stage = c.stage) ∧
    (c.blockedOnErrored = true → (apply c m now).card.stage = .todo ∧ (apply c m now).card.blocked = none) := by
  cases hs : step c m now
  all_goals first
    | (rw [apply_moved hs]; simp only; (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all [Card.letGo, Card.blockedOnErrored])
    | (unfold step at hs; simp [hk] at hs; split at hs <;> simp at hs)

/-- "A card in todo or plan moves to doing when you claim it." -/
theorem claim_starts_work (hk : m.kind = .claim) (h : (apply c m now).action ≠ "") :
    (apply c m now).card.holder = m.window ∧
    (c.stage = .todo ∨ c.stage = .plan → (apply c m now).card.stage = .doing) := by
  cases hs : step c m now
  · rw [apply_none hs] at h; simp at h
  · rw [apply_refused hs] at h; simp at h
  · rw [apply_moved hs]; simp only
    (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all

/-- "A claim by the pane that holds the card succeeds." -/
theorem claim_by_holder_succeeds (hk : m.kind = .claim) (hh : c.holder = m.window) :
    (apply c m now).refusal = none := by
  cases hs : step c m now
  · rw [apply_none hs]
  · unfold step at hs; simp only [hk, hh, ite_true] at hs; (repeat' split at hs) <;> nomatch hs
  · rw [apply_moved hs]

/-- A claim refuses a card another pane holds, a done card and a blocked
card, in that order: task_held, then invalid_params. -/
theorem claim_refusals (hk : m.kind = .claim) (hh : c.holder ≠ m.window) :
    (c.holder ≠ "" → (apply c m now).refusal = some .held) ∧
    (c.holder = "" → c.stage = .done → (apply c m now).refusal = some .done) ∧
    (c.holder = "" → c.stage ≠ .done → c.blocked.isSome → (apply c m now).refusal = some .blocked) := by
  cases hs : step c m now
  all_goals first
    | (rw [apply_refused hs]; (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all)
    | (rw [apply_none hs]; (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all)
    | (rw [apply_moved hs]; (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all)

/-- "A blocked card and a done card never move on their own": an automatic
move leaves them as they are. -/
theorem auto_leaves_blocked_and_done (hk : m.kind = .auto) (h : c.blocked.isSome ∨ c.stage = .done) :
    (apply c m now).card = c := by
  cases hs : step c m now
  · rw [apply_none hs]; (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all
  · rw [apply_refused hs]
  · (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all

/-- "A blocked card never moves on its own": a verify result on a card with
a block that is not the errored agent's keeps the stage and the block, and
the note says what the verify found. -/
theorem verified_keeps_a_blocked_card (hk : m.kind = .verified) (h : (apply c m now).action ≠ "")
    (hb : c.blocked = some b) (he : erroredBlock c.blocked = false) :
    (apply c m now).card.stage = c.stage ∧ (apply c m now).card.blocked = some b ∧
    (apply c m now).card.note = m.reason ∧ (apply c m now).card.verify = m.verify := by
  cases hs : step c m now
  · rw [apply_none hs] at h; simp at h
  · rw [apply_refused hs] at h; simp at h
  · rw [apply_moved hs]; simp only
    (unfold step autoApplies at hs; cases hk : m.kind <;> simp only [hk] at hs <;> (repeat' split at hs) <;> (try cases hs)) <;> simp_all [Card.letGo]

/-- An automatic move and a verify result are the moves the daemon makes on
its own. Neither changes the stage of a card with a block that is not the
errored agent's. -/
theorem blocked_card_never_moves_on_its_own (hk : m.kind = .auto ∨ m.kind = .verified)
    (hb : c.blocked.isSome) (he : erroredBlock c.blocked = false) :
    (apply c m now).card.stage = c.stage := by
  rcases hk with hk | hk
  · rw [auto_leaves_blocked_and_done hk (Or.inl hb)]
  · by_cases h : (apply c m now).action = ""
    · rcases noop_changes_nothing h with e | ⟨hk', -⟩
      · rw [e]
      · simp [hk] at hk'
    · obtain ⟨b, hb'⟩ := Option.isSome_iff_exists.mp hb
      exact (verified_keeps_a_blocked_card hk h hb' he).1

end TaskSpec
