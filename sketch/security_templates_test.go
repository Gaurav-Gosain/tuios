// Code generated from pele tests/render/*-security.test.ts at 8fad23bd. DO NOT EDIT.

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

var xychartTemplates = []func(p string) string{
	func(p string) string { return "xychart\n  title \"" + p + "\"\n  line [1, 2]" },
	func(p string) string { return "xychart\n  title " + p + "\n  line [1, 2]" },
	func(p string) string { return "xychart\n  x-axis \"" + p + "\" [\"" + p + "\", \"b\"]\n  bar [1, 2]" },
	func(p string) string { return "xychart\n  x-axis " + p + " [" + p + ", b]\n  bar [1, 2]" },
	func(p string) string { return "xychart\n  x-axis \"" + p + "\" 1 --> 5\n  line [1, 2]" },
	func(p string) string { return "xychart\n  x-axis " + p + " --> " + p + "\n  line [1, 2]" },
	func(p string) string { return "xychart\n  y-axis \"" + p + "\"\n  line [1, 2]" },
	func(p string) string { return "xychart\n  y-axis \"" + p + "\" 0 --> 10\n  bar [1, 2]" },
	func(p string) string { return "xychart\n  y-axis " + p + " --> " + p + "\n  bar [1, 2]" },
	func(p string) string { return "xychart\n  line \"" + p + "\" [1, 2]\n  bar \"" + p + "\" [2, 1]" },
	func(p string) string { return "xychart\n  line " + p + " [1, 2]" },
	func(p string) string { return "xychart\n  line [1 \"" + p + "\", 2 \"" + p + "\"]" },
	func(p string) string {
		return "xychart horizontal\n  x-axis [\"" + p + "\", \"b\"]\n  line \"" + p + "\" [1 \"" + p + "\", 2]\n  bar [3, 4]"
	},
	func(p string) string { return "xychart\n  line [" + p + ", 2]" },
	func(p string) string { return "xychart\n  bar [1, " + p + "]" },
	func(p string) string { return "xychart " + p + "\n  line [1, 2]" },
	func(p string) string { return "xychart\n  accTitle: " + p + "\n  accDescr: " + p + "\n  line [1, 2]" },
	func(p string) string { return "xychart\n  accDescr {\n " + p + "\n }\n  line [1, 2]" },
	func(p string) string { return "---\ntitle: \"" + p + "\"\n---\nxychart\n  line [1, 2]" },
	func(p string) string {
		return "---\nconfig:\n  xyChart:\n    width: \"" + p + "\"\n    height: \"" + p + "\"\n    chartOrientation: \"" + p + "\"\n    showDataLabel: \"" + p + "\"\n    showTitle: \"" + p + "\"\n    xAxis:\n      showLabel: \"" + p + "\"\n      showTick: \"" + p + "\"\n    yAxis: \"" + p + "\"\n---\nxychart\n  title t\n  x-axis x [a, b]\n  y-axis y\n  bar \"s\" [1, 2]"
	},
	func(p string) string { return "---\nconfig:\n  xyChart: \"" + p + "\"\n---\nxychart\n  line [1, 2]" },
	func(p string) string {
		return "---\nconfig:\n  xyChart:\n    xAxis: [\"" + p + "\"]\n    yAxis:\n      \"" + p + "\": true\n---\nxychart\n  line [1, 2]"
	},
	func(p string) string {
		return "%%{init: {\"xyChart\": {\"width\": \"" + p + "\", \"showDataLabel\": true}, \"themeVariables\": {\"xyChart\": {\"plotColorPalette\": \"" + p + "\"}}}}%%\nxychart\n  bar [1, 2]"
	},
	func(p string) string { return "xychart\n  %% " + p + "\n  line [1, 2]" },
}

var timelineTemplates = []func(p string) string{
	func(p string) string { return "timeline\n  title " + p + "\n  2001 : a" },
	func(p string) string { return "timeline\n  " + p },
	func(p string) string { return "timeline\n  " + p + " : event" },
	func(p string) string { return "timeline\n  2001 : " + p },
	func(p string) string { return "timeline\n  2001 : a : " + p + " : b" },
	func(p string) string { return "timeline\n  2001 : a\n       : " + p },
	func(p string) string { return "timeline\n  section " + p + "\n  2001 : a" },
	func(p string) string { return "timeline\n  section " + p },
	func(p string) string {
		return "timeline TD\n  title " + p + "\n  section " + p + "\n  " + p + " : " + p
	},
	func(p string) string { return "timeline TD\n  x" + p + " : y" + p + " : z" + p },
	func(p string) string { return "timeline " + p + "\n  2001 : a" },
	func(p string) string { return "timeline\n  accTitle: " + p + "\n  accDescr: " + p + "\n  2001 : a" },
	func(p string) string { return "timeline\n  accDescr {\n " + p + "\n }\n  2001 : a" },
	func(p string) string { return "timeline\n  2001 : a<br>" + p + "<br/>b" },
	func(p string) string { return "timeline\n  2001 : <b>" + p + "</b> <i>" + p },
	func(p string) string { return "timeline\n  2001 : fa:fa-" + p + " x" },
	func(p string) string { return "timeline\n  %% " + p + "\n  2001 : a # " + p },
	func(p string) string { return "---\ntitle: \"" + p + "\"\n---\ntimeline\n  2001 : a" },
	func(p string) string {
		return "---\nconfig:\n  timeline:\n    disableMulticolor: \"" + p + "\"\n---\ntimeline\n  2001 : a\n  2002 : b"
	},
	func(p string) string { return "---\nconfig:\n  timeline: \"" + p + "\"\n---\ntimeline\n  2001 : a" },
	func(p string) string {
		return "%%{init: {\"timeline\": {\"disableMulticolor\": \"" + p + "\", \"padding\": \"" + p + "\"}, \"themeVariables\": {\"cScale0\": \"" + p + "\"}}}%%\ntimeline\n  2001 : a"
	},
}

var quadrantTemplates = []func(p string) string{
	func(p string) string { return "quadrantChart\n  title " + p + "\n  A: [0.5, 0.5]" },
	func(p string) string { return "quadrantChart\n  x-axis " + p + " --> " + p },
	func(p string) string {
		return "quadrantChart\n  x-axis \"" + p + "\" --> \"" + p + "\"\n  A: [0.5, 0.5]"
	},
	func(p string) string { return "quadrantChart\n  y-axis \"" + p + "\" --> \"" + p + "\"" },
	func(p string) string { return "quadrantChart\n  y-axis " + p + "\n  A: [0.5, 0.5]" },
	func(p string) string { return "quadrantChart\n  x-axis \"" + p + "\" -->" },
	func(p string) string { return "quadrantChart\n  quadrant-1 " + p + "\n  quadrant-2 \"" + p + "\"" },
	func(p string) string {
		return "quadrantChart\n  quadrant-3 \"`" + p + "`\"\n  quadrant-4 \"`**" + p + "**`\"\n  A: [0.1, 0.9]"
	},
	func(p string) string { return "quadrantChart\n  " + p + ": [0.5, 0.5]" },
	func(p string) string { return "quadrantChart\n  \"" + p + "\": [0.5, 0.5]" },
	func(p string) string { return "quadrantChart\n  \"`" + p + "`\": [0.5, 0.5]" },
	func(p string) string { return "quadrantChart\n  A:::" + p + ": [0.5, 0.5]" },
	func(p string) string { return "quadrantChart\n  A:::k: [0.5, 0.5]\n  classDef " + p + " color: #f00" },
	func(p string) string { return "quadrantChart\n  A:::k: [0.5, 0.5]\n  classDef k color: " + p },
	func(p string) string { return "quadrantChart\n  A:::k: [0.5, 0.5]\n  classDef k " + p + ": 3" },
	func(p string) string { return "quadrantChart\n  A: [0.5, 0.5] color: " + p },
	func(p string) string {
		return "quadrantChart\n  A: [0.5, 0.5] radius: " + p + ", stroke-width: " + p + ", stroke-color: " + p
	},
	func(p string) string { return "quadrantChart\n  A: [0.5, 0.5] " + p },
	func(p string) string { return "quadrantChart\n  A: [" + p + ", 0.5]" },
	func(p string) string { return "quadrantChart\n  A: [0.5, " + p + "]" },
	func(p string) string {
		return "quadrantChart\n  accTitle: " + p + "\n  accDescr: " + p + "\n  A: [0.5, 0.5]"
	},
	func(p string) string { return "quadrantChart\n  accDescr {\n " + p + "\n }\n  A: [0.5, 0.5]" },
	func(p string) string { return "---\ntitle: \"" + p + "\"\n---\nquadrantChart\n  A: [0.5, 0.5]" },
	func(p string) string {
		return "---\nconfig:\n  quadrantChart:\n    chartWidth: \"" + p + "\"\n    chartHeight: \"" + p + "\"\n    pointRadius: \"" + p + "\"\n    quadrantPadding: \"" + p + "\"\n    xAxisPosition: \"" + p + "\"\n    yAxisPosition: \"" + p + "\"\n---\nquadrantChart\n  x-axis a --> b\n  y-axis c --> d\n  A: [0.5, 0.5]"
	},
	func(p string) string {
		return "---\nconfig:\n  quadrantChart: \"" + p + "\"\n---\nquadrantChart\n  A: [0.5, 0.5]"
	},
	func(p string) string {
		return "%%{init: {\"quadrantChart\": {\"chartWidth\": \"" + p + "\", \"titlePadding\": \"" + p + "\"}, \"themeVariables\": {\"quadrant1Fill\": \"" + p + "\"}}}%%\nquadrantChart\n  title t\n  A: [0.5, 0.5]"
	},
	func(p string) string { return "quadrantChart " + p + "\n  A: [0.5, 0.5]" },
	func(p string) string { return "quadrantChart\n  %% " + p + "\n  A: [0.5, 0.5]" },
}

