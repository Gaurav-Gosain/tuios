/-
Pane grants and verb scopes, as an executable model of the checks a JSON verb
passes before its handler runs, and of the checks the start verbs and
set-pane-grants make in their handlers. The Go code is checked against this
model by the differential test internal/session/grants_spec_test.go (build
tag spec).

The decision is spread over these places in internal/session, in the order a
call meets them:

  - checkGrants (pane_grants.go): what the caller's pane holds. It calls
    holdToOwnTree (own_tree.go), holdToPane (conn_scope.go),
    holdTypingTarget and holdOwnRecord (pane_grants.go).
  - checkScope (conn_scope.go): what a restricted connection kept. It calls
    holdToPane too.
  - launchGrants (pane_grants.go) and depthRefusal (agent_depth.go), in the
    handlers of start-agent and fan.
  - verbSetPaneGrants (pane_grants.go), in its handler.

The model keeps the Go names and the Go order of the checks, so a reader can
hold the two side by side. Strings stay strings: a pane is a window id, a
session a name, and "" is none, as in Go.

Out of the model: callers over a link, hosted panes and native plugins
(checkGrants holds them before these rules), the files folder part of the
typing rule, the agent features switched off, and a prompt held on screen
after its state changed (promptStillShown).
-/
namespace GrantSpec

/-- Grants: the pane grants, one flag each (Grants in pane_grants.go). -/
structure Grants where
  read : Bool := false
  write : Bool := false
  mail : Bool := false
  fan : Bool := false
  respond : Bool := false
  files : Bool := false
  task : Bool := false
  admin : Bool := false
  deriving DecidableEq, Repr, Inhabited

/-- expand: admin implies every grant but respond. -/
def Grants.expand (g : Grants) : Grants :=
  if g.admin then { g with read := true, write := true, mail := true, fan := true, files := true, task := true }
  else g

/-- Every flag of a is in b. -/
def Grants.le (a b : Grants) : Bool :=
  (!a.read || b.read) && (!a.write || b.write) && (!a.mail || b.mail) && (!a.fan || b.fan) && (!a.respond || b.respond) &&
  (!a.files || b.files) && (!a.task || b.task) && (!a.admin || b.admin)

/-- The flags both hold. -/
def Grants.meet (a b : Grants) : Grants :=
  ⟨a.read && b.read, a.write && b.write, a.mail && b.mail, a.fan && b.fan, a.respond && b.respond,
   a.files && b.files, a.task && b.task, a.admin && b.admin⟩

/-- Covers: g holds everything o holds, so a holder of g may give o. -/
def Grants.covers (g o : Grants) : Bool := o.expand.le g.expand

def Grants.adminOnly : Grants := { admin := true }

/-- What a verb does, for a restricted connection and for a pane
(scopeKind in conn_scope.go). -/
inductive Kind where
  | deny | open_ | global | read | self | mail | write | launch | bufferRead | bufferWrite | task
  deriving DecidableEq, Repr, Inhabited

/-- One verb: its scope on the verb registry (verbScope, which checkScope
reads), its kind for a pane (grantKind, which checkGrants reads), and whether
it declares a session and a from parameter. -/
structure VerbInfo where
  scope : Kind
  grant : Kind
  session : Bool
  from_ : Bool
  deriving Repr

