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
// FormatXML, FormatJSON, Format), validates a tree against the catalog with
// errors that name the fix (Validate, Check), generates the JSON Schema of
// the JSON encoding (Schema), evaluates bindings with a step budget
// (ParseTemplate, ParseExpr), parses actions (ParseActions), and applies
// RFC 6902 JSON Patch to the state (Store, DecodePatch). Each expression
// records the state paths it reads (Expr.Deps), so a renderer can redraw
// only what a patch touches.
//
// The host owns the frame. It gives every binding of one frame the same
// Budget; a nil Budget bounds each call alone. It makes the store of a
// document with StoreFor, so a patch cannot grow each, list or the rows
// past the limits. Every string and key that enters a store or a tree, or
// leaves an evaluation, has passed through Sanitize.
//
// It draws nothing, and it imports only the standard library. No code from a
// document runs: expressions compute values and cannot loop, assign or call
// out.
package tml
