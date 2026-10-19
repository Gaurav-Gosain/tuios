// Code generated from pele tests/render/*-security.test.ts at 458322a5. DO NOT EDIT.

package sketch

var sequenceTemplates = []func(p string) string{
	func(p string) string { return "sequenceDiagram\n  participant " + p + "\n  A->>B: x" },
	func(p string) string { return "sequenceDiagram\n  actor " + p + "\n  A->>B: x" },
	func(p string) string { return "sequenceDiagram\n  participant A as " + p + "\n  A->>B: x" },
	func(p string) string { return "sequenceDiagram\n  actor A as " + p + "\n  A->>B: x" },
	func(p string) string {
		return "sequenceDiagram\n  participant A@{ \"type\": " + jsonText(p) + " }\n  A->>B: x"
	},
	func(p string) string {
		return "sequenceDiagram\n  participant A@{ \"type\": \"database\", \"alias\": " + jsonText(p) + " }\n  A->>B: x"
	},
	func(p string) string {
		return "sequenceDiagram\n  participant A@{ " + jsonText(p) + ": \"x\", \"alias\": \"y\" }\n  A->>B: x"
	},
	func(p string) string { return "sequenceDiagram\n  participant A@{ " + p + " }\n  A->>B: x" },
	func(p string) string {
		return "sequenceDiagram\n  participant A@{ \"type\": \"queue\" } as " + p + "\n  A->>B: x"
	},
	func(p string) string { return "sequenceDiagram\n  " + p + "->>B: x" },
	func(p string) string { return "sequenceDiagram\n  A->>" + p + ": x" },
	func(p string) string { return "sequenceDiagram\n  A->>B: " + p },
	func(p string) string {
		return "sequenceDiagram\n  A->>B: wrap: " + p + " and some more words to wrap around the corner of the label"
	},
	func(p string) string { return "sequenceDiagram\n  A->>A: " + p },
	func(p string) string { return "sequenceDiagram\n  A-x+B: " + p + "\n  B--)-A: " + p },
	func(p string) string { return "sequenceDiagram\n  A()->>()B: " + p },
	func(p string) string { return "sequenceDiagram\n  A " + p + " B: x" },
	func(p string) string { return "sequenceDiagram\n  Note left of A: " + p },
	func(p string) string { return "sequenceDiagram\n  Note right of A: " + p },
	func(p string) string { return "sequenceDiagram\n  Note over A,B: " + p },
	func(p string) string { return "sequenceDiagram\n  Note over " + p + ": x" },
	func(p string) string { return "sequenceDiagram\n  Note over A: wrap: " + p },
	func(p string) string { return "sequenceDiagram\n  loop " + p + "\n  A->>B: x\n  end" },
	func(p string) string {
		return "sequenceDiagram\n  alt " + p + "\n  A->>B: x\n  else " + p + "\n  B->>A: y\n  end"
	},
	func(p string) string { return "sequenceDiagram\n  opt " + p + "\n  A->>B: x\n  end" },
	func(p string) string {
		return "sequenceDiagram\n  par " + p + "\n  A->>B: x\n  and " + p + "\n  B->>A: y\n  end"
	},
	func(p string) string { return "sequenceDiagram\n  par_over " + p + "\n  A->>B: x\n  end" },
	func(p string) string {
		return "sequenceDiagram\n  critical " + p + "\n  A->>B: x\n  option " + p + "\n  B->>A: y\n  end"
	},
	func(p string) string { return "sequenceDiagram\n  break " + p + "\n  A->>B: x\n  end" },
	func(p string) string { return "sequenceDiagram\n  rect " + p + "\n  A->>B: x\n  end" },
	func(p string) string { return "sequenceDiagram\n  rect rgb(0, 0, 0)" + p + "\n  A->>B: x\n  end" },
	func(p string) string { return "sequenceDiagram\n  box " + p + "\n  participant A\n  end\n  A->>B: x" },
	func(p string) string {
		return "sequenceDiagram\n  box red " + p + "\n  participant A\n  end\n  A->>B: x"
	},
	func(p string) string {
		return "sequenceDiagram\n  box rgb(1, 2, 3)" + p + "\n  participant A\n  end\n  A->>B: x"
	},
	func(p string) string {
		return "sequenceDiagram\n  box rgb(" + p + ") T\n  participant A\n  end\n  A->>B: x"
	},
	func(p string) string { return "sequenceDiagram\n  title " + p + "\n  A->>B: x" },
	func(p string) string { return "sequenceDiagram\n  title: " + p + "\n  A->>B: x" },
	func(p string) string {
		return "sequenceDiagram\n  accTitle: " + p + "\n  accDescr: " + p + "\n  A->>B: x"
	},
	func(p string) string { return "sequenceDiagram\n  accDescr {\n " + p + "\n }\n  A->>B: x" },
	func(p string) string {
		return "sequenceDiagram\n  participant A\n  links A: {" + jsonText(p) + ": " + jsonText(p) + "}\n  A->>B: x"
	},
	func(p string) string { return "sequenceDiagram\n  participant A\n  links A: " + p + "\n  A->>B: x" },
	func(p string) string {
		return "sequenceDiagram\n  participant A\n  link A: " + p + " @ " + p + "\n  A->>B: x"
	},
	func(p string) string {
		return "sequenceDiagram\n  participant A\n  link A: Docs @ " + p + "\n  A->>B: x"
	},
	func(p string) string {
		return "sequenceDiagram\n  participant A\n  properties A: {\"class\": " + jsonText(p) + ", " + jsonText(p) + ": " + jsonText(p) + "}\n  A->>B: x"
	},
	func(p string) string {
		return "sequenceDiagram\n  participant A\n  properties A: " + p + "\n  A->>B: x"
	},
	func(p string) string { return "sequenceDiagram\n  participant A\n  details A: " + p + "\n  A->>B: x" },
	func(p string) string { return "sequenceDiagram\n  autonumber " + p + "\n  A->>B: x" },
	func(p string) string { return "sequenceDiagram\n  autonumber\n  A->>B: " + p + "\n  A->>A: " + p },
	func(p string) string {
		return "sequenceDiagram\n  A->>B: x\n  create participant " + p + "\n  A->>" + p + ": y"
	},
	func(p string) string {
		return "sequenceDiagram\n  A->>B: x\n  create actor C as " + p + "\n  A->>C: y\n  destroy C\n  C-xA: " + p
	},
	func(p string) string { return "sequenceDiagram\n  activate " + p + "\n  A->>B: x" },
	func(p string) string { return "sequenceDiagram\n  A->>B: x\n  %% " + p + "\n  B->>A: y # " + p },
	func(p string) string { return "sequenceDiagram " + p + "\n  A->>B: x" },
	func(p string) string {
		return "---\ntitle: " + jsonText(p) + "\nconfig:\n  sequence:\n    messageAlign: " + jsonText(p) + "\n    noteAlign: " + jsonText(p) + "\n---\nsequenceDiagram\n  A->>B: x"
	},
	func(p string) string {
		return "%%{init: {\"sequence\": {\"width\": " + jsonText(p) + ", \"actorMargin\": " + jsonText(p) + ", \"mirrorActors\": " + jsonText(p) + ", \"messageFontFamily\": " + jsonText(p) + "}, \"themeVariables\": {\"fontFamily\": " + jsonText(p) + "}}}%%\nsequenceDiagram\n  A->>B: x"
	},
}