/-- The verbs the model knows, with the class the table "What a restricted
connection may call" in docs/protocol.md gives each. Any other name is
refused, as a name that is not a verb is. -/
def verbInfo : String → VerbInfo
  | "hello" | "pane-grants" => ⟨.open_, .open_, false, false⟩
  | "set-pane-grants" => ⟨.deny, .open_, true, false⟩
  | "resolve-pane" => ⟨.deny, .open_, false, false⟩
  | "list-sessions" => ⟨.global, .global, false, false⟩
  | "list-agents" | "capture-pane" | "get-agent-state" | "read-agent-messages"
  | "compare-fan" | "wait-for" => ⟨.read, .read, true, false⟩
  | "set-agent-state" | "ask-human" => ⟨.self, .self, true, false⟩
  | "request-approval" => ⟨.deny, .self, true, false⟩
  | "send-agent-message" => ⟨.mail, .mail, true, true⟩
  | "send-text" | "send-keys" | "run" | "respond" | "paste-buffer" => ⟨.write, .write, true, false⟩
  | "start-agent" | "set-fan-verdict" => ⟨.launch, .launch, true, false⟩
  | "fan" => ⟨.launch, .launch, false, false⟩
  | "list-buffers" => ⟨.bufferRead, .bufferRead, false, false⟩
  | "set-buffer" => ⟨.bufferWrite, .bufferWrite, false, false⟩
  | "add-task" | "claim-task" | "remove-task" => ⟨.task, .task, true, false⟩
  | "close-window" | "kill-session" | "set-option" | "new-window" => ⟨.deny, .deny, true, false⟩
  | "transfer-start" | "transfer-list" => ⟨.deny, .deny, false, false⟩
  | _ => ⟨.deny, .deny, false, false⟩

/-- The verbs the model knows, for the tests. -/
def verbs : List String :=
  ["hello", "pane-grants", "set-pane-grants", "resolve-pane", "list-sessions", "list-agents",
   "capture-pane", "get-agent-state", "read-agent-messages", "compare-fan", "wait-for",
   "set-agent-state", "ask-human", "request-approval", "send-agent-message", "send-text",
   "send-keys", "run", "respond", "paste-buffer", "start-agent", "set-fan-verdict", "fan",
   "list-buffers", "set-buffer", "add-task", "claim-task", "remove-task", "close-window",
   "kill-session", "set-option", "new-window", "transfer-start", "transfer-list"]

/-- typingVerbs. -/
def typingVerb (v : String) : Bool :=
  v ∈ ["send-text", "send-keys", "ask-agent", "run", "queue-prompt", "send-review", "paste-buffer"]

/-- paneTypingVerbs: the typing verbs that type into a prompt only with
allow_blocked. -/
def paneTypingVerb (v : String) : Bool := v ∈ ["send-text", "send-keys", "paste-buffer"]

/-- judgeReadVerbs, cut to the verbs the model knows. -/
def judgeReadVerb (v : String) : Bool := v ∈ ["compare-fan", "capture-pane", "get-agent-state"]

/-- transferGrantVerbs, cut to the verbs the model knows. -/
def transferVerb (v : String) : Bool := v ∈ ["transfer-start", "transfer-list"]

/-- The verbs that start an agent and so check the depth and the grants they
give. -/
def startVerb (v : String) : Bool := v ∈ ["start-agent", "fan"]

/-- readsBuffers. -/
def readsBuffers (v : String) : Bool := (verbInfo v).scope == .bufferRead || v == "paste-buffer"

/-- WorktreeInfo: the fields reach reads. -/
structure Worktree where
  /-- LaunchedFromID: the ID of the session whose pane ran the fan. -/
  launchedFrom : String
  group : String
  managed : Bool
  repoRoot : String
  fanId : String
  /-- JudgeSession (an ID) and JudgeWindow: the judge of the fan. -/
  judgeSession : String
  judgeWindow : String
  deriving DecidableEq, Repr, Inhabited

structure Session where
  name : String
  id : String
  /-- FocusedWindowID. -/
  focused : String
  wt : Option Worktree
  deriving DecidableEq, Repr, Inhabited

/-- AgentLink: the root of the chain and the depth. -/
structure Link where
  root : String
  depth : Nat
  deriving DecidableEq, Repr, Inhabited

