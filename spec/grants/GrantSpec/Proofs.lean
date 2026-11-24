/-
Proofs of the rules docs/protocol.md and the comments of pane_grants.go,
conn_scope.go, own_tree.go and agent_depth.go promise. Each theorem holds
for every state, caller, verb and parameters, not only for the tested ones.
-/
import GrantSpec.Model

namespace GrantSpec

/-! ## Grants -/

/-- Admin does not imply respond. -/
theorem expand_respond (g : Grants) : g.expand.respond = g.respond := by
  unfold Grants.expand; split <;> rfl

theorem expand_admin (g : Grants) : g.expand.admin = g.admin := by
  unfold Grants.expand; split <;> rfl

/-- A pane gives respond only when it holds respond. -/
theorem covers_respond {a b : Grants} (h : a.covers b = true) (hb : b.respond = true) :
    a.respond = true := by
  have h1 := expand_respond a
  have h2 := expand_respond b
  cases a; cases b
  simp_all [Grants.covers, Grants.le]

/-- A pane gives admin only when it holds admin. -/
theorem covers_admin {a b : Grants} (h : a.covers b = true) (hb : b.admin = true) :
    a.admin = true := by
  have h1 := expand_admin a
  have h2 := expand_admin b
  cases a; cases b
  simp_all [Grants.covers, Grants.le]

/-- What a pane gives is what it holds, or less: covers is transitive. -/
theorem covers_trans {a b c : Grants} (h1 : a.covers b = true) (h2 : b.covers c = true) :
    a.covers c = true := by
  cases a with | mk r1 w1 f1 p1 i1 t1 a1 =>
  cases b with | mk r2 w2 f2 p2 i2 t2 a2 =>
  cases c with | mk r3 w3 f3 p3 i3 t3 a3 =>
  cases a1 <;> cases a2 <;> cases a3 <;>
    simp_all [Grants.covers, Grants.le, Grants.expand] <;> grind

theorem covers_refl (a : Grants) : a.covers a = true := by
  cases a with | mk r w f p i t ad =>
  cases ad <;> simp [Grants.covers, Grants.le, Grants.expand]

/-- The default a pane with admin gives, cut to what it holds, is covered by
what it holds. -/
theorem covers_meet (a d : Grants) : a.covers (d.expand.meet a.expand) = true := by
  cases a with | mk r1 w1 f1 p1 i1 t1 a1 =>
  cases d with | mk r2 w2 f2 p2 i2 t2 a2 =>
  cases a1 <;> cases a2 <;> simp [Grants.covers, Grants.le, Grants.expand, Grants.meet] <;> grind

/-! ## The session boundary -/