var stateTemplates = []func(p string) string{
	func(p string) string { return "stateDiagram-v2\n  " + p + " --> b" },
	func(p string) string { return "stateDiagram-v2\n  a --> " + p },
	func(p string) string { return "stateDiagram-v2\n  " + p },
	func(p string) string { return "stateDiagram-v2\n  a : " + p },
	func(p string) string { return "stateDiagram-v2\n  a : title\n  a : " + p + "\n  a : " + p },
	func(p string) string { return "stateDiagram-v2\n  a --> b : " + p },
	func(p string) string { return "stateDiagram-v2\n  a --> a : " + p },
	func(p string) string { return "stateDiagram-v2\n  state \"" + p + "\" as a\n  a --> b" },
	func(p string) string { return "stateDiagram-v2\n  state \"x\" as " + p + "\n" },
	func(p string) string { return "stateDiagram-v2\n  state \"x\" as a:" + p + "\n" },
	func(p string) string { return "stateDiagram-v2\n  state " + p + " {\n  a --> b\n  }" },
	func(p string) string {
		return "stateDiagram-v2\n  state \"" + p + "\" as c {\n  a --> b\n  }\n  c --> c : " + p
	},
	func(p string) string {
		return "stateDiagram-v2\n  state c {\n  a --> b : " + p + "\n  --\n  " + p + " --> d\n  }\n  c --> a : " + p
	},
	func(p string) string {
		return "stateDiagram-v2\n  state " + p + " <<fork>>\n  state " + p + " <<join>>\n  [*] --> " + p
	},
	func(p string) string { return "stateDiagram-v2\n  state " + p + " <<choice>>\n  a --> " + p },
	func(p string) string { return "stateDiagram-v2\n  state " + p + " [[choice]]\n" },
	func(p string) string { return "stateDiagram-v2\n  a --> b\n  note right of a : " + p },
	func(p string) string {
		return "stateDiagram-v2\n  a --> b\n  note left of a\n  " + p + "\n  " + p + "\n  end note"
	},
	func(p string) string { return "stateDiagram-v2\n  note right of " + p + " : text" },
	func(p string) string { return "stateDiagram-v2\n  a --> b\n  note \"" + p + "\" as " + p },
	func(p string) string { return "stateDiagram-v2\n  a:::" + p + " --> b" },
	func(p string) string { return "stateDiagram-v2\n  [*]:::" + p + " --> b:::" + p },
	func(p string) string { return "stateDiagram-v2\n  a --> b\n  class a " + p },
	func(p string) string { return "stateDiagram-v2\n  a --> b\n  class a," + p + " c" },
	func(p string) string {
		return "stateDiagram-v2\n  a --> b\n  classDef " + p + " fill:red\n  class a " + p
	},
	func(p string) string { return "stateDiagram-v2\n  a --> b\n  classDef c fill:" + p + "\n  class a c" },
	func(p string) string {
		return "stateDiagram-v2\n  a --> b\n  classDef c " + p + ":red,color:" + p + ",font-family:" + p + ",stroke:" + p + "\n  class a c"
	},
	func(p string) string {
		return "stateDiagram-v2\n  state g {\n  a --> b\n  }\n  classDef c fill:" + p + ",stroke:" + p + ",color:" + p + "\n  class g c"
	},
	func(p string) string { return "stateDiagram-v2\n  a --> b\n  style a fill:" + p },
	func(p string) string { return "stateDiagram-v2\n  a --> b\n  style a " + p + ":red" },
	func(p string) string {
		return "stateDiagram-v2\n  a --> b\n  style a," + p + " color:" + p + ",stroke:" + p + ",font-size:" + p
	},
	func(p string) string { return "stateDiagram-v2\n  a --> b\n  click a \"" + p + "\" \"" + p + "\"\n" },
	func(p string) string { return "stateDiagram-v2\n  a --> b\n  click a href \"" + p + "\"\n" },
	func(p string) string {
		return "stateDiagram-v2\n  state a {\n  x\n  }\n  click a \"" + p + "\" \"" + p + "\"\n"
	},
	func(p string) string {
		return "stateDiagram-v2\n  " + p + " --> b\n  click " + p + " \"https://example.com\" \"" + p + "\"\n"
	},
	func(p string) string {
		return "stateDiagram-v2\n  accTitle: " + p + "\n  accDescr: " + p + "\n  a --> b"
	},
	func(p string) string { return "stateDiagram-v2\n  accDescr {\n " + p + "\n }\n  a --> b" },
	func(p string) string { return "stateDiagram-v2\n  direction " + p + "\n  a --> b" },
	func(p string) string { return "stateDiagram-v2\n  direction LR " + p + "\n  a --> b" },
	func(p string) string { return "stateDiagram-v2\n  scale " + p + " width\n  a --> b" },
	func(p string) string { return "stateDiagram-v2\n  hide empty description " + p + "\n  a --> b" },
	func(p string) string { return "stateDiagram-v2\n  %% " + p + "\n  a --> b %% " + p },
	func(p string) string { return "stateDiagram-v2\n  a --> b # " + p },
	func(p string) string { return "stateDiagram " + p + "\n  a --> b" },
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\nconfig:\n  state:\n    nodeSpacing: \"" + p + "\"\n    wrappingWidth: \"" + p + "\"\n---\nstateDiagram-v2\n  a --> b"
	},
	func(p string) string {
		return "%%{init: {\"state\": {\"rankSpacing\": \"" + p + "\"}, \"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\nstateDiagram-v2\n  a --> b"
	},
}

