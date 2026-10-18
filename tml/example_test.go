package tml_test

import (
	"fmt"

	"tuios.dev/tml"
)

// The README shows the bodies of these examples, and TestReadme keeps the
// two the same, so the README code compiles.

func ExampleCheck() {
	src := []byte(`<tml version="1" title="Seconds">
  <state>{"secs": [41, 39, 44]}</state>
  <chart-bar data="state.secs" h="4"/>
</tml>`)

	_, problems, err := tml.Check(src, nil)
	if err != nil {
		for _, p := range problems {
			fmt.Println(p)
		}
	}
	// Output:
	// 3:3: <chart-bar> is not a TML component. (unknown_component at /children/0) Did you mean "bar"?
}

func Example() {
	src := []byte(`<tml version="1" title="Build">
  <state>{"pct": 40, "step": "Compiling"}</state>
  <card title="Build">
    <text>Step: <b>{state.step}</b></text>
    <progress value="{state.pct / 100}"/>
  </card>
</tml>`)

	doc, _, err := tml.Check(src, nil)
	if err != nil {
		panic(err)
	}
	store, err := tml.StoreFor(doc, nil)
	if err != nil {
		panic(err)
	}

	// The owner sends a patch. Apply returns the state paths it changed.
	ops, err := tml.DecodePatch([]byte(`[{"op":"replace","path":"/pct","value":80}]`), nil)
	if err != nil {
		panic(err)
	}
	changed, err := store.Apply(ops)
	if err != nil {
		panic(err)
	}
	fmt.Println(changed)

	// A renderer evaluates the progress bar's binding with a budget.
	bar := doc.At("/children/0/children/1")
	v, _ := bar.Attr("value")
	tmpl, err := tml.ParseTemplate(v.Text())
	if err != nil {
		panic(err)
	}
	value, err := tmpl.Eval(&tml.Env{Vars: map[string]any{"state": store.State()}, Budget: tml.NewBudget()})
	fmt.Println(value, err)
	// Output:
	// [/pct]
	// 0.8 <nil>
}