var journeyTemplates = []func(p string) string{
	func(p string) string { return "journey\n  title " + p + "\n  Task: 5: Me" },
	func(p string) string { return "journey\n  " + p + ": 5: Me" },
	func(p string) string { return "journey\n  " + p },
	func(p string) string { return "journey\n  Task: " + p },
	func(p string) string { return "journey\n  Task: " + p + ": Me" },
	func(p string) string { return "journey\n  Task: 5: " + p },
	func(p string) string { return "journey\n  Task: 5: Me, " + p + ", You" },
	func(p string) string { return "journey\n  Task: 5: Me: " + p },
	func(p string) string { return "journey\n  section " + p + "\n  Task: 5: Me" },
	func(p string) string { return "journey\n  section " + p },
	func(p string) string {
		return "journey\n  title " + p + "\n  section " + p + "\n  x" + p + ": 3: y" + p + ", z" + p
	},
	func(p string) string { return "journey " + p + "\n  Task: 5: Me" },
	func(p string) string { return "journey\n  accTitle: " + p + "\n  accDescr: " + p + "\n  Task: 5: Me" },
	func(p string) string { return "journey\n  accDescr {\n " + p + "\n }\n  Task: 5: Me" },
	func(p string) string { return "journey\n  a<br>" + p + "<br/>b: 5: Me<br>" + p },
	func(p string) string { return "journey\n  fa:fa-" + p + " x: 5: fa:fa-" + p },
	func(p string) string { return "journey\n  %% " + p + "\n  Task: 5: Me # " + p },
	func(p string) string { return "---\ntitle: \"" + p + "\"\n---\njourney\n  Task: 5: Me" },
	func(p string) string {
		return "---\nconfig:\n  journey:\n    actorColours: [\"" + p + "\"]\n    sectionFills: [\"" + p + "\"]\n    taskFontFamily: \"" + p + "\"\n---\njourney\n  section S\n  Task: 5: Me"
	},
	func(p string) string { return "---\nconfig:\n  journey: \"" + p + "\"\n---\njourney\n  Task: 5: Me" },
	func(p string) string {
		return "%%{init: {\"journey\": {\"width\": \"" + p + "\", \"titleColor\": \"" + p + "\"}, \"themeVariables\": {\"fillType0\": \"" + p + "\"}}}%%\njourney\n  Task: 5: Me"
	},
}

var kanbanTemplates = []func(p string) string{
	func(p string) string { return "kanban\n  " + p + "\n    card" },
	func(p string) string { return "kanban\n  todo\n    " + p },
	func(p string) string { return "kanban\n  todo[" + p + "]\n    a[" + p + "]" },
	func(p string) string { return "kanban\n  " + p + "[Todo]\n    " + p + "[Card]" },
	func(p string) string {
		return "kanban\n  todo(" + p + ")\n    a((" + p + "))\n    b{{" + p + "}}\n    c))" + p + "(("
	},
	func(p string) string { return "kanban\n  todo[\"" + p + "\"]\n    a[\"" + p + "\"]" },
	func(p string) string { return "kanban\n  todo[\"`" + p + "`\"]\n    a[\"`**" + p + "**\n" + p + "`\"]" },
	func(p string) string { return "kanban\n  todo\n    a@{ label: \"" + p + "\" }" },
	func(p string) string { return "kanban\n  todo\n    a@{ label: '" + p + "' }" },
	func(p string) string { return "kanban\n  todo\n    a@{ assigned: \"" + p + "\" }" },
	func(p string) string { return "kanban\n  todo\n    a@{ ticket: \"" + p + "\" }" },
	func(p string) string { return "kanban\n  todo\n    a@{ priority: \"" + p + "\" }" },
	func(p string) string { return "kanban\n  todo\n    a@{ icon: \"" + p + "\" }" },
	func(p string) string { return "kanban\n  todo\n    a@{ shape: \"" + p + "\" }" },
	func(p string) string { return "kanban\n  todo\n    a@{ " + p + ": 1 }" },
	func(p string) string { return "kanban\n  todo\n    a@{ " + p + " }" },
	func(p string) string {
		return "kanban\n  todo\n    a@{\n      assigned: " + p + "\n      ticket: " + p + "\n      priority: " + p + "\n    }"
	},
	func(p string) string {
		return "kanban\n  todo@{ label: \"" + p + "\", ticket: \"" + p + "\", assigned: \"" + p + "\", priority: \"" + p + "\", icon: \"" + p + "\" }\n    a"
	},
	func(p string) string { return kanbanBase + "kanban\n  todo\n    a@{ ticket: \"" + p + "\" }" },
	func(p string) string {
		return "---\nconfig:\n  kanban:\n    ticketBaseUrl: \"" + p + "\"\n---\nkanban\n  todo\n    a@{ ticket: T-1 }"
	},
	func(p string) string {
		return "---\nconfig:\n  kanban:\n    ticketBaseUrl: \"" + p + "#TICKET#\"\n---\nkanban\n  todo\n    a@{ ticket: \"" + p + "\" }"
	},
	func(p string) string {
		return "---\nconfig:\n  kanban:\n    ticketBaseUrl: \"#TICKET#\"\n---\nkanban\n  todo\n    a@{ ticket: \"" + p + "\" }"
	},
	func(p string) string {
		return "%%{init: {\"kanban\": {\"ticketBaseUrl\": \"" + p + "\", \"sectionWidth\": \"" + p + "\"}}}%%\nkanban\n  todo\n    a@{ ticket: T-1 }"
	},
	func(p string) string { return "kanban\n  todo\n  ::icon(" + p + ")\n    a\n    ::icon(" + p + ")" },
	func(p string) string { return "kanban\n  todo\n  :::" + p + "\n    a\n    :::" + p + " " + p },
	func(p string) string { return "kanban\n  todo\n    a %% " + p + "\n    b[c] %% " + p },
	func(p string) string { return "kanban\n  todo\n    fa:fa-" + p + " x" },
	func(p string) string { return "kanban " + p + "\n  todo" },
	func(p string) string { return "---\ntitle: \"" + p + "\"\n---\nkanban\n  todo\n    a" },
}

var gitTemplates = []func(q, p string) string{
	func(q, p string) string { return "gitGraph\n commit id: " + q },
	func(q, p string) string {
		return "gitGraph\n commit id: " + q + " msg: " + q + " tag: " + q + " tag: " + q + " type: HIGHLIGHT"
	},
	func(q, p string) string { return "gitGraph\n commit " + q },
	func(q, p string) string {
		return "gitGraph\n commit\n branch " + q + "\n commit\n checkout " + q + "\n commit"
	},
	func(q, p string) string {
		return "gitGraph\n commit\n branch " + q + " order: 2\n commit\n switch main\n commit\n merge " + q + " id: " + q + " tag: " + q + " type: REVERSE"
	},
	func(q, p string) string {
		return "gitGraph\n commit id: " + q + "\n branch b\n commit\n cherry-pick id: " + q
	},
	func(q, p string) string {
		return "gitGraph\n commit id: \"a\"\n branch b\n commit id: \"c\"\n checkout main\n commit\n cherry-pick id: \"c\" tag: " + q
	},
	func(q, p string) string {
		return "gitGraph\n commit id:\"z\"\n branch b\n commit id:\"a\"\n checkout main\n commit\n merge b id:\"m\"\n checkout b\n cherry-pick id:\"m\" parent:\"a\" tag: " + q
	},
	func(q, p string) string {
		return "gitGraph TB:\n commit id: " + q + " tag: " + q + "\n branch " + q + "\n commit"
	},
	func(q, p string) string {
		return "gitGraph BT:\n commit id: " + q + " tag: " + q + "\n branch " + q + "\n commit"
	},
	func(q, p string) string { return "gitGraph\n commit\n branch " + p + "\n commit" },
	func(q, p string) string { return "gitGraph\n title " + p + "\n commit" },
	func(q, p string) string { return "gitGraph\n accTitle: " + p + "\n accDescr: " + p + "\n commit" },
	func(q, p string) string { return "gitGraph\n accDescr {\n " + p + "\n }\n commit" },
	func(q, p string) string { return "gitGraph " + p + ":\n commit" },
	func(q, p string) string { return "gitGraph\n commit type: " + p },
	func(q, p string) string { return "gitGraph\n commit\n branch b order: " + p },
	func(q, p string) string {
		return "---\ntitle: " + q + "\nconfig:\n  gitGraph:\n    mainBranchName: " + q + "\n    mainBranchOrder: " + q + "\n    showBranches: " + q + "\n    rotateCommitLabel: " + q + "\n---\ngitGraph\n commit id: \"" + gitStrip(p) + "\""
	},
	func(q, p string) string {
		return "%%{init: {\"gitGraph\": {\"mainBranchName\": " + q + ", \"showCommitLabel\": " + q + ", \"parallelCommits\": " + q + "}}}%%\ngitGraph\n commit\n branch b\n commit"
	},
}

var sankeyTemplates = []func(p string) string{
	func(p string) string { return "sankey\n" + p + ",b,1\n" },
	func(p string) string { return "sankey\na," + p + ",1\n" },
	func(p string) string { return "sankey\na,b," + p + "\n" },
	func(p string) string { return "sankey\n" + sankeyQuoted(p) + ",b,1\n" + sankeyBody },
	func(p string) string { return "sankey\na," + sankeyQuoted(p) + ",1\n" + sankeyQuoted(p) + ",c,2\n" },
	func(p string) string { return "sankey\na,b," + sankeyQuoted(p) + "\n" },
	func(p string) string { return "sankey-beta\n\"" + p + "\",\"" + p + "2\",1\n" },
	func(p string) string { return "sankey " + p + "\n" + sankeyBody },
	func(p string) string { return "sankey\n%% " + p + "\n" + sankeyBody },
	func(p string) string { return "---\ntitle: " + jsonText(p) + "\n---\nsankey\n" + sankeyBody },
	func(p string) string {
		return "---\nconfig:\n  sankey:\n    prefix: " + jsonText(p) + "\n    suffix: " + jsonText(p) + "\n---\nsankey\n" + sankeyBody
	},
	func(p string) string {
		return "---\nconfig:\n  sankey:\n    linkColor: " + jsonText(p) + "\n---\nsankey\n" + sankeyBody
	},
	func(p string) string {
		return "---\nconfig:\n  sankey:\n    nodeColors:\n      a: " + jsonText(p) + "\n      " + jsonText(p) + ": red\n---\nsankey\n" + sankeyBody
	},
	func(p string) string {
		return "---\nconfig:\n  sankey:\n    nodeAlignment: " + jsonText(p) + "\n    labelStyle: " + jsonText(p) + "\n    showValues: " + jsonText(p) + "\n---\nsankey\n" + sankeyBody
	},
	func(p string) string {
		return "---\nconfig:\n  sankey:\n    width: " + jsonText(p) + "\n    height: " + jsonText(p) + "\n    nodeWidth: " + jsonText(p) + "\n    nodePadding: " + jsonText(p) + "\n---\nsankey\n" + sankeyBody
	},
	func(p string) string {
		return "---\nconfig:\n  sankey: " + jsonText(p) + "\n---\nsankey\n" + sankeyBody
	},
	func(p string) string {
		return "%%{init: {\"sankey\": {\"prefix\": " + jsonText(p) + ", \"linkColor\": " + jsonText(p) + ", \"nodeColors\": {\"a\": " + jsonText(p) + "}}, \"themeVariables\": {\"fontFamily\": " + jsonText(p) + "}}}%%\nsankey\n" + sankeyBody
	},
}