var classTemplates = []func(p string) string{
	func(p string) string { return "classDiagram\n  class " + p },
	func(p string) string { return "classDiagram\n  class `" + p + "`" },
	func(p string) string { return "classDiagram\n  class A[\"" + p + "\"]" },
	func(p string) string { return "classDiagram\n  class A~" + p + "~" },
	func(p string) string { return "classDiagram\n  class A:::" + p },
	func(p string) string { return "classDiagram\n  class A <<" + p + ">>" },
	func(p string) string { return "classDiagram\n  <<" + p + ">> A" },
	func(p string) string { return "classDiagram\n  class A {\n    " + p + "\n  }" },
	func(p string) string {
		return "classDiagram\n  class A {\n    +" + p + "()\n    -" + p + " x$\n    #f(" + p + ") " + p + "*\n    <<" + p + ">>\n  }"
	},
	func(p string) string {
		return "classDiagram\n  class A {\n    +List~" + p + "~ x\n    +f(Map~" + p + ", " + p + "~ m) List~" + p + "~\n  }"
	},
	func(p string) string { return "classDiagram\n  A : " + p },
	func(p string) string { return "classDiagram\n  A : +" + p + "()" },
	func(p string) string { return "classDiagram\n  " + p + " <|-- B" },
	func(p string) string { return "classDiagram\n  `" + p + "` <|-- `" + p + "2`" },
	func(p string) string { return "classDiagram\n  A <|-- B : " + p },
	func(p string) string { return "classDiagram\n  A \"" + p + "\" --> \"" + p + "\" B : " + p },
	func(p string) string { return "classDiagram\n  A \"" + p + "\" --> \"" + p + "\" A : " + p },
	func(p string) string { return "classDiagram\n  " + p + " ()-- A" },
	func(p string) string { return "classDiagram\n  A --() `" + p + "`" },
	func(p string) string { return "classDiagram\n  note \"" + p + "\"" },
	func(p string) string { return "classDiagram\n  class A\n  note for A \"" + p + "\"" },
	func(p string) string { return "classDiagram\n  note for `" + p + "` \"x\"" },
	func(p string) string { return "classDiagram\n  namespace " + p + " {\n    class A\n  }" },
	func(p string) string { return "classDiagram\n  namespace `" + p + "` {\n    class A\n  }" },
	func(p string) string {
		return "classDiagram\n  namespace N[\"" + p + "\"] {\n    class A\n    note \"" + p + "\"\n  }"
	},
	func(p string) string { return "classDiagram\n  class A\n  link A \"" + p + "\"" },
	func(p string) string { return "classDiagram\n  class A\n  link A \"" + p + "\" \"" + p + "\" _blank" },
	func(p string) string { return "classDiagram\n  class A\n  link A \"https://example.com\" " + p },
	func(p string) string { return "classDiagram\n  class A\n  click A href \"" + p + "\" \"" + p + "\"" },
	func(p string) string { return "classDiagram\n  class A\n  click A href \"x\" \"" + p + "\" _top" },
	func(p string) string { return "classDiagram\n  class A\n  click A call " + p + "()" },
	func(p string) string { return "classDiagram\n  class A\n  click A call f(" + p + ") \"" + p + "\"" },
	func(p string) string { return "classDiagram\n  class A\n  callback A \"" + p + "\" \"" + p + "\"" },
	func(p string) string { return "classDiagram\n  class A\n  style A fill:" + p },
	func(p string) string { return "classDiagram\n  class A\n  style A " + p + ":red" },
	func(p string) string {
		return "classDiagram\n  class A\n  style A color:" + p + ",stroke:" + p + ",font-family:" + p + ",font-size:" + p
	},
	func(p string) string { return "classDiagram\n  class A\n  style " + p + " fill:red" },
	func(p string) string { return "classDiagram\n  class A:::c\n  classDef c fill:" + p + ",color:" + p },
	func(p string) string {
		return "classDiagram\n  class A {\n +x$\n}\n  classDef default color:" + p + ",stroke:" + p
	},
	func(p string) string {
		return "classDiagram\n  class A\n  classDef " + p + " fill:red\n  cssClass \"A\" " + p
	},
	func(p string) string { return "classDiagram\n  class A\n  cssClass \"" + p + "\" c" },
	func(p string) string { return "classDiagram\n  class A\n  cssClass \"A\" " + p },
	func(p string) string { return "classDiagram\n  accTitle: " + p + "\n  accDescr: " + p + "\n  class A" },
	func(p string) string { return "classDiagram\n  accDescr {\n " + p + "\n }\n  class A" },
	func(p string) string { return "classDiagram\n  direction " + p + "\n  class A" },
	func(p string) string { return "classDiagram\n  %% " + p + "\n  class A" },
	func(p string) string { return "classDiagram " + p + "\n  class A" },
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\nconfig:\n  class:\n    hideEmptyMembersBox: \"" + p + "\"\n    nodeSpacing: \"" + p + "\"\n    hierarchicalNamespaces: \"" + p + "\"\n---\nclassDiagram\n  class A"
	},
	func(p string) string {
		return "%%{init: {\"class\": {\"rankSpacing\": \"" + p + "\"}, \"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\nclassDiagram\n  A --> B"
	},
}