/-- A pane: a window of a session, with the fields the checks read. -/
structure Pane where
  id : String
  /-- The name of the session that holds the window. -/
  session : String
  /-- StartedBy.Window. -/
  startedBy : String
  link : Option Link
  /-- ChainDepth. -/
  depth : Nat
  /-- The grants it was given; none is the default. -/
  grants : Option Grants
  /-- AgentState is needs_input. -/
  needsInput : Bool
  /-- AgentKind is approval. -/
  approval : Bool
  deriving DecidableEq, Repr, Inhabited

/-- [agents.permissions] and agents.max_depth. -/
structure Config where
  strict : Bool
  /-- The grants list of [agents.permissions]. -/
  list : Grants
  /-- agents.max_depth; 0 is no cap. -/
  maxDepth : Nat
  deriving DecidableEq, Repr, Inhabited

structure State where
  sessions : List Session
  panes : List Pane
  config : Config
  deriving Repr, Inhabited

/-- restrict-connection: what the connection kept. -/
structure Restriction where
  own : Bool
  readOnly : Bool
  deriving DecidableEq, Repr, Inhabited

/-- Who calls: the pane its process runs in ("" for the person, outside
every pane), and the restriction of its connection. -/
structure Caller where
  pane : String
  restriction : Option Restriction
  deriving Repr, Inhabited

/-- The parameters of a call that the checks read. grants is the grants
parameter of start-agent, fan and set-pane-grants; none is no grants, which
set-pane-grants sends as reset. -/
structure Target where
  session : String := ""
  window : String := ""
  from_ : String := ""
  select : Bool := false
  allSessions : Bool := false
  allowBlocked : Bool := false
  grants : Option Grants := none
  deriving Repr, Inhabited

/-- Why a call is refused: the error code it is answered with. error is any
other code, such as session_not_found. -/
inductive Refusal where
  | forbidden | invalid | blocked | error
  deriving DecidableEq, Repr, Inhabited

/-- What the call is answered with: served, or the code of the refusal. -/
inductive Decision where
  | allow | forbidden | invalid | blocked | error
  deriving DecidableEq, Repr, Inhabited

def Refusal.toDecision : Refusal → Decision
  | .forbidden => .forbidden | .invalid => .invalid | .blocked => .blocked | .error => .error

@[simp] theorem Refusal.toDecision_ne_allow (d : Refusal) : (d.toDecision = .allow) = False := by
  cases d <;> simp [Refusal.toDecision]

/-- The grants a started pane holds: the default, or a set. -/
inductive Child where
  | none | default | given (g : Grants)
  deriving DecidableEq, Repr, Inhabited

def State.session? (s : State) (name : String) : Option Session := s.sessions.find? (·.name == name)
def State.sessionById? (s : State) (id : String) : Option Session := s.sessions.find? (·.id == id)
def State.pane? (s : State) (id : String) : Option Pane := s.panes.find? (·.id == id)
/-- A pane of the named session. -/
def State.paneIn? (s : State) (session id : String) : Option Pane :=
  s.panes.find? (fun p => p.id == id && p.session == session)

/-- defaults: admin under open, the grants list under strict. -/
def Config.defaults (c : Config) : Grants := if c.strict then c.list else Grants.adminOnly

/-- effective. -/
def State.effective (s : State) (id : String) : Grants :=
  match s.pane? id with
  | some { grants := some g, .. } => g
  | _ => s.config.defaults

/-- paneAuth: the pane a caller runs in and what it holds. -/
structure PaneAuth where
  window : String
  session : String
  sessionId : String
  grants : Grants
  deriving Repr

/-- paneAuthority: none for the person. -/
def State.auth (s : State) (c : Caller) : Option PaneAuth :=
  if c.pane = "" then none
  else match s.pane? c.pane with
    | some p =>
      let id := match s.session? p.session with | some x => x.id | none => ""
      some ⟨p.id, p.session, id, s.effective p.id⟩
    | none => none

/-- sameFan. -/
def sameFan (a b : Worktree) : Bool :=
  a.group != "" && a.group == b.group && a.repoRoot == b.repoRoot && a.fanId == b.fanId