var blockTemplates = []func(p string) string{
	func(p string) string { return "block\n  " + p },
	func(p string) string { return "block\n  a " + p + " b" },
	func(p string) string { return "block\n  a[\"" + p + "\"]" },
	func(p string) string {
		return "block\n  a(\"" + p + "\") b([\"" + p + "\"]) c[[\"" + p + "\"]] d[(\"" + p + "\")]"
	},
	func(p string) string {
		return "block\n  a((\"" + p + "\")) b(((\"" + p + "\"))) c>\"" + p + "\"] d{\"" + p + "\"} e{{\"" + p + "\"}}"
	},
	func(p string) string {
		return "block\n  a[/\"" + p + "\"/] b[\\\"" + p + "\"\\] c[/\"" + p + "\"\\] d[\\\"" + p + "\"/]"
	},
	func(p string) string { return "block\n  a<[\"" + p + "\"]>(right) b<[\"" + p + "\"]>(x, y)" },
	func(p string) string { return "block\n  a<[\"x\"]>(" + p + ")" },
	func(p string) string { return "block\n  a[\"`" + p + "`\"]" },
	func(p string) string { return "block\n  block:g[\"" + p + "\"]\n    a\n  end" },
	func(p string) string { return "block\n  block:" + p + "\n    a\n  end" },
	func(p string) string { return "block\n  block:g:" + p + "\n    a\n  end" },
	func(p string) string { return "block\n  a space b\n  a -- \"" + p + "\" --> b" },
	func(p string) string {
		return "block\n  a space b\n  a == \"" + p + "\" ==> b\n  b -. \"" + p + "\" .-> a\n  a -- \"" + p + "\" --> a"
	},
	func(p string) string { return "block\n  a space b\n  a " + p + " b" },
	func(p string) string { return "block\n  a:" + p + " b" },
	func(p string) string { return "block\n  a space:" + p + " b" },
	func(p string) string { return "block\n  columns " + p + "\n  a b" },
	func(p string) string { return "block\n  a b\n  class a " + p },
	func(p string) string { return "block\n  a b\n  class " + p + " hot" },
	func(p string) string { return "block\n  a b\n  classDef " + p + " fill:red\n  class a " + p },
	func(p string) string { return "block\n  a b\n  classDef hot fill:" + p + "\n  class a hot" },
	func(p string) string { return "block\n  a b\n  classDef hot " + p + "\n  class a,b hot" },
	func(p string) string {
		return "block\n  a b\n  classDef hot color:" + p + ",stroke:" + p + ",font-family:" + p + ",font-size:" + p + "\n  class a hot"
	},
	func(p string) string { return "block\n  a b\n  classDef default " + p + ":red,fill:" + p },
	func(p string) string { return "block\n  a b\n  style a fill:" + p },
	func(p string) string { return "block\n  a b\n  style a " + p },
	func(p string) string { return "block\n  a b\n  style a " + p + ":red;" + p },
	func(p string) string {
		return "block\n  a b\n  style a color:" + p + ",stroke:" + p + ",stroke-dasharray:" + p
	},
	func(p string) string { return "block\n  a b\n  style " + p + " fill:red" },
	func(p string) string {
		return "block\n  block:g\n    a\n  end\n  style g fill:" + p + ",color:" + p + "\n  class g " + p
	},
	func(p string) string { return "block\n  a[\"fa:fa-" + p + " x\"]" },
	func(p string) string { return "block\n  a\n  accDescr {\n " + p + "\n }" },
	func(p string) string { return "block\n  accTitle: " + p + "\n  a" },
	func(p string) string { return "block\n  %% " + p + "\n  a" },
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\nconfig:\n  block:\n    padding: \"" + p + "\"\n---\nblock\n  a b"
	},
	func(p string) string {
		return "%%{init: {\"block\": {\"padding\": \"" + p + "\"}, \"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\nblock\n  a b"
	},
	func(p string) string { return "block " + p + "\n  a" },
	func(p string) string { return "block-beta\n  " + p + "[\"" + p + "\"] --> " + p },
}

var architectureTemplates = []func(p string) string{
	func(p string) string { return archHead + "service a(server)[" + p + "]" },
	func(p string) string { return archHead + "service a(server)[\"" + p + "\"]" },
	func(p string) string { return archHead + "service a(server)['" + p + "']" },
	func(p string) string { return archHead + "service a \"" + p + "\"[A]" },
	func(p string) string { return archHead + "service a '" + p + "'" },
	func(p string) string { return archHead + "service a(" + p + ")[A]" },
	func(p string) string { return archHead + "service " + p },
	func(p string) string { return archHead + "group g(cloud)[" + p + "]\nservice a in g" },
	func(p string) string { return archHead + "group g(cloud)[\"" + p + "\"]\nservice a[" + p + "] in g" },
	func(p string) string { return archHead + "group g(" + p + ")[G]" },
	func(p string) string { return archHead + "group " + p },
	func(p string) string { return archHead + "group g\nservice a in " + p },
	func(p string) string { return archHead + "junction " + p },
	func(p string) string { return archHead + "service a\nservice b\na:R -[" + p + "]-> L:b" },
	func(p string) string { return archHead + "service a\nservice b\na:R -[\"" + p + "\"]- L:b" },
	func(p string) string { return archHead + "service a\nservice b\na:B <-['" + p + "']-> T:b" },
	func(p string) string { return archHead + "service a\nservice b\na:" + p + " -- L:b" },
	func(p string) string { return archHead + "service a\nservice b\n" + p + ":R -- L:b" },
	func(p string) string { return archHead + "service a\nservice b\na" + p + ":R -- L:b{group}" },
	func(p string) string { return archHead + "service a\nservice b\nalign row a " + p },
	func(p string) string { return archHead + "service a\nservice b\nalign " + p + " a b" },
	func(p string) string { return archHead + "title " + p + "\nservice a" },
	func(p string) string { return archHead + "accTitle: " + p + "\naccDescr: " + p + "\nservice a" },
	func(p string) string { return archHead + "accDescr {\n " + p + "\n }\nservice a" },
	func(p string) string { return "architecture-beta " + p + "\nservice a" },
	func(p string) string { return archHead + "%% " + p + "\nservice a" },
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\nconfig:\n  architecture:\n    iconSize: \"" + p + "\"\n    fontSize: \"" + p + "\"\n    padding: \"" + p + "\"\n---\n" + archHead + "group g[G]\nservice a[A] in g"
	},
	func(p string) string {
		return "%%{init: {\"architecture\": {\"iconSize\": \"" + p + "\"}, \"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\n" + archHead + "service a[A]"
	},
	func(p string) string {
		return "---\nconfig:\n  architecture: \"" + p + "\"\n---\n" + archHead + "service a[A]"
	},
}

var requirementTemplates = []func(p string) string{
	func(p string) string { return "requirementDiagram\nrequirement " + p + " {\nid: 1\n}\n" },
	func(p string) string { return "requirementDiagram\nrequirement \"" + p + "\" {\nid: 1\n}\n" },
	func(p string) string { return "requirementDiagram\nelement " + p + " {\ntype: t\n}\n" },
	func(p string) string {
		return "requirementDiagram\nelement \"" + p + "\" {\ntype: t\n}\n\"" + p + "\" - contains -> \"" + p + "\"\n"
	},
	func(p string) string {
		return "requirementDiagram\nrequirement a {\nid: " + p + "\ntext: " + p + "\n}\n"
	},
	func(p string) string {
		return "requirementDiagram\nrequirement a {\nid: \"" + p + "\"\ntext: \"" + p + "\"\n}\n"
	},
	func(p string) string {
		return "requirementDiagram\nrequirement a {\nrisk: " + p + "\nverifymethod: " + p + "\n}\n"
	},
	func(p string) string {
		return "requirementDiagram\nelement b {\ntype: " + p + "\ndocref: " + p + "\n}\n"
	},
	func(p string) string {
		return "requirementDiagram\nelement b {\ntype: \"" + p + "\"\ndocRef: \"" + p + "\"\n}\n"
	},
	func(p string) string { return "requirementDiagram\n" + requirementBody + "a - " + p + " -> b\n" },
	func(p string) string { return "requirementDiagram\n" + requirementBody + "a - contains -> " + p + "\n" },
	func(p string) string {
		return "requirementDiagram\n" + requirementBody + "\"" + p + "\" <- traces - a\n"
	},
	func(p string) string { return "requirementDiagram\nrequirement a:::" + p + " {\nid: 1\n}\n" },
	func(p string) string {
		return "requirementDiagram\nrequirement a:::\"" + p + "\" {\nid: 1\n}\nelement b:::\"" + p + "\",\"" + p + "\" {\n}\n"
	},
	func(p string) string { return "requirementDiagram\n" + requirementBody + "a:::" + p + "\n" },
	func(p string) string { return "requirementDiagram\n" + requirementBody + "a:::\"" + p + "\"\n" },
	func(p string) string { return "requirementDiagram\n" + requirementBody + "class a " + p + "\n" },
	func(p string) string { return "requirementDiagram\n" + requirementBody + "class a,b \"" + p + "\"\n" },
	func(p string) string { return "requirementDiagram\n" + requirementBody + "class \"" + p + "\" c\n" },
	func(p string) string {
		return "requirementDiagram\n" + requirementBody + "classDef " + p + " fill:red\nclass a " + p + "\n"
	},
	func(p string) string {
		return "requirementDiagram\n" + requirementBody + "classDef \"" + p + "\" fill:red\nclass a \"" + p + "\"\n"
	},
	func(p string) string {
		return "requirementDiagram\n" + requirementBody + "classDef c fill:" + p + "\nclass a c\n"
	},
	func(p string) string {
		return "requirementDiagram\n" + requirementBody + "classDef c " + p + ":red\nclass a c\n"
	},
	func(p string) string { return "requirementDiagram\n" + requirementBody + "style a fill:" + p + "\n" },
	func(p string) string { return "requirementDiagram\n" + requirementBody + "style a " + p + "\n" },
	func(p string) string {
		return "requirementDiagram\n" + requirementBody + "style a color:" + p + ",stroke:" + p + ",font-family:" + p + "\n"
	},
	func(p string) string {
		return "requirementDiagram\n" + requirementBody + "style \"" + p + "\" fill:red\n"
	},
	func(p string) string {
		return "requirementDiagram\naccTitle: " + p + "\naccDescr: " + p + "\n" + requirementBody
	},
	func(p string) string { return "accTitle: " + p + "\nrequirementDiagram\n" + requirementBody },
	func(p string) string { return "requirementDiagram\naccDescr {\n " + p + "\n }\n" + requirementBody },
	func(p string) string { return "requirementDiagram\ndirection " + p + "\n" + requirementBody },
	func(p string) string { return "requirementDiagram\n" + p + " direction LR\n" + requirementBody },
	func(p string) string { return "requirementDiagram\n# " + p + "\n" + requirementBody },
	func(p string) string { return "requirementDiagram\n%% " + p + "\n" + requirementBody },
	func(p string) string { return "requirementDiagram " + p + "\n" + requirementBody },
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\nconfig:\n  requirement:\n    fontSize: \"" + p + "\"\n  theme: \"" + p + "\"\n---\nrequirementDiagram\n" + requirementBody
	},
	func(p string) string {
		return "%%{init: {\"requirement\": {\"rect_fill\": \"" + p + "\"}, \"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\nrequirementDiagram\n" + requirementBody
	},
}