var erTemplates = []func(p string) string{
	func(p string) string { return "erDiagram\n  " + p },
	func(p string) string { return "erDiagram\n  \"" + p + "\"" },
	func(p string) string { return "erDiagram\n  \"" + p + "\" ||--o{ B : has" },
	func(p string) string { return "erDiagram\n  A ||--o{ \"" + p + "\" : has" },
	func(p string) string { return "erDiagram\n  A ||--o{ B : " + p },
	func(p string) string { return "erDiagram\n  A ||--o{ B : \"" + p + "\"" },
	func(p string) string { return "erDiagram\n  A ||--o{ A : \"" + p + "\"" },
	func(p string) string { return "erDiagram\n  A " + p + " B : has" },
	func(p string) string { return "erDiagram\n  A[" + p + "]" },
	func(p string) string { return "erDiagram\n  A[\"" + p + "\"] {\n    string name\n  }" },
	func(p string) string { return "erDiagram\n  A {\n    " + p + " name\n  }" },
	func(p string) string { return "erDiagram\n  A {\n    string " + p + "\n  }" },
	func(p string) string { return "erDiagram\n  A {\n    `" + p + "` `" + p + "`\n  }" },
	func(p string) string { return "erDiagram\n  A {\n    " + p + "~" + p + "~ ~" + p + "~ PK \"c\"\n  }" },
	func(p string) string { return "erDiagram\n  A {\n    string name PK \"" + p + "\"\n  }" },
	func(p string) string { return "erDiagram\n  A {\n    string name " + p + "\n  }" },
	func(p string) string { return "erDiagram\n  A {\n    string name PK, " + p + "\n  }" },
	func(p string) string { return "erDiagram\n  A:::" + p },
	func(p string) string { return "erDiagram\n  A:::" + p + " ||--o{ B:::" + p + " : has" },
	func(p string) string { return "erDiagram\n  A\n  class A " + p },
	func(p string) string { return "erDiagram\n  A\n  class " + p + " x" },
	func(p string) string { return "erDiagram\n  A\n  classDef " + p + " fill:red\n  class A " + p },
	func(p string) string { return "erDiagram\n  A\n  classDef c fill:" + p + "\n  class A c" },
	func(p string) string { return "erDiagram\n  A\n  classDef c " + p + ":red\n  class A c" },
	func(p string) string {
		return "erDiagram\n  A\n  classDef default fill:" + p + ",color:" + p + ",stroke:" + p + "\n"
	},
	func(p string) string { return "erDiagram\n  A\n  style A fill:" + p + "\n" },
	func(p string) string { return "erDiagram\n  A { int id }\n  style A " + p + ":red\n" },
	func(p string) string {
		return "erDiagram\n  A { int id }\n  style A color:" + p + ",stroke:" + p + ",font-family:" + p + ",font-size:" + p + "\n"
	},
	func(p string) string { return "erDiagram\n  A\n  style " + p + " fill:red\n" },
	func(p string) string { return "erDiagram\n  subgraph " + p + "\n  A\n  end" },
	func(p string) string { return "erDiagram\n  subgraph \"" + p + "\"\n  A\n  end" },
	func(p string) string { return "erDiagram\n  subgraph s [" + p + "]\n  A\n  end" },
	func(p string) string {
		return "erDiagram\n  subgraph s [\"" + p + "\"]\n  A\n  end\n  s ||--|| B : \"" + p + "\""
	},
	func(p string) string {
		return "erDiagram\n  subgraph s\n  A\n  end\n  style s fill:" + p + "\n  class s " + p
	},
	func(p string) string { return "erDiagram\n  A[\"" + p + "\"]:::internal-link" },
	func(p string) string { return "erDiagram\n  \"" + p + "\":::internal-link" },
	func(p string) string { return "erDiagram\n  accTitle: " + p + "\n  accDescr: " + p + "\n  A" },
	func(p string) string { return "erDiagram\n  accDescr {\n " + p + "\n }\n  A" },
	func(p string) string { return "erDiagram\n  direction " + p + "\n  A" },
	func(p string) string { return "erDiagram\n  direction TB " + p + "\n  A" },
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\nconfig:\n  er:\n    nodeSpacing: \"" + p + "\"\n    rankSpacing: \"" + p + "\"\n---\nerDiagram\n  A ||--|| B : x"
	},
	func(p string) string { return "---\ntitle: " + p + "\n---\nerDiagram\n  A" },
	func(p string) string {
		return "%%{init: {\"er\": {\"layoutDirection\": \"" + p + "\"}, \"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\nerDiagram\n  A"
	},
	func(p string) string { return "erDiagram " + p + "\n  A" },
	func(p string) string { return "erDiagram\n  %% " + p + "\n  A" },
}