/-- sessionInScope: target is own, was launched from own, or shares own's
fan group. -/
def State.inScope (s : State) (own target : String) : Bool :=
  if own = "" ∨ target = "" then false
  else if target = own then true
  else match s.session? target with
    | none => false
    | some t => match t.wt with
      | none => false
      | some tw =>
        let o := s.session? own
        if tw.launchedFrom != "" && (match o with | some o => o.id == tw.launchedFrom | none => false) then true
        else if tw.group = "" ∨ !tw.managed then false
        else match o with
          | none => false
          | some o => match o.wt with
            | some ow => ow.managed && sameFan ow tw
            | none => false

/-- judgeReaches: pa is the live judge pane of the fan target belongs to. -/
def State.judgeReaches (s : State) (pa : PaneAuth) (target : String) : Bool :=
  if pa.sessionId = "" ∨ pa.window = "" then false
  else match s.session? target with
    | some { wt := some wt, .. } =>
      if wt.judgeSession = "" ∨ wt.judgeSession ≠ pa.sessionId ∨ wt.judgeWindow ≠ pa.window then false
      else match s.sessionById? wt.judgeSession with
        | some j => (s.paneIn? j.name wt.judgeWindow).isSome
        | none => false
    | _ => false

/-- paneOriginMaxDepth: how far the walk up StartedBy goes. -/
def maxWalk : Nat := 64

/-- The walk of inOwnTree up StartedBy from up, with the panes it has seen. -/
def State.walk (s : State) (root : String) : String → List String → Nat → Bool
  | _, _, 0 => false
  | up, seen, n + 1 =>
    if up = "" then false
    else if up = root then true
    else if up ∈ seen then false
    else match s.pane? up with
      | none => false
      | some q => s.walk root q.startedBy (up :: seen) n

/-- inOwnTree: root started the pane id, or started a pane that did, down
the chain, or the pane's agent link names root as its root. root itself is
not in its tree. -/
def State.inOwnTree (s : State) (id root : String) : Bool :=
  if root = "" ∨ id = root then false
  else match s.pane? id with
    | none => false
    | some p =>
      if (match p.link with | some l => l.root == root | none => false) then true
      else s.walk root p.startedBy [id] maxWalk

/-- The session part of holdToPane: a named session must be in reach, and a
verb that declares a session and names none gets the caller's own. -/
def holdSession (verb : String) (t : Target) (own : String) (reach : String → Bool) : Except Refusal Target :=
  if t.session ≠ "" then (if reach t.session then .ok t else .error .forbidden)
  else if (verbInfo verb).session then
    (if reach own then .ok { t with session := own } else .error .forbidden)
  else .ok t

/-- holdToPane: the session and window parameters of a call from the pane
window in session own, held to the sessions reach allows. -/
def holdToPane (verb : String) (kind : Kind) (t : Target) (own window : String)
    (reach : String → Bool) : Except Refusal Target :=
  match holdSession verb t own reach with
  | .error d => .error d
  | .ok t =>
    if t.allSessions then .error .forbidden
    else if t.select ∧ verb ≠ "list-agents" then .error .forbidden
    else match kind with
      | .self =>
        if t.session ≠ own then .error .forbidden
        else if t.window = "" then .ok { t with window := window }
        else if t.window ≠ window then .error .forbidden
        else .ok t
      | .mail | .write =>
        if (verbInfo verb).from_ then
          if t.session = own then
            if t.from_ = "" then .ok { t with from_ := window }
            else if t.from_ ≠ window then .error .forbidden
            else .ok t
          else if t.from_ ≠ "" ∧ t.from_ ≠ window then .error .forbidden
          else .ok t
        else .ok t
      | _ => .ok t