var c4Templates = []func(p string) string{
	func(p string) string { return "C4Context\nPerson(" + p + ", \"L\")" },
	func(p string) string { return "C4Context\nPerson(\"" + p + "\", \"L\")" },
	func(p string) string { return "C4Context\nPerson(a, " + p + ", \"d\")" },
	func(p string) string { return "C4Context\nPerson(a, \"" + p + "\")" },
	func(p string) string { return "C4Context\nPerson_Ext(a, \"L\", \"" + p + "\")" },
	func(p string) string {
		return "C4Context\nSystem(a, \"L\", \"d\", \"" + p + "\", \"" + p + "\", \"" + p + "\")"
	},
	func(p string) string { return "C4Context\nSystemDb(a, \"L\", $link=\"" + p + "\")" },
	func(p string) string {
		return "C4Context\nSystemQueue_Ext(a, \"L\", $tags=\"" + p + "\", $sprite=\"" + p + "\")"
	},
	func(p string) string {
		return "C4Context\nSystem(a, \"L\", $descr=\"" + p + "\", $techn=\"" + p + "\", $type=\"" + p + "\", $label=\"" + p + "\")"
	},
	func(p string) string {
		return "C4Context\nSystem(a, \"L\", $bgColor=\"" + p + "\", $fontColor=\"" + p + "\", $borderColor=\"" + p + "\", $shape=\"" + p + "\")"
	},
	func(p string) string { return "C4Context\nSystem(a, \"L\", $" + p + "=\"x\")" },
	func(p string) string { return "C4Context\nSystem($" + p + "=\"x\", \"L\")" },
	func(p string) string { return "C4Context\nSystem(a, $link=\"" + p + "\")" },
	func(p string) string { return "C4Container\nContainer(a, \"" + p + "\", \"" + p + "\", \"" + p + "\")" },
	func(p string) string {
		return "C4Container\nContainerDb_Ext(a, \"L\", \"" + p + "\", \"d\", \"s\", \"t\", \"" + p + "\")"
	},
	func(p string) string { return "C4Component\nComponentQueue(a, \"L\", " + p + ", " + p + ")" },
	func(p string) string { return "C4Context\nBoundary(" + p + ", \"L\") {\nSystem(a, \"A\")\n}" },
	func(p string) string {
		return "C4Context\nBoundary(b, \"" + p + "\", \"" + p + "\") {\nSystem(a, \"A\")\n}"
	},
	func(p string) string {
		return "C4Context\nEnterprise_Boundary(b, \"L\", \"" + p + "\", \"" + p + "\") {\nSystem(a, \"A\")\n}"
	},
	func(p string) string {
		return "C4Context\nSystem_Boundary(b, \"L\", $link=\"" + p + "\", $tags=\"" + p + "\", $type=\"" + p + "\") {\nSystem(a, \"A\")\n}"
	},
	func(p string) string {
		return "C4Container\nContainer_Boundary(b, \"L\", $bgColor=\"" + p + "\", $borderColor=\"" + p + "\", $fontColor=\"" + p + "\") {\nSystem(a, \"A\")\n}"
	},
	func(p string) string {
		return "C4Deployment\nDeployment_Node(n, \"" + p + "\", \"" + p + "\", \"" + p + "\", \"" + p + "\", \"" + p + "\", \"" + p + "\") {\nContainer(a, \"A\")\n}"
	},
	func(p string) string {
		return "C4Deployment\nNode_L(n, \"L\", $descr=\"" + p + "\") {\nNode_R(m, \"M\", $link=\"" + p + "\")\n{\nContainer(a, \"A\")\n}\n}"
	},
	func(p string) string {
		return "C4Context\n" + c4Two + "Rel(a, b, \"" + p + "\", \"" + p + "\", \"" + p + "\")"
	},
	func(p string) string { return "C4Context\n" + c4Two + "Rel(a, b, " + p + ")" },
	func(p string) string {
		return "C4Context\n" + c4Two + "BiRel(a, b, \"L\", \"t\", \"d\", \"" + p + "\", \"" + p + "\", \"" + p + "\")"
	},
	func(p string) string {
		return "C4Context\n" + c4Two + "Rel_Back(a, b, \"L\", $link=\"" + p + "\", $techn=\"" + p + "\", $descr=\"" + p + "\")"
	},
	func(p string) string {
		return "C4Context\nSystem(" + p + ", \"A\")\nSystem(b, \"B\")\nRel(" + p + ", b, \"L\")"
	},
	func(p string) string { return "C4Context\nSystem(a, \"A\")\nRel(a, a, \"" + p + "\", \"" + p + "\")" },
	func(p string) string { return "C4Dynamic\n" + c4Two + "RelIndex(" + p + ", a, b, \"" + p + "\")" },
	func(p string) string { return "C4Context\n" + c4Two + "Rel(a, b, \"L\")\nRel(b, a, \"" + p + "\")" },
	func(p string) string {
		return "C4Context\n" + c4Two + "Rel(a, b, \"L\")\nUpdateRelStyle(a, b, \"" + p + "\", \"" + p + "\", \"" + p + "\", \"" + p + "\")"
	},
	func(p string) string {
		return "C4Context\n" + c4Two + "Rel(a, b, \"L\")\nUpdateRelStyle(a, b, $textColor=\"" + p + "\", $lineColor=\"" + p + "\", $offsetX=\"" + p + "\", $offsetY=\"" + p + "\")"
	},
	func(p string) string {
		return "C4Context\n" + c4Two + "Rel(a, b, \"L\")\nUpdateRelStyle(a, b, $offsetX=\"" + p + "\", $" + p + "=\"1\")"
	},
	func(p string) string {
		return "C4Context\n" + c4Two + "UpdateElementStyle(a, \"" + p + "\", \"" + p + "\", \"" + p + "\", \"" + p + "\", \"" + p + "\", \"" + p + "\", \"" + p + "\", \"" + p + "\", \"" + p + "\")"
	},
	func(p string) string {
		return "C4Context\n" + c4Two + "UpdateElementStyle(a, $bgColor=\"" + p + "\", $fontColor=\"" + p + "\", $borderColor=\"" + p + "\")"
	},
	func(p string) string {
		return "C4Context\n" + c4Two + "UpdateElementStyle(a, $shape=\"" + p + "\", $techn=\"" + p + "\", $legendText=\"" + p + "\", $label=\"" + p + "\")"
	},
	func(p string) string {
		return "C4Context\n" + c4Two + "UpdateLayoutConfig(\"" + p + "\", \"" + p + "\")"
	},
	func(p string) string {
		return "C4Context\n" + c4Two + "UpdateLayoutConfig($c4ShapeInRow=\"" + p + "\", $c4BoundaryInRow=\"" + p + "\")"
	},
	func(p string) string { return "C4Context\ntitle " + p + "\n" + c4Two },
	func(p string) string { return "C4Context\naccTitle: " + p + "\naccDescr: " + p + "\n" + c4Two },
	func(p string) string { return "C4Context\naccDescr {\n " + p + "\n }\n" + c4Two },
	func(p string) string { return "C4Context\naccDescription " + p + "\n" + c4Two },
	func(p string) string { return "C4Context " + p + "\n" + c4Two },
	func(p string) string { return "C4Context\n%% " + p + "\n" + c4Two },
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\nconfig:\n  c4:\n    c4ShapeInRow: \"" + p + "\"\n    wrap: \"" + p + "\"\n---\nC4Context\n" + c4Two
	},
	func(p string) string {
		return "%%{init: {\"c4\": {\"c4ShapeInRow\": \"" + p + "\"}, \"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\nC4Context\n" + c4Two
	},
}

var packetTemplates = []func(p string) string{
	func(p string) string { return "packet\n0-31: \"" + p + "\"" },
	func(p string) string { return "packet\n0-31: '" + p + "'" },
	func(p string) string { return "packet\n0: \"" + p + "\"\n1: \"" + p + "\"\n2-31: \"ok\"" },
	func(p string) string { return "packet\n+8: \"" + p + "\"\n+200: \"" + p + "\"" },
	func(p string) string { return "packet\ntitle " + p + "\n0-31: \"a\"" },
	func(p string) string { return "packet title " + p },
	func(p string) string { return "packet\naccTitle: " + p + "\naccDescr: " + p + "\n0-31: \"a\"" },
	func(p string) string { return "packet\naccDescr {\n " + p + "\n }\n0-31: \"a\"" },
	func(p string) string { return "packet\n0-31: \"a\" %% " + p },
	func(p string) string { return "packet\n" + p + ": \"a\"" },
	func(p string) string { return "packet\n0-" + p + ": \"a\"" },
	func(p string) string { return "packet " + p + "\n0-31: \"a\"" },
	func(p string) string { return "---\ntitle: \"" + p + "\"\n---\npacket\n0-31: \"a\"" },
	func(p string) string {
		return "---\nconfig:\n  packet:\n    bitOrder: \"" + p + "\"\n    bitsPerRow: \"" + p + "\"\n    showBits: \"" + p + "\"\n    rowHeight: \"" + p + "\"\n---\npacket\n0-31: \"a\""
	},
	func(p string) string {
		return "%%{init: {\"packet\": {\"bitWidth\": \"" + p + "\", \"paddingX\": \"" + p + "\"}, \"themeVariables\": {\"packet\": {\"blockFillColor\": \"" + p + "\"}}}}%%\npacket\n0-31: \"a\""
	},
}