var ganttTemplates = []func(p string) string{
	func(p string) string { return ganttHead + "  title " + p + "\n" + ganttTasks },
	func(p string) string { return ganttHead + "  section " + p + "\n  A task :a1, 2014-01-01, 30d\n" },
	func(p string) string { return ganttHead + "  " + p + " :a1, 2014-01-01, 30d\n" },
	func(p string) string { return ganttHead + "  " + p + " :a1, 2014-01-01, 1h\n  B :b, 2014-01-01, 30d\n" },
	func(p string) string { return ganttHead + "  A task :" + p + ", 2014-01-01, 30d\n" },
	func(p string) string { return ganttHead + "  A task :a1, " + p + ", 30d\n" },
	func(p string) string { return ganttHead + "  A task :a1, 2014-01-01, " + p + "\n" },
	func(p string) string { return ganttHead + "  A task :" + p + "\n" },
	func(p string) string {
		return ganttHead + "  A task :a1, after " + p + ", 3d\n  B :b, 2014-01-01, until " + p + "\n"
	},
	func(p string) string {
		return ganttHead + "  " + p + " :milestone, m1, 2014-01-01, 0d\n  B :b, 2014-01-01, 30d\n"
	},
	func(p string) string {
		return ganttHead + "  " + p + " :vert, v1, 2014-01-05, 0d\n  B :b, 2014-01-01, 30d\n"
	},
	func(p string) string { return ganttHead + "  A :" + p + ", a1, 2014-01-01, 30d\n" },
	func(p string) string { return "gantt\n  dateFormat " + p + "\n" + ganttTasks },
	func(p string) string {
		return "gantt\n  dateFormat [" + p + "]YYYY-MM-DD\n  A :a, " + p + "2014-01-01, 3d\n"
	},
	func(p string) string { return ganttHead + "  axisFormat " + p + "\n" + ganttTasks },
	func(p string) string { return ganttHead + "  axisFormat %Y " + p + " %c %%\n" + ganttTasks },
	func(p string) string { return ganttHead + "  tickInterval " + p + "\n" + ganttTasks },
	func(p string) string { return ganttHead + "  excludes " + p + "\n" + ganttTasks },
	func(p string) string { return ganttHead + "  excludes weekends\n  includes " + p + "\n" + ganttTasks },
	func(p string) string { return ganttHead + "  todayMarker " + p + "\n" + ganttTasks },
	func(p string) string {
		return ganttHead + "  todayMarker stroke:" + p + ",stroke-width:" + p + ",opacity:" + p + "\n" + ganttTasks
	},
	func(p string) string { return ganttHead + "  todayMarker " + p + ":red\n" + ganttTasks },
	func(p string) string { return ganttHead + "  weekday " + p + "\n" + ganttTasks },
	func(p string) string { return ganttHead + "  weekend " + p + "\n" + ganttTasks },
	func(p string) string { return ganttHead + ganttTasks + "  click a1 href \"" + p + "\"\n" },
	func(p string) string { return ganttHead + ganttTasks + "  click a1 call " + p + "()\n" },
	func(p string) string { return ganttHead + ganttTasks + "  click a1 call f(" + p + ")\n" },
	func(p string) string {
		return ganttHead + ganttTasks + "  click a1 call f(\"" + p + "\") href \"" + p + "\"\n"
	},
	func(p string) string {
		return ganttHead + ganttTasks + "  click " + p + " href \"https://example.com\"\n"
	},
	func(p string) string {
		return ganttHead + "  A :" + p + ", 2014-01-01, 3d\n  click " + p + " href \"https://example.com\"\n"
	},
	func(p string) string {
		return ganttHead + "  accTitle: " + p + "\n  accDescr: " + p + "\n" + ganttTasks
	},
	func(p string) string { return ganttHead + "  accDescr {\n " + p + "\n }\n" + ganttTasks },
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\ndisplayMode: \"" + p + "\"\n---\n" + ganttHead + ganttTasks
	},
	func(p string) string {
		return "---\nconfig:\n  gantt:\n    axisFormat: \"" + p + "\"\n    tickInterval: \"" + p + "\"\n    weekday: \"" + p + "\"\n    displayMode: \"" + p + "\"\n---\n" + ganttHead + ganttTasks
	},
	func(p string) string {
		return "---\nconfig:\n  gantt:\n    barHeight: \"" + p + "\"\n    barGap: \"" + p + "\"\n    fontSize: \"" + p + "\"\n    sectionFontSize: \"" + p + "\"\n    leftPadding: \"" + p + "\"\n    useWidth: \"" + p + "\"\n    numberSectionStyles: \"" + p + "\"\n---\n" + ganttHead + ganttTasks
	},
	func(p string) string {
		return "%%{init: {\"gantt\": {\"axisFormat\": \"" + p + "\", \"topAxis\": \"" + p + "\"}, \"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\n" + ganttHead + ganttTasks
	},
	func(p string) string { return "gantt " + p + "\n" + ganttTasks },
	func(p string) string { return ganttHead + "  %% " + p + "\n" + ganttTasks },
}

