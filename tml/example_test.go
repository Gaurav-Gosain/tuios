package tml_test

import (
	"fmt"

	"tuios.dev/tml"
)

func ExampleCheck() {
	src := []byte(`<tml version="1" title="Seconds">
  <state>{"secs": [41, 39, 44]}</state>
  <chart-bar data="state.secs" h="4"/>
</tml>`)
	_, problems, err := tml.Check(src, nil)
	fmt.Println(err != nil)
	for _, p := range problems {
		fmt.Println(p)
	}
	// Output:
	// true
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
	store, _ := tml.StoreFor(doc, tml.DefaultLimits())
	deps := tml.BuildDeps(doc)

	ops, _ := tml.DecodePatch([]byte(`[{"op":"replace","path":"/pct","value":80}]`), tml.DefaultLimits())
	changed, _ := store.Apply(ops)
	fmt.Println(deps.Affected(changed))

	bar := doc.At("/children/0/children/1")
	v, _ := bar.Attr("value")
	tmpl, _ := tml.ParseTemplate(v.Text())
	value, _ := tmpl.Eval(&tml.Env{Vars: map[string]any{"state": store.State()}, Budget: tml.NewBudget()})
	fmt.Println(value)
	// Output:
	// [/children/0/children/1]
	// 0.8
}