var radarTemplates = []func(p string) string{
	func(p string) string { return "radar-beta\n axis a[\"" + p + "\"], b, c\n curve x{1,2,3}" },
	func(p string) string { return "radar-beta\n axis a['" + p + "'], b, c\n curve x{1,2,3}" },
	func(p string) string { return "radar-beta\n axis a, b, c\n curve x[\"" + p + "\"]{1,2,3}" },
	func(p string) string { return "radar-beta\n axis a, b, c\n curve x['" + p + "']{ a: 1, b: 2, c: 3 }" },
	func(p string) string { return "radar-beta\n axis " + p + ", b\n curve x{1,2}" },
	func(p string) string { return "radar-beta\n axis a, b\n curve " + p + "{1,2}" },
	func(p string) string { return "radar-beta\n axis a, b\n curve x{ " + p + ": 1, a: 1, b: 2 }" },
	func(p string) string { return "radar-beta\n axis a, b\n curve x{ " + p + ", 2 }" },
	func(p string) string { return "radar-beta\n title " + p + "\n axis a, b\n curve x{1,2}" },
	func(p string) string { return "radar-beta title " + p },
	func(p string) string {
		return "radar-beta\n accTitle: " + p + "\n accDescr: " + p + "\n axis a, b\n curve x{1,2}"
	},
	func(p string) string { return "radar-beta\n accDescr {\n " + p + "\n }\n axis a, b\n curve x{1,2}" },
	func(p string) string { return "radar-beta\n axis a, b\n curve x{1,2}\n graticule " + p },
	func(p string) string { return "radar-beta\n axis a, b\n curve x{1,2}\n max " + p + "\n ticks " + p },
	func(p string) string { return "radar-beta\n axis a, b %% " + p + "\n curve x{1,2}" },
	func(p string) string { return "radar-beta: " + p + "\n axis a, b" },
	func(p string) string { return "---\ntitle: \"" + p + "\"\n---\nradar-beta\n axis a, b\n curve x{1,2}" },
	func(p string) string {
		return "---\nconfig:\n  radar:\n    width: \"" + p + "\"\n    curveTension: \"" + p + "\"\n    marginLeft: \"" + p + "\"\n---\nradar-beta\n axis a, b\n curve x{1,2}"
	},
	func(p string) string {
		return "%%{init: {\"radar\": {\"axisLabelFactor\": \"" + p + "\"}, \"themeVariables\": {\"cScale0\": \"" + p + "\", \"radar\": {\"axisColor\": \"" + p + "\"}}}}%%\nradar-beta\n axis a, b\n curve x{1,2}"
	},
}

var treemapTemplates = []func(p string) string{
	func(p string) string { return "treemap\n\"" + p + "\"\n  \"a\": 1" },
	func(p string) string { return "treemap\n'" + p + "'\n  \"a\": 1" },
	func(p string) string { return "treemap\n\"s\"\n  \"" + p + "\": 1\n  \"b\": 2" },
	func(p string) string { return "treemap\n\"" + p + "\": 5" },
	func(p string) string { return "treemap\n\"s\":::" + p + "\n  \"a\": 1" },
	func(p string) string { return "treemap\n\"s\"\n  \"a\": 1:::" + p },
	func(p string) string { return "treemap\n\"s\":::c1\n  \"a\": 1:::c1\nclassDef c1 fill:" + p },
	func(p string) string { return "treemap\n\"s\":::c1\n  \"a\": 1:::c1\nclassDef c1 " + p + ":red" },
	func(p string) string {
		return "treemap\n\"s\":::c1\n  \"a\": 1:::c1\nclassDef c1 color:" + p + ",stroke:" + p + ",font-family:" + p + ",font-size:" + p
	},
	func(p string) string { return "treemap\n\"s\":::c1\n  \"a\": 1:::c1\nclassDef c1 " + p },
	func(p string) string { return "treemap\n\"s\"\n  \"a\": 1\nclassDef " + p + " fill:red" },
	func(p string) string { return "treemap\n\"s\"\n  \"a\": " + p },
	func(p string) string { return "treemap\ntitle " + p + "\n\"a\": 1" },
	func(p string) string { return "treemap title " + p },
	func(p string) string { return "treemap\naccTitle: " + p + "\naccDescr: " + p + "\n\"a\": 1" },
	func(p string) string { return "treemap\naccDescr {\n " + p + "\n }\n\"a\": 1" },
	func(p string) string { return "treemap\n\"a\": 1 %% " + p },
	func(p string) string { return "treemap-beta " + p + "\n\"a\": 1" },
	func(p string) string { return "---\ntitle: \"" + p + "\"\n---\ntreemap\n\"a\": 1" },
	func(p string) string {
		return "---\nconfig:\n  treemap:\n    valueFormat: \"" + p + "\"\n    padding: \"" + p + "\"\n    showValues: \"" + p + "\"\n---\ntreemap\n\"s\"\n  \"a\": 1"
	},
	func(p string) string {
		return "%%{init: {\"treemap\": {\"nodeWidth\": \"" + p + "\", \"valueFormat\": \"" + p + "\"}, \"themeVariables\": {\"cScale0\": \"" + p + "\"}}}%%\ntreemap\n\"a\": 1"
	},
}

var infoTemplates = []func(p string) string{
	func(p string) string { return "info\ntitle " + p },
	func(p string) string { return "info showInfo title " + p },
	func(p string) string { return "info\naccTitle: " + p + "\naccDescr: " + p },
	func(p string) string { return "info\naccDescr {\n " + p + "\n }" },
	func(p string) string { return "info %% " + p },
	func(p string) string { return "info " + p },
	func(p string) string { return "---\ntitle: \"" + p + "\"\n---\ninfo" },
	func(p string) string {
		return "%%{init: {\"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\ninfo"
	},
}

var treeviewTemplates = []func(p string) string{
	func(p string) string { return "treeView-beta\n  " + p },
	func(p string) string { return "treeView-beta\n  " + p + "/\n    child" },
	func(p string) string { return "treeView-beta\n  \"" + p + "\"" },
	func(p string) string { return "treeView-beta\n  '" + p + "'" },
	func(p string) string { return "treeView-beta\n  a ## " + p },
	func(p string) string { return "treeView-beta\n  a :::" + p },
	func(p string) string { return "treeView-beta\n  a :::highlight ## " + p },
	func(p string) string { return "treeView-beta\n  a icon(" + p + ")" },
	func(p string) string { return "treeView-beta\n  a icon(x:" + p + ")" },
	func(p string) string { return "treeView-beta\ntitle " + p + "\n  a" },
	func(p string) string { return "treeView-beta\naccTitle: " + p + "\naccDescr: " + p + "\n  a" },
	func(p string) string { return "treeView-beta\naccDescr {\n " + p + "\n}\n  a" },
	func(p string) string {
		return "treeView-beta\n├── " + p + "\n│   └── " + p + " ## " + p + "\n└── x"
	},
	func(p string) string { return "---\ntitle: \"" + stripQuotes(p) + "\"\n---\ntreeView-beta\n  a" },
	func(p string) string {
		return "---\nconfig:\n  treeView:\n    showIcons: true\n    defaultIconPack: '" + stripApostrophes(p) + "'\n    filenameIcons:\n      a: '" + stripApostrophes(p) + "'\n    extensionIcons:\n      .b: '" + stripApostrophes(p) + "'\n---\ntreeView-beta\n  a\n  c.b\n  d icon(e)\n  " + p
	},
	func(p string) string {
		return "---\nconfig:\n  treeView:\n    rowIndent: '" + stripApostrophes(p) + "'\n    paddingX: 1e999\n    paddingY: -5\n    lineThickness: .nan\n---\ntreeView-beta\n  a\n    b"
	},
}

var ishikawaTemplates = []func(p string) string{
	func(p string) string { return ishikawaHead + p },
	func(p string) string { return ishikawaHead + "  " + p },
	func(p string) string { return ishikawaHead + "  Effect\n    " + p },
	func(p string) string { return ishikawaHead + "  Effect\n    Category\n      " + p },
	func(p string) string { return ishikawaHead + "  Effect\n    Category\n      Cause\n        " + p },
	func(p string) string {
		return ishikawaHead + "  Effect\n    Category\n      Cause\n        Sub\n          " + p + "\n            " + p
	},
	func(p string) string {
		return ishikawaHead + "  " + p + "\n    " + p + "\n      " + p + "\n    " + p + "\n      " + p + "\n        " + p
	},
	func(p string) string {
		return ishikawaHead + "  Effect " + p + "\n    Category " + p + "\n      Cause " + p + " more"
	},
	func(p string) string { return ishikawaHead + "  Effect\n    A\n    B\n    " + p + "\n    C" },
	func(p string) string { return ishikawaHead + "  Effect\n%% " + p + "\n    Category" },
	func(p string) string { return ishikawaHead + "  Effect\n    Category %% " + p },
	func(p string) string { return "ishikawa " + p },
	func(p string) string { return "ishikawa-beta " + p + "\n  Cause" },
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\n---\n" + ishikawaHead + "  Effect\n    Cause"
	},
	func(p string) string {
		return "---\ntitle: " + p + "\nconfig:\n  ishikawa:\n    diagramPadding: \"" + p + "\"\n---\n" + ishikawaHead + "  Effect"
	},
	func(p string) string {
		return "%%{init: {\"ishikawa\": {\"useMaxWidth\": \"" + p + "\"}, \"themeVariables\": {\"lineColor\": \"" + p + "\"}}}%%\n" + ishikawaHead + "  Effect\n    Cause"
	},
}

var cynefinTemplates = []func(p string) string{
	func(p string) string { return "cynefin-beta\ncomplex\n\"" + cynefinQuoted(p) + "\"" },
	func(p string) string { return "cynefin-beta\ncomplex\n'" + cynefinSingle(p) + "'" },
	func(p string) string {
		return "cynefin-beta\nconfusion\n\"" + cynefinQuoted(p) + "\"\n\"" + cynefinQuoted(p) + "\"\n\"" + cynefinQuoted(p) + "\"\n\"" + cynefinQuoted(p) + "\""
	},
	func(p string) string { return "cynefin-beta\nclear\n\"a \\\"" + cynefinQuoted(p) + "\\\" \\\\ b\"" },
	func(p string) string { return "cynefin-beta\ncomplex --> clear : \"" + cynefinQuoted(p) + "\"" },
	func(p string) string { return "cynefin-beta\nconfusion --> chaotic : '" + cynefinSingle(p) + "'" },
	func(p string) string { return "cynefin-beta\ntitle " + p + "\ncomplex" },
	func(p string) string { return "cynefin-beta\naccTitle: " + p + "\naccDescr: " + p + "\ncomplex" },
	func(p string) string { return "cynefin-beta\naccDescr {\n " + p + "\n}\ncomplex" },
	func(p string) string { return "cynefin-beta\n" + p },
	func(p string) string { return "cynefin-beta\ncomplex --> " + p },
	func(p string) string {
		return "---\ntitle: \"" + cynefinQuoted(p) + "\"\n---\ncynefin-beta\ncomplex\n\"a\""
	},
	func(p string) string {
		return "---\nconfig:\n  cynefin:\n    width: '" + cynefinSingle(p) + "'\n    height: 1e999\n    seed: .nan\n    boundaryAmplitude: '" + cynefinSingle(p) + "'\n    showDomainDescriptions: '" + cynefinSingle(p) + "'\n---\ncynefin-beta\ncomplex\n\"a\"\nclear --> chaotic : \"b\""
	},
}