theorem holdSession_reach {v : String} {t t' : Target} {own : String} {f : String → Bool}
    (h : holdSession v t own f = .ok t') : t'.session = "" ∨ f t'.session = true := by
  unfold holdSession at h
  split at h
  · split at h
    · cases h; right; assumption
    · cases h
  · split at h
    · split at h
      · cases h; right; assumption
      · cases h
    · cases h; left; simp_all

theorem holdSession_keeps {v : String} {t t' : Target} {own : String} {f : String → Bool}
    (h : holdSession v t own f = .ok t') (hs : t.session ≠ "") : t'.session = t.session := by
  unfold holdSession at h
  simp only [hs, ne_eq, not_false_eq_true, ↓reduceIte] at h
  split at h
  · cases h; rfl
  · cases h

/-- What holdToPane lets through names no session, or a session in reach. -/
theorem holdToPane_reach {v : String} {k : Kind} {t t' : Target} {own w : String} {f : String → Bool}
    (h : holdToPane v k t own w f = .ok t') : t'.session = "" ∨ f t'.session = true := by
  unfold holdToPane at h
  split at h
  · cases h
  · rename_i t1 h1
    have r := holdSession_reach h1
    repeat' split at h
    all_goals (cases h <;> first | rfl | exact r)

theorem holdToPane_keeps {v : String} {k : Kind} {t t' : Target} {own w : String} {f : String → Bool}
    (h : holdToPane v k t own w f = .ok t') (hs : t.session ≠ "") : t'.session = t.session := by
  unfold holdToPane at h
  split at h
  · cases h
  · rename_i t1 h1
    have r := holdSession_keeps h1 hs
    repeat' split at h
    all_goals (cases h <;> first | rfl | exact r)

theorem holdTyping_keeps {s : State} {pa : PaneAuth} {v : String} {t t' : Target}
    (h : s.holdTyping pa v t = .ok t') : t'.session = t.session := by
  unfold State.holdTyping at h
  repeat' split at h
  all_goals (cases h <;> rfl)

/-- The sessions a verb of a pane without admin reaches through its grants:
its own, its fan group with read or fan, and the attempts it judges. -/
theorem gate_reach {s : State} {pa : PaneAuth} {v x : String} {f : String → Bool}
    (hn : pa.grants.admin = false) (h : s.grantGate pa v = .ok (.reach f)) (hx : f x = true) :
    x = pa.session ∨ (s.inScope pa.session x = true ∧ ((verbInfo v).grant = .read ∨ pa.grants.expand.fan = true)) ∨
      s.judgeReaches pa x = true := by
  unfold State.grantGate at h
  dsimp only at h
  split at h <;> rename_i hk
  all_goals (repeat' split at h)
  all_goals (try cases h)
  all_goals simp_all [State.writeReach]
  all_goals grind

/-- A write or mail of a pane without admin reaches its own session, or a
session of its fan group with fan. -/
theorem gate_write_reach {s : State} {pa : PaneAuth} {v x : String} {f : String → Bool}
    (hn : pa.grants.admin = false) (hk : (verbInfo v).grant = .mail ∨ (verbInfo v).grant = .write)
    (h : s.grantGate pa v = .ok (.reach f)) (hx : f x = true) :
    x = pa.session ∨ (s.inScope pa.session x = true ∧ pa.grants.expand.fan = true) := by
  unfold State.grantGate at h
  dsimp only at h
  rcases hk with hk | hk <;> simp only [hk] at h
  all_goals (repeat' split at h)
  all_goals (try cases h)
  all_goals simp_all [State.writeReach]
  all_goals grind

/-- A task write or a report of a pane without admin reaches its own session
only. -/
theorem gate_home_reach {s : State} {pa : PaneAuth} {v x : String} {f : String → Bool}
    (hk : (verbInfo v).grant = .task ∨ (verbInfo v).grant = .self)
    (h : s.grantGate pa v = .ok (.reach f)) (hx : f x = true) : x = pa.session := by
  unfold State.grantGate at h
  dsimp only at h
  rcases hk with hk | hk <;> simp only [hk] at h
  all_goals (repeat' split at h)
  all_goals (try cases h)
  all_goals simp_all

/-- The kinds of verb that name a session a pane's grants hold it to. -/
def sessionKind (k : Kind) : Bool :=
  k == .read || k == .self || k == .mail || k == .write || k == .launch || k == .task

theorem gate_of_sessionKind {s : State} {pa : PaneAuth} {v : String}
    (hk : sessionKind (verbInfo v).grant = true) : s.grantGate pa v ≠ .ok .pass := by
  unfold State.grantGate
  intro h
  split at h <;> simp_all [sessionKind] <;> (repeat' split at h) <;> simp_all

/-- The path of checkGrants for a pane without admin when the own tree rule
does not apply: the call either passes the gate untouched or is held to the
gate's reach. -/
theorem checkGrants_reach {s : State} {c : Caller} {pa : PaneAuth} {v : String} {t t' : Target}
    (ha : s.auth c = some pa) (hn : pa.grants.admin = false) (ht : s.holdToOwnTree pa v t = none)
    (h : s.checkGrants c v t = .ok t') :
    (s.grantGate pa v = .ok .pass ∧ t' = t) ∨
      ∃ f, s.grantGate pa v = .ok (.reach f) ∧ (t'.session = "" ∨ f t'.session = true) := by
  unfold State.checkGrants at h
  rw [ha] at h
  simp only [hn, Bool.false_eq_true, ↓reduceIte, ht] at h
  split at h
  · cases h
  · split at h
    · cases h
    · rename_i hg; cases h; left; exact ⟨hg, rfl⟩
    · rename_i f hg
      split at h
      · cases h
      · split at h
        · cases h
        · rename_i t1 h1
          refine Or.inr ⟨f, hg, ?_⟩
          have r := holdToPane_reach h1
          split at h
          · rw [holdTyping_keeps h]; exact r
          · cases h; exact r

/-- The session boundary. A pane without admin, outside its own tree rule,
reaches through a verb that names a session only its own session, a session
of its fan group (with read for a read, with fan otherwise), or the attempts
it judges. -/
theorem pane_stays_in_scope {s : State} {c : Caller} {pa : PaneAuth} {v : String} {t t' : Target}
    (ha : s.auth c = some pa) (hn : pa.grants.admin = false) (ht : s.holdToOwnTree pa v t = none)
    (hk : sessionKind (verbInfo v).grant = true) (h : s.checkGrants c v t = .ok t') :
    t'.session = "" ∨ t'.session = pa.session ∨
      (s.inScope pa.session t'.session = true ∧ ((verbInfo v).grant = .read ∨ pa.grants.expand.fan = true)) ∨
      s.judgeReaches pa t'.session = true := by
  rcases checkGrants_reach ha hn ht h with ⟨hp, _⟩ | ⟨f, hg, hr⟩
  · exact absurd hp (gate_of_sessionKind hk)
  · rcases hr with hr | hr
    · exact Or.inl hr
    · exact Or.inr (gate_reach hn hg hr)

/-- Writing outside the pane's own session needs fan: a pane without admin
types into, or leaves mail in, another session only with fan, and only in
its fan group. respond is a write verb, so this is also the reach of
respond that the respond row of skills/tuios/grants.md, the grant table of
docs/protocol.md and the respond verb doc state. -/
theorem write_outside_needs_fan {s : State} {c : Caller} {pa : PaneAuth} {v : String} {t t' : Target}
    (ha : s.auth c = some pa) (hn : pa.grants.admin = false) (ht : s.holdToOwnTree pa v t = none)
    (hk : (verbInfo v).grant = .mail ∨ (verbInfo v).grant = .write) (h : s.checkGrants c v t = .ok t')
    (hs : t'.session ≠ "") (ho : t'.session ≠ pa.session) :
    pa.grants.expand.fan = true ∧ s.inScope pa.session t'.session = true := by
  have hk' : sessionKind (verbInfo v).grant = true := by rcases hk with hk | hk <;> simp [hk, sessionKind]
  rcases checkGrants_reach ha hn ht h with ⟨hp, _⟩ | ⟨f, hg, hr⟩
  · exact absurd hp (gate_of_sessionKind hk')
  · rcases hr with hr | hr
    · exact absurd hr hs
    · rcases gate_write_reach hn hk hg hr with h1 | ⟨h1, h2⟩
      · exact absurd h1 ho
      · exact ⟨h2, h1⟩

/-- A task write and a report of a pane without admin stay in its own
session. -/
theorem tasks_and_reports_stay_home {s : State} {c : Caller} {pa : PaneAuth} {v : String} {t t' : Target}
    (ha : s.auth c = some pa) (hn : pa.grants.admin = false) (ht : s.holdToOwnTree pa v t = none)
    (hk : (verbInfo v).grant = .task ∨ (verbInfo v).grant = .self) (h : s.checkGrants c v t = .ok t') :
    t'.session = "" ∨ t'.session = pa.session := by
  have hk' : sessionKind (verbInfo v).grant = true := by rcases hk with hk | hk <;> simp [hk, sessionKind]
  rcases checkGrants_reach ha hn ht h with ⟨hp, _⟩ | ⟨f, hg, hr⟩
  · exact absurd hp (gate_of_sessionKind hk')
  · rcases hr with hr | hr
    · exact Or.inl hr
    · exact Or.inr (gate_home_reach hk hg hr)

/-- The verbs that need admin: a pane without admin passes one only as a
copy with files, or as a close of a pane of its own tree. -/
theorem admin_verbs_need_admin {s : State} {c : Caller} {pa : PaneAuth} {v : String} {t t' : Target}
    (ha : s.auth c = some pa) (hn : pa.grants.admin = false)
    (hk : (verbInfo v).grant = .deny ∨ (verbInfo v).grant = .global) (h : s.checkGrants c v t = .ok t') :
    (transferVerb v = true ∧ pa.grants.expand.files = true) ∨ s.holdToOwnTree pa v t ≠ none := by
  cases ht : s.holdToOwnTree pa v t with
  | some r => right; simp
  | none =>
    left
    rcases checkGrants_reach ha hn ht h with ⟨hp, _⟩ | ⟨f, hg, _⟩
    · unfold State.grantGate at hp
      rcases hk with hk | hk <;> simp only [hk] at hp <;> split at hp <;> simp_all
    · unfold State.grantGate at hg
      rcases hk with hk | hk <;> simp only [hk] at hg <;> split at hg <;> simp_all

/-! ## Lookups -/

theorem paneIn?_id {s : State} {n id : String} {p : Pane} (h : s.paneIn? n id = some p) :
    p.id = id ∧ p.session = n := by
  have := List.find?_some h
  simp_all

theorem session?_name {s : State} {n : String} {x : Session} (h : s.session? n = some x) : x.name = n := by
  have := List.find?_some h
  simp_all

/-! ## The own tree -/

/-- Desc s root id: the pane id is below root in the records the daemon
writes: root started it, or its agent link names root as its root, or root
is above the pane that started it. -/
inductive Desc (s : State) (root : String) : String → Prop
  | started {id : String} {p : Pane} : s.pane? id = some p → p.startedBy = root → Desc s root id
  | linked {id : String} {p : Pane} {l : Link} :
      s.pane? id = some p → p.link = some l → l.root = root → Desc s root id
  | up {id : String} {p : Pane} : s.pane? id = some p → Desc s root p.startedBy → Desc s root id

theorem walk_desc {s : State} {root : String} :
    ∀ (n : Nat) (up : String) (seen : List String), s.walk root up seen n = true → up = root ∨ Desc s root up := by
  intro n
  induction n with
  | zero => intro up seen h; simp [State.walk] at h
  | succ n ih =>
    intro up seen h
    unfold State.walk at h
    split at h
    · cases h
    · split at h
      · left; assumption
      · split at h
        · cases h
        · split at h
          · cases h
          · rename_i q hq
            right
            rcases ih _ _ h with h1 | h1
            · exact Desc.started hq h1
            · exact Desc.up hq h1

/-- inOwnTree holds only for a pane below the root, never the root itself. -/
theorem inOwnTree_desc {s : State} {id root : String} (h : s.inOwnTree id root = true) :
    id ≠ root ∧ Desc s root id := by
  unfold State.inOwnTree at h
  by_cases hr : root = "" ∨ id = root
  · simp [hr] at h
  · simp only [hr, ↓reduceIte] at h
    refine ⟨fun e => hr (Or.inr e), ?_⟩
    cases hp : s.pane? id with
    | none => simp [hp] at h
    | some p =>
      simp only [hp] at h
      have up : s.walk root p.startedBy [id] maxWalk = true → Desc s root id := fun hw => by
        rcases walk_desc _ _ _ hw with h1 | h1
        · exact Desc.started hp h1
        · exact Desc.up hp h1
      cases hl : p.link with
      | some l =>
        simp only [hl] at h
        by_cases hlr : l.root = root
        · exact Desc.linked hp hl hlr
        · simp only [hlr, beq_iff_eq, ↓reduceIte] at h
          exact up h
      | none =>
        simp only [hl, Bool.false_eq_true, ↓reduceIte] at h
        exact up h

theorem checkGrants_ownTree {s : State} {c : Caller} {pa : PaneAuth} {v : String} {t : Target}
    {r : Except Refusal Target}
    (ha : s.auth c = some pa) (hn : pa.grants.admin = false) (ht : s.holdToOwnTree pa v t = some r) :
    s.checkGrants c v t = r := by
  unfold State.checkGrants
  rw [ha]
  simp only [hn, Bool.false_eq_true, ↓reduceIte, ht]

/-- A fan holder closes only its own descendants. A pane without admin that
may close a window closes the window the call names, and that window is
below the pane in its own tree. -/
theorem fan_closes_only_its_tree {s : State} {c : Caller} {pa : PaneAuth} {t t' : Target}
    (ha : s.auth c = some pa) (hn : pa.grants.admin = false)
    (h : s.checkGrants c "close-window" t = .ok t') :
    ∃ w, s.closeTarget pa t = some w ∧ t'.window = w.id ∧ w.id ≠ pa.window ∧ Desc s pa.window w.id := by
  cases ht : s.holdToOwnTree pa "close-window" t with
  | none =>
    rcases checkGrants_reach ha hn ht h with ⟨hp, _⟩ | ⟨f, hg, _⟩
    · simp [State.grantGate, verbInfo, transferVerb] at hp
    · simp [State.grantGate, verbInfo, transferVerb] at hg
  | some r =>
    rw [checkGrants_ownTree ha hn ht] at h
    subst h
    unfold State.holdToOwnTree at ht
    split at ht
    · cases ht
    · simp only [↓reduceIte] at ht
      split at ht
      · cases ht
      · rename_i w hw
        split at ht
        · rename_i hin
          cases ht
          have := inOwnTree_desc hin
          exact ⟨w, hw, rfl, this.1, this.2⟩
        · cases ht

/-! ## The depth cap -/

theorem decide_allow {s : State} {c : Caller} {v : String} {t : Target}
    (h : (s.decide c v t).decision = .allow) :
    ∃ t1 t2, s.checkGrants c v t = .ok t1 ∧ s.checkScope c v t1 = .ok t2 := by
  unfold State.decide at h
  split at h
  · simp at h
  · rename_i t1 h1
    split at h
    · simp at h
    · rename_i t2 h2
      exact ⟨t1, t2, h1, h2⟩

/-- No start makes a pane deeper than agents.max_depth: an agent start is
served only when the new pane's depth is within the cap. -/
theorem depth_within_cap {s : State} {c : Caller} {v : String} {t : Target}
    (h : (s.decide c v t).decision = .allow) (hv : startVerb v = true)
    (hc : 0 < min s.config.maxDepth linkMaxDepth) :
    s.callerDepth c + 1 ≤ min s.config.maxDepth linkMaxDepth := by
  unfold State.decide at h
  split at h
  · simp at h
  · split at h
    · simp at h
    · simp only [hv, ↓reduceIte] at h
      split at h
      · simp at h
      · split at h
        · simp at h
        · rename_i hd
          simp only [State.tooDeep, Bool.and_eq_true, decide_eq_true_eq, not_and] at hd
          exact Nat.ge_of_not_lt (hd hc)

/-! ## Restricted connections -/

/-- A restricted connection calls only the verbs the table of docs/protocol.md
lists: a verb whose scope is deny is never served on it. -/
theorem restricted_calls_only_listed {s : State} {c : Caller} {r : Restriction} {v : String} {t : Target}
    (hr : c.restriction = some r) (h : (s.decide c v t).decision = .allow) :
    (verbInfo v).scope ≠ .deny := by
  rcases decide_allow h with ⟨t1, t2, _, h2⟩
  unfold State.checkScope at h2
  rw [hr] at h2
  intro hk
  simp [hk] at h2

/-- A read-only connection never types into a pane, starts one, changes a
task card or changes the paste buffers. -/
theorem read_only_never_writes {s : State} {c : Caller} {r : Restriction} {v : String} {t : Target}
    (hr : c.restriction = some r) (hro : r.readOnly = true) (h : (s.decide c v t).decision = .allow) :
    (verbInfo v).scope ≠ .write ∧ (verbInfo v).scope ≠ .launch ∧ (verbInfo v).scope ≠ .task ∧
      (verbInfo v).scope ≠ .bufferWrite := by
  rcases decide_allow h with ⟨t1, t2, _, h2⟩
  unfold State.checkScope at h2
  rw [hr] at h2
  simp only at h2
  refine ⟨?_, ?_, ?_, ?_⟩ <;> intro hk <;> simp [hk, hro] at h2

/-- A connection restricted to its own scope reads nothing across sessions,
never the paste buffers, nothing at all from outside every pane, and names
only sessions its own session reaches. -/
theorem own_scope_stays_in_scope {s : State} {c : Caller} {r : Restriction} {v : String} {t t' : Target}
    (hr : c.restriction = some r) (ho : r.own = true) (h : s.checkScope c v t = .ok t')
    (hk : (verbInfo v).scope ≠ .open_) :
    (verbInfo v).scope ≠ .global ∧ readsBuffers v = false ∧ (verbInfo v).scope ≠ .bufferWrite ∧
      s.callerSession c ≠ "" ∧ (t'.session = "" ∨ s.inScope (s.callerSession c) t'.session = true) := by
  unfold State.checkScope at h
  rw [hr] at h
  simp only [hk, ↓reduceIte, ho, Bool.not_true, Bool.false_eq_true] at h
  split at h
  · cases h
  · split at h
    · cases h
    · split at h
      · cases h
      · split at h
        · cases h
        · split at h
          · cases h
          · rename_i h1 h2 h3 h4 h5
            simp only [not_or] at h4
            refine ⟨h3, by simpa using h4.2, h4.1, h5, holdToPane_reach h⟩

/-! ## Giving grants -/

/-- A start gives the new pane no more than the caller holds: its own grants,
the default cut to them, or the default only when it holds all of it. -/
theorem launch_gives_no_more {s : State} {c : Caller} {pa : PaneAuth} {w : Option Grants} {ch : Child}
    (ha : s.auth c = some pa) (h : s.launchGrants c w = .ok ch) :
    (ch = .default → pa.grants.covers s.config.defaults = true) ∧
      ∀ g, ch = .given g → pa.grants.covers g = true := by
  unfold State.launchGrants at h
  rw [ha] at h
  simp only at h
  constructor
  · intro hd; subst hd
    split at h
    · split at h
      · split at h
        · assumption
        · simp at h
      · simp at h
    · split at h <;> simp at h
  · intro g hg; subst hg
    split at h
    · split at h
      · split at h
        · simp at h
        · simp only [Except.ok.injEq, Child.given.injEq] at h; subst h; exact covers_meet _ _
      · simp only [Except.ok.injEq, Child.given.injEq] at h; subst h; exact covers_refl _
    · split at h
      · simp only [Except.ok.injEq, Child.given.injEq] at h; subst h; assumption
      · simp at h

theorem decide_child {s : State} {c : Caller} {v : String} {t : Target} {ch : Child} {d : Decision}
    (h : s.decide c v t = ⟨d, some ch⟩) : ∃ w, s.launchGrants c w = .ok ch := by
  unfold State.decide at h
  split at h
  · cases h
  · split at h
    · cases h
    · split at h
      · split at h
        · cases h
        · rename_i ch' hl
          split at h
          · cases h
          · cases h; exact ⟨_, hl⟩
      · split at h <;> cases h

/-- Granting is monotone: no call of a pane starts a pane that holds more
than the caller. -/
theorem started_pane_holds_no_more {s : State} {c : Caller} {pa : PaneAuth} {v : String} {t : Target}
    {d : Decision} {ch : Child} (ha : s.auth c = some pa) (h : s.decide c v t = ⟨d, some ch⟩) :
    (ch = .default → pa.grants.covers s.config.defaults = true) ∧
      ∀ g, ch = .given g → pa.grants.covers g = true := by
  rcases decide_child h with ⟨w, hl⟩
  exact launch_gives_no_more ha hl

/-- Only a holder of respond starts a pane with respond, so admin alone
never hands it on. -/
theorem respond_only_from_a_holder {s : State} {c : Caller} {pa : PaneAuth} {v : String} {t : Target}
    {d : Decision} {g : Grants} (ha : s.auth c = some pa) (h : s.decide c v t = ⟨d, some (.given g)⟩)
    (hg : g.respond = true) : pa.grants.respond = true :=
  covers_respond ((started_pane_holds_no_more ha h).2 g rfl) hg

/-- set-pane-grants from a pane gives no more than the pane holds, and a pane
without admin sets only its own grants. -/
theorem set_grants_gives_no_more {s : State} {c : Caller} {pa : PaneAuth} {t : Target}
    (ha : s.auth c = some pa) (h : s.setPaneGrants c t = .allow) :
    pa.grants.covers (t.grants.getD s.config.defaults) = true ∧
      (pa.grants.admin = false → t.window = "" ∨ t.window = pa.window) := by
  unfold State.setPaneGrants at h
  rw [ha] at h
  dsimp only at h
  cases hs : s.session? (if t.session = "" then pa.session else t.session) with
  | none => rw [hs] at h; cases h
  | some sess =>
    rw [hs] at h
    dsimp only at h
    by_cases hw0 : (if t.window = "" ∧ pa.session = sess.name then pa.window else t.window) = ""
    · rw [ite_eq_left_of_eq_true _ _ (eq_true hw0)] at h; cases h
    · rw [ite_eq_right_of_eq_false _ _ (eq_false hw0)] at h
      cases hw : s.paneIn? sess.name (if t.window = "" ∧ pa.session = sess.name then pa.window else t.window) with
      | none => rw [hw] at h; cases h
      | some w =>
        rw [hw] at h
        dsimp only at h
        by_cases h1 : w.id ≠ pa.window ∧ (!pa.grants.admin) = true
        · rw [ite_eq_left_of_eq_true _ _ (eq_true h1)] at h; cases h
        · rw [ite_eq_right_of_eq_false _ _ (eq_false h1)] at h
          by_cases h2 : (!pa.grants.covers (t.grants.getD s.config.defaults)) = true
          · rw [ite_eq_left_of_eq_true _ _ (eq_true h2)] at h; cases h
          · refine ⟨by simpa using h2, fun hn => ?_⟩
            have hid := (paneIn?_id hw).1
            simp only [hn, Bool.not_false, and_true, Classical.not_not] at h1
            by_cases e : t.window = ""
            · exact Or.inl e
            · right
              have hc : ¬(t.window = "" ∧ pa.session = sess.name) := fun hc => e hc.1
              rw [ite_eq_right_of_eq_false _ _ (eq_false hc)] at hid
              rw [← hid, h1]

/-! ## Typing -/

/-- holdTyping lets a call of a pane without admin through to another pane
in a named session only when that pane holds nothing the caller does not and
is not on a prompt, or the caller holds respond. -/
theorem holdTyping_target {s : State} {pa : PaneAuth} {v : String} {t t' : Target} {sess : Session} {w : Pane}
    (hn : pa.grants.admin = false) (h : s.holdTyping pa v t = .ok t') (hs : t.session ≠ "")
    (hss : s.session? t.session = some sess) (hw : s.paneIn? sess.name t'.window = some w)
    (hne : t'.window ≠ "") (ho : w.id ≠ pa.window) :
    pa.grants.covers (s.effective w.id) = true ∧ (w.needsInput = true → pa.grants.respond = true) := by
  unfold State.holdTyping at h
  split at h
  · cases h
  · simp only [State.targetSession, hs, ne_eq, not_false_eq_true, ↓reduceIte, hss] at h
    split at h
    · rename_i hnone
      cases h
      simp only [hne, ↓reduceIte] at hnone
      rw [hnone] at hw; cases hw
    · rename_i target ht
      split at h
      · cases h
      · rename_i href
        split at h
        · cases h
        · simp only [hn, Bool.false_eq_true, ↓reduceIte] at h
          cases h
          have e1 := (paneIn?_id ht).1
          simp only at hw
          rw [e1] at hw
          rw [ht] at hw
          injection hw with hw
          subst hw
          unfold State.typingRefused at href
          simp_all

theorem holdToPane_fills {v : String} {k : Kind} {t t' : Target} {own w : String} {f : String → Bool}
    (h : holdToPane v k t own w f = .ok t') (hd : (verbInfo v).session = true) (ho : own ≠ "") :
    t'.session ≠ "" := by
  unfold holdToPane at h
  split at h
  · cases h
  · rename_i t1 h1
    have r : t1.session ≠ "" := by
      unfold holdSession at h1
      repeat' split at h1
      all_goals (cases h1 <;> simp_all)
    repeat' split at h
    all_goals (cases h <;> first | exact r | skip)

/-- A pane without admin types only into itself, or into a pane that holds
nothing it does not and is not on a prompt unless the caller holds respond. -/
theorem types_only_into_no_more {s : State} {c : Caller} {pa : PaneAuth} {v : String} {t t' : Target}
    {sess : Session} {w : Pane}
    (ha : s.auth c = some pa) (hn : pa.grants.admin = false)
    (hv : v = "send-text" ∨ v = "send-keys" ∨ v = "run" ∨ v = "paste-buffer")
    (h : s.checkGrants c v t = .ok t') (hss : s.session? t'.session = some sess)
    (hw : s.paneIn? sess.name t'.window = some w) (hne : t'.window ≠ "") (ho : w.id ≠ pa.window) :
    pa.grants.covers (s.effective w.id) = true ∧ (w.needsInput = true → pa.grants.respond = true) := by
  have hnt : s.holdToOwnTree pa v t = none := by
    unfold State.holdToOwnTree
    rcases hv with rfl | rfl | rfl | rfl <;> simp
  have htv : typingVerb v = true := by rcases hv with rfl | rfl | rfl | rfl <;> decide
  have hd : (verbInfo v).session = true := by rcases hv with rfl | rfl | rfl | rfl <;> rfl
  unfold State.checkGrants at h
  rw [ha] at h
  simp only [hn, Bool.false_eq_true, ↓reduceIte, hnt] at h
  split at h
  · cases h
  · split at h
    · cases h
    · rename_i hp
      exfalso
      unfold State.grantGate at hp
      rcases hv with rfl | rfl | rfl | rfl <;> simp [verbInfo] at hp <;> (repeat' split at hp) <;> simp_all
    · split at h
      · cases h
      · rename_i hpo
        split at h
        · cases h
        · rename_i t1 h1
          have hs1 := holdToPane_fills h1 hd hpo
          have hT : s.holdTyping pa v t1 = .ok t' := by
            first | exact h | (simp only [htv, ↓reduceIte] at h; exact h)
          rw [holdTyping_keeps hT] at hss
          exact holdTyping_target hn hT hs1 hss hw hne ho

/-! ## Reports -/

theorem holdOwnRecord_window {s : State} {pa : PaneAuth} {t t' : Target} (hr : pa.grants.respond = false)
    (h : s.holdOwnRecord pa t = .ok t') : t'.window = pa.window := by
  unfold State.holdOwnRecord at h
  simp only [hr, Bool.false_eq_true, ↓reduceIte] at h
  cases hb : s.otherSession pa t.session <;> simp only [hb, Bool.false_eq_true, ↓reduceIte] at h
  · by_cases hw : t.window ≠ "" ∧ t.window ≠ pa.window
    · rw [ite_eq_left_of_eq_true _ _ (eq_true hw)] at h; cases h
    · rw [ite_eq_right_of_eq_false _ _ (eq_false hw)] at h; cases h; rfl
  · cases h

/-- A pane reports only about its own pane unless it holds respond, with
admin too: a report the checks let through names the caller's window. -/
theorem reports_only_for_own_pane {s : State} {c : Caller} {pa : PaneAuth} {v : String} {t t' : Target}
    (ha : s.auth c = some pa) (hk : (verbInfo v).grant = .self) (hr : pa.grants.respond = false)
    (h : s.checkGrants c v t = .ok t') : t'.window = pa.window := by
  have hnt : s.holdToOwnTree pa v t = none := by
    unfold State.holdToOwnTree
    split
    · rfl
    · have h1 : v ≠ "close-window" := by intro e; subst e; simp [verbInfo] at hk
      have h2 : v ≠ "wait-for" := by intro e; subst e; simp [verbInfo] at hk
      simp [h1, h2]
  have htv : typingVerb v = false := by
    unfold typingVerb
    simp only [List.mem_cons, List.not_mem_nil, or_false, decide_eq_false_iff_not]
    intro e
    rcases e with e | e | e | e | e | e | e <;> subst e <;> simp [verbInfo] at hk
  unfold State.checkGrants at h
  rw [ha] at h
  simp only at h
  split at h
  · simp only [htv, Bool.false_eq_true, ↓reduceIte] at h
    exact holdOwnRecord_window hr h
  · rw [hnt] at h
    simp only at h
    split at h
    · cases h
    · split at h
      · cases h
      · rename_i hp
        exfalso
        unfold State.grantGate at hp
        simp [hk] at hp
      · split at h
        · cases h
        · split at h
          · cases h
          · rename_i t1 h1
            simp only [htv, Bool.false_eq_true, ↓reduceIte] at h
            cases h
            rw [hk] at h1
            unfold holdToPane at h1
            split at h1
            · cases h1
            · simp only at h1
              repeat' split at h1
              all_goals (cases h1 <;> first | rfl | (rename_i hw; simp_all) | skip)

end GrantSpec