/-- typingRefusal: a pane types into itself, and into another pane only when
that pane holds nothing it does not and is not on a prompt, or it holds
respond. A child agent on an approval is the person's alone. -/
def State.typingRefused (s : State) (pa : PaneAuth) (target : Pane) : Bool :=
  if target.id = pa.window then false
  else if target.link.isSome ∧ target.needsInput ∧ target.approval then true
  else if !pa.grants.admin && !pa.grants.covers (s.effective target.id) then true
  else target.needsInput && !pa.grants.respond

/-- findTargetSession for a caller in a pane: the named session, or the
caller's own. -/
def State.targetSession (s : State) (pa : PaneAuth) (name : String) : Option Session :=
  if name ≠ "" then s.session? name else s.session? pa.session

/-- typingBlocked: a send-text, send-keys or paste-buffer into another pane
on a prompt needs allow_blocked. -/
def typingBlocked (pa : PaneAuth) (verb : String) (t : Target) (target : Pane) : Bool :=
  paneTypingVerb verb && !t.allowBlocked && target.id != pa.window && target.needsInput

/-- holdTypingTarget. -/
def State.holdTyping (s : State) (pa : PaneAuth) (verb : String) (t : Target) : Except Refusal Target :=
  if t.window = "" ∧ verb ∈ ["send-text", "send-keys", "run"] then .error .invalid
  else match s.targetSession pa t.session with
    | none => .ok t
    | some sess =>
      match s.paneIn? sess.name (if t.window = "" then sess.focused else t.window) with
      | none => .ok t
      | some target =>
        if s.typingRefused pa target then .error .forbidden
        else if typingBlocked pa verb t target then .error .blocked
        else if pa.grants.admin then .ok t
        else .ok { t with window := target.id }

/-- A session the call names that is not the pane's own, or that does not
resolve. -/
def State.otherSession (s : State) (pa : PaneAuth) (name : String) : Bool :=
  name ≠ "" && (match s.session? name with | some x => x.name != pa.session | none => true)

/-- holdOwnRecord: a report from a pane with admin is about its own pane,
unless it holds respond. -/
def State.holdOwnRecord (s : State) (pa : PaneAuth) (t : Target) : Except Refusal Target :=
  if pa.grants.respond then .ok t
  else if s.otherSession pa t.session then
    .error .forbidden
  else if t.window ≠ "" ∧ t.window ≠ pa.window then .error .forbidden
  else .ok { t with session := pa.session, window := pa.window }

/-- The window close-window names, found as holdToOwnTree finds it: in the
named session or the caller's own, else in whichever session holds it. -/
def State.closeTarget (s : State) (pa : PaneAuth) (t : Target) : Option Pane :=
  if t.window = "" then none
  else
    let name := if t.session = "" then pa.session else t.session
    match (s.session? name).bind (fun x => s.paneIn? x.name t.window) with
    | some p => some p
    | none => s.pane? t.window

/-- holdToOwnTree: a pane with fan closes the panes of its own tree, and
waits on them by selector. none when the rule does not apply. -/
def State.holdToOwnTree (s : State) (pa : PaneAuth) (verb : String) (t : Target) :
    Option (Except Refusal Target) :=
  if !pa.grants.expand.fan || pa.window = "" then none
  else if verb = "close-window" then
    match s.closeTarget pa t with
    | none => none
    | some w =>
      if s.inOwnTree w.id pa.window then some (.ok { t with session := w.session, window := w.id })
      else some (.error .forbidden)
  else if verb = "wait-for" ∧ t.select then some (.ok t)
  else none

/-- paneWriteReach. -/
def State.writeReach (s : State) (pa : PaneAuth) (target : String) : Bool :=
  if pa.grants.admin then true
  else if target = pa.session then pa.grants.expand.write
  else if s.inScope pa.session target then pa.grants.expand.fan
  else false