var eventmodelingTemplates = []func(p string) string{
	func(p string) string { return "eventmodeling\ntf 01 cmd A { " + emLine(p) + " }" },
	func(p string) string { return "eventmodeling\ntf 01 evt A \"" + emLine(p) + "\"" },
	func(p string) string { return "eventmodeling\ntf 01 evt A '" + emLine(p) + "'" },
	func(p string) string { return "eventmodeling\ntf 01 rmo A `json`{ \"k\": \"" + emLine(p) + "\" }" },
	func(p string) string { return "eventmodeling\ntf 01 evt A [[D]]\ndata D {\n  " + p + "\n}\n" },
	func(p string) string { return "eventmodeling\ntf 01 evt A\nnote 01 {\n  " + p + "\n}\n" },
	func(p string) string {
		return "eventmodeling\ntf 01 evt A\nnote 01 `html` {\n  <div>" + p + "</div>\n}\n"
	},
	func(p string) string { return "eventmodeling title " + emLine(p) + "\ntf 01 ui A" },
	func(p string) string {
		return "eventmodeling accTitle: " + emLine(p) + "\ntf 01 ui A accDescr: " + emLine(p)
	},
	func(p string) string { return "eventmodeling accDescr {\n " + p + "\n}\ntf 01 ui A" },
	func(p string) string { return "eventmodeling\ntf 01 ui " + p },
	func(p string) string { return "eventmodeling\ntf 01 ui A." + p },
	func(p string) string { return "eventmodeling\ntf 01 evt A\ngwt 01 given evt " + p + " then evt B" },
	func(p string) string { return "eventmodeling\n/* " + p + " */\ntf 01 ui A // " + emLine(p) + "\n" },
	func(p string) string { return "---\ntitle: \"" + gitStrip(p) + "\"\n---\neventmodeling\ntf 01 ui A" },
}

var vennTemplates = []func(p string) string{
	func(p string) string { return vennHead + p },
	func(p string) string { return vennHead + "set " + p },
	func(p string) string { return vennHead + "set \"" + p + "\"" },
	func(p string) string { return vennHead + "set A[" + p + "]" },
	func(p string) string { return vennHead + "set A[\"" + p + "\"]" },
	func(p string) string { return vennHead + "set A[\"" + p + "\"]:" + p },
	func(p string) string { return vennHead + "set A:" + p },
	func(p string) string { return vennHead + "set \"" + p + "\"\nset B\nunion \"" + p + "\",B" },
	func(p string) string { return vennHead + "set A\nset B\nunion A,B[" + p + "]" },
	func(p string) string { return vennHead + "set A\nset B\nunion A,B[\"" + p + "\"]" },
	func(p string) string { return vennHead + "set A\nset B\nunion A,B:" + p },
	func(p string) string { return vennHead + "set A\nset B\nset C\nunion A,B,C[\"" + p + "\"]" },
	func(p string) string { return vennHead + "set A\nunion A," + p },
	func(p string) string { return vennHead + "set A\n  text " + p },
	func(p string) string { return vennHead + "set A\n  text \"" + p + "\"" },
	func(p string) string { return vennHead + "set A\n  text T[" + p + "]" },
	func(p string) string { return vennHead + "set A\n  text T[\"" + p + "\"]" },
	func(p string) string { return vennHead + "set A\n  text \"" + p + "\"[\"" + p + "\"]" },
	func(p string) string { return vennHead + "set A\ntext A \"" + p + "\"" },
	func(p string) string { return vennHead + "set A\ntext A T[\"" + p + "\"]" },
	func(p string) string {
		return vennHead + "set A\nset B\nunion A,B\ntext A,B \"" + p + "\"[\"" + p + "\"]"
	},
	func(p string) string { return vennHead + "set A\ntext " + p + " T" },
	func(p string) string { return vennHead + "set A\nstyle A fill:" + p },
	func(p string) string { return vennHead + "set A\nstyle A fill:\"" + p + "\"" },
	func(p string) string { return vennHead + "set A\nstyle A " + p + ":red" },
	func(p string) string {
		return vennHead + "set A\nstyle A color:\"" + p + "\", stroke:\"" + p + "\", stroke-width:\"" + p + "\", fill-opacity:\"" + p + "\""
	},
	func(p string) string {
		return vennHead + "set A\nset B\nunion A,B[\"x\"]\nstyle A,B fill:\"" + p + "\", color:\"" + p + "\""
	},
	func(p string) string { return vennHead + "set A\n  text T\nstyle T color:\"" + p + "\"" },
	func(p string) string { return vennHead + "set A\nstyle " + p + " fill:red" },
	func(p string) string { return vennHead + "set A\nstyle \"" + p + "\" fill:red" },
	func(p string) string { return vennHead + "title " + p + "\nset A" },
	func(p string) string { return vennHead + "title \"" + p + "\"\nset A" },
	func(p string) string { return vennHead + "%% " + p + "\nset A" },
	func(p string) string { return vennHead + "set A %% " + p },
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\nconfig:\n  venn:\n    width: \"" + p + "\"\n    padding: \"" + p + "\"\n---\n" + vennHead + "set A"
	},
	func(p string) string { return "---\ntitle: " + p + "\n---\n" + vennHead + "set A\nset B" },
	func(p string) string {
		return "%%{init: {\"venn\": {\"useDebugLayout\": \"" + p + "\"}, \"themeVariables\": {\"venn1\": \"" + p + "\"}}}%%\n" + vennHead + "set A"
	},
	func(p string) string { return "venn-beta " + p + "\nset A" },
}

var wardleyTemplates = []func(p string) string{
	func(p string) string { return "wardley-beta\ncomponent \"" + cynefinQuoted(p) + "\" [0.5, 0.5]" },
	func(p string) string {
		return "wardley-beta\ncomponent '" + cynefinSingle(p) + "' [0.5, 0.5] (buy) (inertia)"
	},
	func(p string) string { return "wardley-beta\ncomponent " + p + " [0.5, 0.5]" },
	func(p string) string {
		return "wardley-beta\nanchor \"" + cynefinQuoted(p) + "\" [0.9, 0.5]\ncomponent B [0.5, 0.5]\n\"" + cynefinQuoted(p) + "\" -> B"
	},
	func(p string) string {
		return "wardley-beta\ncomponent A [0.9, 0.1]\ncomponent B [0.5, 0.5]\nA -> B; " + p
	},
	func(p string) string {
		return "wardley-beta\ncomponent A [0.9, 0.1]\ncomponent B [0.5, 0.5]\nA +'" + cynefinSingle(p) + "'> B"
	},
	func(p string) string {
		return "wardley-beta\ncomponent A [0.9, 0.1]\n\"" + cynefinQuoted(p) + "\" -> A\nA -> \"" + cynefinQuoted(p) + "\""
	},
	func(p string) string { return "wardley-beta\nnote \"" + cynefinQuoted(p) + "\" [0.5, 0.5]" },
	func(p string) string {
		return "wardley-beta\nannotations [0.9, 0.1]\nannotation 1,[0.5, 0.5] \"" + cynefinQuoted(p) + "\""
	},
	func(p string) string { return "wardley-beta\nannotation 7,[0.5, 0.5] \"" + cynefinQuoted(p) + "\"" },
	func(p string) string {
		return "wardley-beta\naccelerator \"" + cynefinQuoted(p) + "\" [0.5, 0.5]\ndeaccelerator \"" + cynefinQuoted(p) + "\" [0.2, 0.2]"
	},
	func(p string) string {
		return "wardley-beta\nevolution \"" + cynefinQuoted(p) + "\" -> \"" + cynefinQuoted(p) + "\"@0.5 / \"" + cynefinQuoted(p) + "\" -> C\ncomponent A [0.5, 0.5]"
	},
	func(p string) string {
		return "wardley-beta\ncomponent \"" + cynefinQuoted(p) + "\" [0.5, 0.5]\nevolve \"" + cynefinQuoted(p) + "\" 0.9"
	},
	func(p string) string {
		return "wardley-beta\ncomponent \"" + cynefinQuoted(p) + "\" [0.5, 0.5]\npipeline \"" + cynefinQuoted(p) + "\" {\n component \"" + cynefinQuoted(p) + "\" [0.2]\n component B [0.8]\n}"
	},
	func(p string) string { return "wardley-beta\ntitle " + p + "\ncomponent A [0.5, 0.5]" },
	func(p string) string {
		return "wardley-beta\naccTitle: " + p + "\naccDescr: " + p + "\ncomponent A [0.5, 0.5]"
	},
	func(p string) string { return "wardley-beta\naccDescr {\n " + p + "\n}\ncomponent A [0.5, 0.5]" },
	func(p string) string {
		return "---\ntitle: \"" + cynefinQuoted(p) + "\"\nconfig:\n  wardley-beta:\n    width: '" + cynefinSingle(p) + "'\n    height: 1e999\n    showGrid: '" + cynefinSingle(p) + "'\n---\nwardley-beta\ncomponent A [0.5, 0.5]"
	},
}