var mindmapTemplates = []func(p string) string{
	func(p string) string { return "mindmap\n  " + p + "\n    child" },
	func(p string) string { return "mindmap\n  root\n    " + p },
	func(p string) string { return "mindmap\n  root[" + p + "]" },
	func(p string) string { return "mindmap\n  root\n    id(" + p + ")" },
	func(p string) string { return "mindmap\n  root\n    id((" + p + "))" },
	func(p string) string { return "mindmap\n  root\n    id))" + p + "((" },
	func(p string) string { return "mindmap\n  root\n    id)" + p + "(" },
	func(p string) string { return "mindmap\n  root\n    id{{" + p + "}}" },
	func(p string) string { return "mindmap\n  root\n    " + p + "[text]" },
	func(p string) string { return "mindmap\n  root\n    " + p + "((text))" },
	func(p string) string { return "mindmap\n  root[\"" + p + "\"]\n    [\"" + p + "\"]" },
	func(p string) string {
		return "mindmap\n  root[\"`" + p + "`\"]\n    a[\"`**" + p + "**\n" + p + "`\"]"
	},
	func(p string) string { return "mindmap\n  root\n    a\n    ::icon(" + p + ")" },
	func(p string) string { return "mindmap\n  root\n  ::icon(" + p + ")\n    a(b)\n    ::icon(" + p + ")" },
	func(p string) string { return "mindmap\n  root\n    a\n    :::" + p },
	func(p string) string { return "mindmap\n  root\n  :::" + p + "\n    a[b]\n    :::" + p + " " + p },
	func(p string) string { return "mindmap\n  root\n    a %% " + p + "\n    b(c) %% " + p },
	func(p string) string { return "mindmap\n  root\n    fa:fa-" + p + " x" },
	func(p string) string { return "mindmap " + p + "\n  root" },
	func(p string) string { return "---\ntitle: \"" + p + "\"\n---\nmindmap\n  root\n    a" },
	func(p string) string {
		return "---\nconfig:\n  mindmap:\n    maxNodeWidth: \"" + p + "\"\n    padding: \"" + p + "\"\n---\nmindmap\n  root\n    a"
	},
	func(p string) string {
		return "%%{init: {\"mindmap\": {\"maxNodeWidth\": \"" + p + "\"}, \"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\nmindmap\n  root"
	},
}
