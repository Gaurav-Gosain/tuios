// Package tml is the TML language: a markup for terminal surfaces that a
// program, a plugin or an agent sends to a host such as tuios, which draws it
// in a pane, a popup or a dock cell.
//
// A document is a tree of components from a fixed catalog, with a JSON state
// store. It has two encodings with a lossless mapping: TML text, a strict XML
// subset, for files and people, and TML JSON for the wire and for agents.
//
//	<tml version="1" title="Build">
//	  <state>{"pct": 40, "step": "Compiling"}</state>
//	  <card title="Build">
//	    <text>Step: <b>{state.step}</b></text>
//	    <progress value="{state.pct / 100}"/>
//	  </card>
//	</tml>
//
// The package parses and prints both encodings (ParseXML, ParseJSON,
// FormatXML, FormatJSON), validates a tree against the catalog with errors
// that name the fix (Validate, Check), generates the JSON Schema of the JSON
// encoding (Schema), evaluates bindings with a step budget (ParseTemplate,
// ParseExpr), runs the local actions (RunActions), applies RFC 6902 JSON
// Patch to the state (Store, ApplyPatch), and records which nodes read which
// state paths (BuildDeps), so a renderer redraws only what a patch touches.
//
// It draws nothing, and it imports only the standard library. No code from a
// document runs: expressions compute values and cannot loop, assign or call
// out.
package tml