var railroadTemplates = []func(p string) string{
	func(p string) string { return "railroad-beta\nr = terminal(\"" + railroadQuoted(p) + "\") ;" },
	func(p string) string { return "railroad-beta\nr = terminal('" + railroadSingle(p) + "') ;" },
	func(p string) string { return "railroad-beta\nr = nonterminal(\"" + railroadQuoted(p) + "\") ;" },
	func(p string) string { return "railroad-beta\nr = special(\"" + railroadQuoted(p) + "\") ;" },
	func(p string) string {
		return "railroad-beta\nr = sequence(terminal(\"" + railroadQuoted(p) + "\"), choice(nonterminal(\"" + railroadQuoted(p) + "\"), optional(special(\"" + railroadQuoted(p) + "\")))) ;"
	},
	func(p string) string {
		return "railroad-beta\nr = zeroOrMore(oneOrMore(terminal(\"" + railroadQuoted(p) + "\"))) ;"
	},
	func(p string) string { return "railroad-beta\nr = terminal(\"" + p + "\") ;" },
	func(p string) string { return "railroad-beta\n" + p + " = terminal(\"a\") ;" },
	func(p string) string { return "railroad-beta\nr = " + p + " ;" },
	func(p string) string { return "railroad-beta\nr = " + p + "(\"a\") ;" },
	func(p string) string { return "railroad-beta\ntitle " + p + "\nr = terminal(\"a\") ;" },
	func(p string) string { return "railroad-beta\ntitle \"" + p + "\"\nr = terminal(\"a\") ;" },
	func(p string) string { return "railroad-beta title " + p },
	func(p string) string {
		return "railroad-beta\naccTitle: " + p + "\naccDescr: " + p + "\nr = terminal(\"a\") ;"
	},
	func(p string) string { return "railroad-beta\naccDescr {\n " + p + "\n }\nr = terminal(\"a\") ;" },
	func(p string) string { return "railroad-beta\n/* " + p + " */\nr = terminal(\"a\") ; %% " + p },
	func(p string) string { return "railroad-beta " + p + "\nr = terminal(\"a\") ;" },
	func(p string) string { return "railroad-ebnf-beta\nr = \"" + railroadQuoted(p) + "\" ;" },
	func(p string) string { return "railroad-ebnf-beta\nr = \"" + p + "\" ;" },
	func(p string) string { return "railroad-ebnf-beta\nr = ? " + railroadNoSpecial(p) + " ? ;" },
	func(p string) string { return "railroad-ebnf-beta\nr = ? " + p + " ? ;" },
	func(p string) string {
		return "railroad-ebnf-beta\nr = a - \"" + railroadQuoted(p) + "\" | [ \"" + railroadQuoted(p) + "\" ] | { ? " + railroadNoSpecial(p) + " ? }+ ;"
	},
	func(p string) string { return "railroad-ebnf-beta\nr = " + p + " ;" },
	func(p string) string { return "railroad-ebnf-beta\n" + p + " ::= a ;" },
	func(p string) string { return "railroad-ebnf-beta\nr = a (* " + p + " *) b /* " + p + " */ ;" },
	func(p string) string {
		return "railroad-ebnf-beta\ntitle " + p + "\naccTitle: " + p + "\naccDescr { " + p + " }\nr = a ;"
	},
	func(p string) string { return "railroad-abnf-beta\nr = \"" + railroadBare(p) + "\" ;" },
	func(p string) string {
		return "railroad-abnf-beta\nr = 2*3\"" + railroadBare(p) + "\" [ \"" + railroadBare(p) + "\" ] / *( \"" + railroadBare(p) + "\" ) ;"
	},
	func(p string) string { return "railroad-abnf-beta\nr = \"" + p + "\" ;" },
	func(p string) string { return "railroad-abnf-beta\nr = " + p + " ;" },
	func(p string) string { return "railroad-abnf-beta\nr = %x" + p + " ;" },
	func(p string) string { return "railroad-abnf-beta\nr = " + p + "*" + p + "a ;" },
	func(p string) string { return "railroad-abnf-beta\n" + p + " = a ;" },
	func(p string) string { return "railroad-abnf-beta\nr = a ; " + p + "\n ;\n" },
	func(p string) string { return "railroad-abnf-beta\ntitle \"" + p + "\"\nr = a ;" },
	func(p string) string { return "railroad-peg-beta\nR <- \"" + railroadQuoted(p) + "\" ;" },
	func(p string) string {
		return "railroad-peg-beta\nR <- !\"" + railroadQuoted(p) + "\" &'x' ( \"" + railroadQuoted(p) + "\" )* . ;"
	},
	func(p string) string { return "railroad-peg-beta\nR <- \"" + p + "\" ;" },
	func(p string) string { return "railroad-peg-beta\nR <- " + p + " ;" },
	func(p string) string { return "railroad-peg-beta\nR <- !" + p + " ;" },
	func(p string) string { return "railroad-peg-beta\n" + p + " <- a ;" },
	func(p string) string { return "railroad-peg-beta\nR <- a # " + p + "\n ;" },
	func(p string) string { return "railroad-peg-beta\ntitle " + p + "\naccDescr: " + p + "\nR <- a ;" },
	func(p string) string {
		return "---\ntitle: \"" + railroadQuoted(p) + "\"\n---\nrailroad-ebnf-beta\nr = a ;"
	},
	func(p string) string { return "---\ntitle: " + p + "\n---\nrailroad-ebnf-beta\nr = a ;" },
	func(p string) string {
		return "---\nconfig:\n  railroad:\n    arcRadius: \"" + railroadQuoted(p) + "\"\n    padding: \"" + railroadQuoted(p) + "\"\n    fontSize: \"" + railroadQuoted(p) + "\"\n    fontFamily: \"" + railroadQuoted(p) + "\"\n    terminalFill: \"" + railroadQuoted(p) + "\"\n    lineColor: \"" + railroadQuoted(p) + "\"\n    strokeWidth: \"" + railroadQuoted(p) + "\"\n    showMarkers: \"" + railroadQuoted(p) + "\"\n    markerRadius: \"" + railroadQuoted(p) + "\"\n---\nrailroad-ebnf-beta\nr = a \"b\" ? c ? ;"
	},
	func(p string) string {
		return "%%{init: {\"railroad\": {\"arcRadius\": \"" + railroadQuoted(p) + "\", \"strokeWidth\": \"" + railroadQuoted(p) + "\"}, \"themeVariables\": {\"fontFamily\": \"" + railroadQuoted(p) + "\"}}}%%\nrailroad-ebnf-beta\nr = a+ ;"
	},
}

var swimlaneTemplates = []func(p string) string{
	func(p string) string { return "swimlane-beta TD\n  subgraph L[\"" + p + "\"]\n    a\n  end" },
	func(p string) string {
		return "swimlane-beta LR\n  subgraph L[\"" + p + "\"]\n    a[\"" + p + "\"] -- \"" + p + "\" --> b\n  end"
	},
	func(p string) string { return "swimlane-beta TD\n  subgraph " + p + "\n    a\n  end" },
	func(p string) string { return "swimlane-beta " + p + "\n  a --> b" },
	func(p string) string {
		return "swimlane-beta TD\n  subgraph L\n    a\n  end\n  style L " + p + "\n  class L " + p
	},
	func(p string) string {
		return "swimlane-beta LR\n  subgraph L\n    a\n  end\n  click a \"" + p + "\" \"" + p + "\"\n  L:::" + p
	},
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\nconfig:\n  swimlane: \"" + p + "\"\n---\nswimlane-beta TD\n  a"
	},
	func(p string) string {
		return "swimlane-beta RL\n  subgraph L[\"`**" + p + "**`\"]\n    a\n  end\n  accTitle: " + p + "\n  accDescr: " + p
	},
}

var usecaseTemplates = []func(p string) string{
	func(p string) string { return usecaseHead + p },
	func(p string) string { return usecaseHead + "actor " + p },
	func(p string) string { return usecaseHead + "actor A(" + p + ")" },
	func(p string) string { return usecaseHead + "actor A(\"" + p + "\")" },
	func(p string) string { return usecaseHead + "actor A('" + p + "')" },
	func(p string) string { return usecaseHead + "actor A(\"`" + p + "`\")" },
	func(p string) string { return usecaseHead + "actor \"" + p + "\"" },
	func(p string) string { return usecaseHead + "actor \"`" + p + "`\"" },
	func(p string) string { return usecaseHead + "actor A <<" + p + ">>" },
	func(p string) string { return usecaseHead + "actor A:::" + p },
	func(p string) string { return usecaseHead + "actor A@{ icon: \"" + p + "\" }" },
	func(p string) string { return usecaseHead + "actor A@{ type: \"" + p + "\" }" },
	func(p string) string { return usecaseHead + "actor A@{ \"" + p + "\": true }" },
	func(p string) string { return usecaseHead + "actor A, \"" + p + "\"" },
	func(p string) string { return usecaseHead + "U(" + p + ")" },
	func(p string) string { return usecaseHead + "U[" + p + "]" },
	func(p string) string { return usecaseHead + "U(\"" + p + "\")" },
	func(p string) string { return usecaseHead + "U[\"`" + p + "`\"]" },
	func(p string) string { return usecaseHead + "\"" + p + "\"" },
	func(p string) string { return usecaseHead + "'" + p + "'" },
	func(p string) string { return usecaseHead + "\"`" + p + "`\"" },
	func(p string) string { return usecaseHead + "U(x) <<" + p + ">>:::" + p },
	func(p string) string { return usecaseHead + "U(x)@{ business: true } <<" + p + ">>" },
	func(p string) string { return usecaseHead + "A --> \"" + p + "\"" },
	func(p string) string { return usecaseHead + "\"" + p + "\" --> \"" + p + " \"" },
	func(p string) string { return usecaseHead + "A -- " + p + " --> B" },
	func(p string) string { return usecaseHead + "A -- \"" + p + "\" --> B" },
	func(p string) string { return usecaseHead + "A -- \"`" + p + "`\" --> B" },
	func(p string) string { return usecaseHead + "A <-- \"" + p + "\" -- B" },
	func(p string) string { return usecaseHead + "A o-- \"" + p + "\" -- B\nA x-- '" + p + "' -- B" },
	func(p string) string { return usecaseHead + "A -- \"" + p + "\" --o B\nA -- \"" + p + "\" --x B" },
	func(p string) string { return usecaseHead + "A -- \"" + p + "\" --> A" },
	func(p string) string { return usecaseHead + "A " + p + "@--> B" },
	func(p string) string { return usecaseHead + "A e@--> B\ne@{ animation: \"" + p + "\" }" },
	func(p string) string { return usecaseHead + "A ..> : include B\nA ..> : " + p + " B" },
	func(p string) string { return usecaseHead + "A " + p + " B" },
	func(p string) string { return usecaseHead + "systemBoundary " + p + "\nend" },
	func(p string) string { return usecaseHead + "systemBoundary \"" + p + "\"\n  U\nend" },
	func(p string) string { return usecaseHead + "systemBoundary S[" + p + "]\n  U\nend" },
	func(p string) string {
		return usecaseHead + "systemBoundary S(\"`" + p + "`\")@{ type: package }\n  U\nend"
	},
	func(p string) string {
		return usecaseHead + "systemBoundary S[\"" + p + "\"]@{ type: package }:::" + p + "\n  actor \"" + p + "\"\n  \"" + p + " \"\nend"
	},
	func(p string) string { return usecaseHead + "systemBoundary S@{ type: \"" + p + "\" }\nend" },
	func(p string) string { return usecaseHead + "U\nnote for U " + p },
	func(p string) string { return usecaseHead + "U\nnote for U \"" + p + "\"" },
	func(p string) string { return usecaseHead + "U\nnote for U \"`" + p + "`\"" },
	func(p string) string { return usecaseHead + "U\nnote for " + p + " \"x\"" },
	func(p string) string { return usecaseHead + "json P@{\"" + p + "\": \"" + p + "\"}" },
	func(p string) string {
		return usecaseHead + "json P@{\"a\": [\"" + p + "\", {\"" + p + "\": null}], \"b\": {\"" + p + "\": 1}}"
	},
	func(p string) string {
		return usecaseHead + "json P@{\"k\": " + jsonText(p) + ", " + jsonText(p) + ": [" + jsonText(p) + "]}"
	},
	func(p string) string { return usecaseHead + "json " + p + "@{}" },
	func(p string) string { return usecaseHead + "json P@{}:::" + p },
	func(p string) string { return usecaseHead + "json P@" + p },
	func(p string) string { return usecaseHead + "A\nclass A " + p },
	func(p string) string { return usecaseHead + "A\nclass " + p + " c" },
	func(p string) string { return usecaseHead + "A\nclassDef " + p + " fill:red\nclass A " + p },
	func(p string) string { return usecaseHead + "A\nclassDef c fill:" + p + "\nclass A c" },
	func(p string) string { return usecaseHead + "A\nclassDef c " + p + ":red\nclass A c" },
	func(p string) string {
		return usecaseHead + "A\nclassDef default fill:" + p + ",color:" + p + ",stroke:" + p
	},
	func(p string) string { return usecaseHead + "A\nstyle A fill:" + p },
	func(p string) string { return usecaseHead + "A\nstyle A " + p + ":red" },
	func(p string) string {
		return usecaseHead + "actor A\nstyle A color:" + p + ",stroke:" + p + ",font-family:" + p + ",font-size:" + p
	},
	func(p string) string {
		return usecaseHead + "A e@--> B\nstyle e stroke:" + p + ",stroke-width:" + p + "\nclass e " + p
	},
	func(p string) string {
		return usecaseHead + "systemBoundary S\nend\nstyle S fill:" + p + "\nclass S " + p
	},
	func(p string) string { return usecaseHead + "json P@{}\nstyle P fill:" + p + ",stroke-width:" + p },
	func(p string) string { return usecaseHead + "A\nstyle " + p + " fill:red" },
	func(p string) string { return usecaseHead + "accTitle: " + p + "\naccDescr: " + p + "\nA" },
	func(p string) string { return usecaseHead + "accDescr {\n " + p + "\n}\nA" },
	func(p string) string { return usecaseHead + "direction " + p + "\nA" },
	func(p string) string { return usecaseHead + "direction TB " + p + "\nA" },
	func(p string) string { return usecaseHead + "%% " + p + "\nA" },
	func(p string) string { return usecaseHead + "A@{ " + p + ": true }\nA" },
	func(p string) string { return usecaseHead + "A\nA@{ business: \"" + p + "\" }" },
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\nconfig:\n  usecase:\n    nodeSpacing: \"" + p + "\"\n    rankSpacing: \"" + p + "\"\n---\n" + usecaseHead + "actor A\nA --> B"
	},
	func(p string) string { return "---\ntitle: " + p + "\n---\n" + usecaseHead + "A" },
	func(p string) string {
		return "%%{init: {\"usecase\": {\"nodeSpacing\": \"" + p + "\"}, \"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\n" + usecaseHead + "A"
	},
	func(p string) string { return "usecase-beta " + p + "\nA" },
}