/-- holdMailSend and holdMailListing (mail_grant.go): a pane holding mail
leaves mail in any session as itself, and lists the agent panes of every
session to address it. A listing inside its read reach is left to the read
rule. none when the rule does not apply. The host and all parameters are out
of the model. -/
def State.holdMail (s : State) (pa : PaneAuth) (verb : String) (t : Target) :
    Option (Except Refusal Target) :=
  if !pa.grants.expand.mail || pa.session = "" then none
  else if verb = "send-agent-message" then
    if t.from_ ≠ "" ∧ t.from_ ≠ pa.window then some (.error .forbidden)
    else if !t.select ∧ t.session = "" then some (.ok { t with from_ := pa.window, session := pa.session })
    else some (.ok { t with from_ := pa.window })
  else if verb = "list-agents" then
    let wide := t.allSessions || (t.select && t.session == "")
    let target := if t.session = "" then pa.session else t.session
    if !wide && pa.grants.expand.read && s.inScope pa.session target then none
    else if wide then some (.ok t)
    else some (.ok { t with session := target })
  else none

/-- What the grants of a pane without admin let a verb do: pass with no
session check, or reach the sessions the function allows. -/
inductive Gate where
  | pass
  | reach (f : String → Bool)

/-- The kind switch of checkGrants, for a pane without admin. -/
def State.grantGate (s : State) (pa : PaneAuth) (verb : String) : Except Refusal Gate :=
  let g := pa.grants.expand
  match (verbInfo verb).grant with
  | .open_ | .bufferRead => .ok .pass
  | .bufferWrite => if g.write then .ok .pass else .error .forbidden
  | .deny | .global => if transferVerb verb && g.files then .ok .pass else .error .forbidden
  | .self => .ok (.reach (· == pa.session))
  | .read =>
    if !g.read then .error .forbidden
    else .ok (.reach fun x => s.inScope pa.session x || judgeReadVerb verb && s.judgeReaches pa x)
  | .launch =>
    let verdict := verb == "set-fan-verdict"
    if !g.fan && !verdict then .error .forbidden
    else .ok (.reach fun x => verdict && s.judgeReaches pa x || g.fan && s.inScope pa.session x)
  | .task =>
    if !g.task then .error .forbidden
    else .ok (.reach (· == pa.session))
  | .mail | .write =>
    if verb = "respond" then
      if !pa.grants.respond then .error .forbidden
      else .ok (.reach fun x => x == pa.session || g.fan && s.inScope pa.session x)
    else if !g.write && !g.fan then .error .forbidden
    else .ok (.reach (s.writeReach pa))

/-- checkGrants: a call from a pane, held to what the pane holds. -/
def State.checkGrants (s : State) (c : Caller) (verb : String) (t : Target) : Except Refusal Target :=
  match s.auth c with
  | none => .ok t
  | some pa =>
    if pa.grants.admin then
      if typingVerb verb then s.holdTyping pa verb t
      else if (verbInfo verb).grant = .self then s.holdOwnRecord pa t
      else .ok t
    else match s.holdToOwnTree pa verb t with
      | some r => r
      | none =>
        if readsBuffers verb && !pa.grants.expand.read then .error .forbidden
        else match s.holdMail pa verb t with
        | some r => r
        | none =>
          match s.grantGate pa verb with
          | .error d => .error d
          | .ok .pass => .ok t
          | .ok (.reach f) =>
            if pa.session = "" then .error .forbidden
            else match holdToPane verb (verbInfo verb).grant t pa.session pa.window f with
              | .error d => .error d
              | .ok t' => if typingVerb verb then s.holdTyping pa verb t' else .ok t'

/-- The session of the caller's pane, "" for the person. -/
def State.callerSession (s : State) (c : Caller) : String :=
  match s.pane? c.pane with
  | some p => if c.pane = "" then "" else p.session
  | none => ""

