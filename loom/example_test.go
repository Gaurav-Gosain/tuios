package loom_test

import (
	"fmt"

	"tuios.dev/loom"
	"tuios.dev/tml"
)

func Example() {
	src := []byte(`<tml version="1">
  <state>{"passed": 412, "failed": 3}</state>
  <row gap="3">
    <stat label="passed" value="{state.passed}" role="success"/>
    <stat label="failed" value="{state.failed}" role="error"/>
  </row>
</tml>`)
	doc, _, err := tml.Check(src, nil)
	if err != nil {
		panic(err)
	}
	s, err := loom.New(doc, &loom.Options{Palette: loom.DefaultPalette(loom.Color16)})
	if err != nil {
		panic(err)
	}
	fmt.Println(s.Render(30, 1).String())

	// A patch from the owner redraws only what reads the changed path.
	_ = s.Set("state.failed", 0)
	fmt.Println(s.Render(30, 1).String())
	// Output:
	// 412 passed   3 failed
	// 412 passed   0 failed
}

func ExampleSurface_Key() {
	doc, _, _ := tml.Check([]byte(`<tml version="1">
  <button key="r" on-press="emit('rerun', 3)">Rerun</button>
</tml>`), nil)
	s, _ := loom.New(doc, nil)
	s.Render(20, 1)
	res, _ := s.Key("r")
	e := res.Effects[0]
	fmt.Println(e.Action, e.Name, e.Value)
	// Output:
	// emit rerun 3
}