var agentflowTemplates = []func(p string) string{
	func(p string) string { return agentflowHead + "  " + p },
	func(p string) string { return agentflowHead + "  " + p + " --> b" },
	func(p string) string { return agentflowHead + "  a --> " + p },
	func(p string) string { return agentflowHead + "  a[" + p + "]" },
	func(p string) string { return agentflowHead + "  a[\"" + p + "\"]" },
	func(p string) string { return agentflowHead + "  a[\"`" + p + "`\"]" },
	func(p string) string { return agentflowHead + "  a(" + p + ")" },
	func(p string) string { return agentflowHead + "  a{" + p + "}" },
	func(p string) string { return agentflowHead + "  a{\"" + p + "\"}" },
	func(p string) string { return agentflowHead + "  a{{" + p + "}}" },
	func(p string) string { return agentflowHead + "  a[[" + p + "]]" },
	func(p string) string { return agentflowHead + "  a[/" + p + "/]" },
	func(p string) string { return agentflowHead + "  a[\\" + p + "\\]" },
	func(p string) string { return agentflowHead + "  a(-" + p + "-)" },
	func(p string) string { return agentflowHead + "  a>" + p + "]" },
	func(p string) string { return agentflowHead + "  a((" + p + "))" },
	func(p string) string { return agentflowHead + "  a([" + p + "])" },
	func(p string) string { return agentflowHead + "  a[|" + p + ":" + p + "|x]" },
	func(p string) string { return agentflowHead + "  a -- " + p + " --> b" },
	func(p string) string { return agentflowHead + "  a -- \"" + p + "\" --> b" },
	func(p string) string { return agentflowHead + "  a -->|" + p + "| b" },
	func(p string) string { return agentflowHead + "  a -->|\"" + p + "\"| b" },
	func(p string) string { return agentflowHead + "  a --x|\"" + p + "\"| a" },
	func(p string) string { return agentflowHead + "  a " + p + "@--> b" },
	func(p string) string {
		return agentflowHead + "  a e1@--> b\n  e1@{ curve: \"" + p + "\", animate: \"" + p + "\", animation: \"" + p + "\", instruction: \"" + p + "\" }"
	},
	func(p string) string { return agentflowHead + "  a@{ label: \"" + p + "\" }" },
	func(p string) string { return agentflowHead + "  a@{ label: \"" + p + "\", labelType: \"" + p + "\" }" },
	func(p string) string { return agentflowHead + "  a@{ shape: " + p + " }" },
	func(p string) string { return agentflowHead + "  a@{ shape: \"" + p + "\" }" },
	func(p string) string { return agentflowHead + "  a@{ \"" + p + "\": \"" + p + "\" }" },
	func(p string) string { return agentflowHead + "  a@{ " + p + " }" },
	func(p string) string { return agentflowHead + "  a@{\n    " + p + "\n  }" },
	func(p string) string { return agentflowHead + "  a@{\n    label: |\n      " + p + "\n  }" },
	func(p string) string {
		return agentflowHead + "  a[\"A\"]@{ shape: tool, icon: \"" + p + "\", img: \"" + p + "\", form: \"" + p + "\", pos: \"" + p + "\", w: \"" + p + "\", h: \"" + p + "\" }"
	},
	func(p string) string {
		return agentflowHead + "  a@{ shape: tool, class: \"" + p + "\", style: \"" + p + "\", view: \"" + p + "\" }"
	},
	func(p string) string { return agentflowHead + "  flow " + p + "\n  a\n  end" },
	func(p string) string { return agentflowHead + "  flow \"" + p + "\"\n  a\n  end" },
	func(p string) string { return agentflowHead + "  flow f[" + p + "]\n  a\n  end" },
	func(p string) string { return agentflowHead + "  flow f[\"" + p + "\"]\n  a --> f\n  end\n  f --> b" },
	func(p string) string {
		return agentflowHead + "  flow f[\"" + p + "\"]@{ view: collapsed, model: \"" + p + "\" }\n  a\n  end\n  a --> b"
	},
	func(p string) string {
		return agentflowHead + "  flow f\n  a\n  end\n  f@{ view: \"" + p + "\", \"" + p + "\": 1 }"
	},
	func(p string) string {
		return agentflowHead + "  flow f\n  a\n  end\n  style f fill:" + p + "\n  class f " + p
	},
	func(p string) string { return agentflowHead + "  flow f\n  direction " + p + "\n  a\n  end" },
	func(p string) string { return agentflowHead + "  connector " + p },
	func(p string) string { return agentflowHead + "  connector \"" + p + "\"" },
	func(p string) string { return agentflowHead + "  connector c[" + p + "]" },
	func(p string) string {
		return agentflowHead + "  connector c[\"" + p + "\"]@{ protocol: \"" + p + "\", \"" + p + "\": \"" + p + "\" }\n  c --> a"
	},
	func(p string) string {
		return agentflowHead + "  global\n  " + p + "\n  a[\"" + p + "\"]\n  end\n  flow f\n  a\n  end"
	},
	func(p string) string { return agentflowHead + "  a:::" + p },
	func(p string) string { return agentflowHead + "  a:::" + p + " --> b:::" + p },
	func(p string) string { return agentflowHead + "  a\n  class a " + p },
	func(p string) string { return agentflowHead + "  a\n  class " + p + " x" },
	func(p string) string { return agentflowHead + "  a\n  classDef " + p + " fill:red\n  class a " + p },
	func(p string) string { return agentflowHead + "  a\n  classDef c fill:" + p + "\n  class a c" },
	func(p string) string { return agentflowHead + "  a\n  classDef c " + p + ":red\n  class a c" },
	func(p string) string {
		return agentflowHead + "  a\n  classDef default fill:" + p + ",color:" + p + ",stroke:" + p + "\n"
	},
	func(p string) string { return agentflowHead + "  a\n  style a fill:" + p + "\n" },
	func(p string) string { return agentflowHead + "  a\n  style a " + p + ":red\n" },
	func(p string) string {
		return agentflowHead + "  a\n  style a color:" + p + ",stroke:" + p + ",font-family:" + p + ",font-size:" + p + "\n"
	},
	func(p string) string { return agentflowHead + "  a\n  style " + p + " fill:red\n" },
	func(p string) string {
		return agentflowHead + "  a --> b\n  linkStyle 0 stroke:" + p + ",fill:" + p + "\n"
	},
	func(p string) string { return agentflowHead + "  a --> b\n  linkStyle default " + p + ":" + p + "\n" },
	func(p string) string { return agentflowHead + "  a --> b\n  linkStyle 0 interpolate " + p + "\n" },
	func(p string) string {
		return agentflowHead + "  a --> b\n  linkStyle default interpolate " + p + " stroke:" + p + "\n"
	},
	func(p string) string { return agentflowHead + "  a\n  click a \"" + p + "\"\n" },
	func(p string) string { return agentflowHead + "  a\n  click a href \"" + p + "\"\n" },
	func(p string) string {
		return agentflowHead + "  a\n  click a href \"" + p + "\" \"" + p + "\" _blank\n"
	},
	func(p string) string { return agentflowHead + "  a\n  click a \"https://example.com\" \"" + p + "\"\n" },
	func(p string) string { return agentflowHead + "  a\n  click a call " + p + "()\n" },
	func(p string) string { return agentflowHead + "  a\n  click a call cb(\"" + p + "\") \"" + p + "\"\n" },
	func(p string) string { return agentflowHead + "  a\n  click a " + p + "\n" },
	func(p string) string { return agentflowHead + "  a\n  click " + p + " href \"https://example.com\"\n" },
	func(p string) string { return agentflowHead + "  a[\"" + p + "\"]:::internal-link" },
	func(p string) string { return agentflowHead + "  " + p + ":::internal-link" },
	func(p string) string { return agentflowHead + "  accTitle: " + p + "\n  accDescr: " + p + "\n  a" },
	func(p string) string { return agentflowHead + "  accDescr {\n " + p + "\n }\n  a" },
	func(p string) string { return "agentflow-beta " + p + "\n  a" },
	func(p string) string { return agentflowHead + "  direction " + p + "\n  a" },
	func(p string) string {
		return agentflowHead + "  %% " + p + "\n  a %% " + p + "\n  b[x %% " + p + "\n y]"
	},
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\nconfig:\n  agentflow:\n    nodeSpacing: \"" + p + "\"\n    rankSpacing: \"" + p + "\"\n    wrappingWidth: \"" + p + "\"\n  flowchart:\n    curve: \"" + p + "\"\n    inheritDir: \"" + p + "\"\n---\n" + agentflowHead + "  a --> b"
	},
	func(p string) string { return "---\ntitle: " + p + "\n---\n" + agentflowHead + "  a" },
	func(p string) string {
		return "%%{init: {\"agentflow\": {\"nodeSpacing\": \"" + p + "\"}, \"flowchart\": {\"curve\": \"" + p + "\"}, \"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\n" + agentflowHead + "  a --> b"
	},
}