/-- checkScope: a call on a restricted connection, held to the restriction. -/
def State.checkScope (s : State) (c : Caller) (verb : String) (t : Target) : Except Refusal Target :=
  match c.restriction with
  | none => .ok t
  | some r =>
    let kind := (verbInfo verb).scope
    if kind = .deny then .error .forbidden
    else if kind = .open_ then .ok t
    else if r.readOnly ∧ (kind = .write ∨ kind = .launch ∨ kind = .task ∨ kind = .bufferWrite) then
      .error .forbidden
    else if !r.own then .ok t
    else if kind = .global then .error .forbidden
    else if kind = .bufferWrite ∨ readsBuffers verb then .error .forbidden
    else if s.callerSession c = "" then .error .forbidden
    else holdToPane verb kind t (s.callerSession c) c.pane (s.inScope (s.callerSession c))

/-- agentLinkMaxDepth: setAgentMaxDepth holds the cap to it. -/
def linkMaxDepth : Nat := 8

/-- windowChainDepth of the caller's pane, 0 for the person. -/
def State.callerDepth (s : State) (c : Caller) : Nat :=
  if c.pane = "" then 0
  else match s.pane? c.pane with
    | some p => max p.depth (match p.link with | some l => l.depth | none => 0)
    | none => 0

/-- depthRefusal: the pane a start would make is deeper than the cap. -/
def State.tooDeep (s : State) (c : Caller) : Bool :=
  let limit := min s.config.maxDepth linkMaxDepth
  limit > 0 && s.callerDepth c + 1 > limit

/-- launchGrants: what a started pane holds, or the refusal. A pane gives
only what it holds. -/
def State.launchGrants (s : State) (c : Caller) (want : Option Grants) : Except Refusal Child :=
  match s.auth c with
  | none => .ok (match want with | some g => .given g | none => .default)
  | some pa =>
    match want with
    | none =>
      if pa.grants.admin then
        -- clampDefaultGrants
        let d := s.config.defaults
        if pa.grants.covers d then .ok .default else .ok (.given (d.expand.meet pa.grants.expand))
      else .ok (.given pa.grants)
    | some g => if pa.grants.covers g then .ok (.given g) else .error .forbidden

/-- verbSetPaneGrants, the checks before it sets anything. -/
def State.setPaneGrants (s : State) (c : Caller) (t : Target) : Decision :=
  let pa := s.auth c
  let name := match pa with
    | some pa => if t.session = "" then pa.session else t.session
    | none => t.session
  match s.session? name with
  | none => .error
  | some sess =>
    let window := match pa with
      | some pa => if t.window = "" ∧ pa.session = sess.name then pa.window else t.window
      | none => t.window
    if window = "" then .invalid
    else match s.paneIn? sess.name window with
      | none => .error
      | some w =>
        let next := t.grants.getD s.config.defaults
        match pa with
        | none => .allow
        | some pa =>
          if w.id ≠ pa.window ∧ !pa.grants.admin then .forbidden
          else if !pa.grants.covers next then .forbidden
          else .allow

/-- The answer to a call: the decision, and for a start the grants the new
pane holds. -/
structure Answer where
  decision : Decision
  child : Option Child
  deriving DecidableEq, Repr

/-- decide: checkGrants, then checkScope, then the checks of the handler. -/
def State.decide (s : State) (c : Caller) (verb : String) (t : Target) : Answer :=
  match s.checkGrants c verb t with
  | .error d => ⟨d.toDecision, none⟩
  | .ok t1 =>
  match s.checkScope c verb t1 with
  | .error d => ⟨d.toDecision, none⟩
  | .ok t' =>
    if startVerb verb then
      match s.launchGrants c t'.grants with
      | .error d => ⟨d.toDecision, none⟩
      | .ok ch => if s.tooDeep c then ⟨.forbidden, none⟩ else ⟨.allow, some ch⟩
    else if verb = "set-pane-grants" then ⟨s.setPaneGrants c t', none⟩
    else ⟨.allow, none⟩

/-- allowed: the call is served. -/
def allowed (s : State) (c : Caller) (verb : String) (t : Target) : Bool :=
  (s.decide c verb t).decision == .allow

end GrantSpec
