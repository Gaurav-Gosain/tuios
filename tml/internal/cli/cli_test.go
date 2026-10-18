package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"tuios.dev/tml"
)

func runCLI(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestCLI checks the exit codes and output the playground and agents read.
func TestCLI(t *testing.T) {
	code, out, _ := runCLI(t, "", "validate", "../../testdata/examples/dashboard.tml")
	if code != 0 || !strings.Contains(out, "valid") {
		t.Errorf("validate of a good document: %d %q", code, out)
	}
	code, out, _ = runCLI(t, "", "validate", "../../testdata/errors/chart-bar.tml")
	if code != 1 || !strings.Contains(out, "did you mean: bar") || !strings.Contains(out, "unknown_component at /children/0") {
		t.Errorf("validate of a bad document: %d %q", code, out)
	}
	code, out, _ = runCLI(t, `{"type":"tml","version":1,"children":[{"type":"bar"}]}`, "validate", "--json")
	if code != 1 || !strings.Contains(out, `"code":"missing_attribute"`) || !strings.Contains(out, `"valid":false`) {
		t.Errorf("validate --json: %d %q", code, out)
	}

	src, err := os.ReadFile("../../testdata/examples/deploy.tml")
	if err != nil {
		t.Fatal(err)
	}
	code, asJSON, _ := runCLI(t, string(src), "fmt", "--json")
	if code != 0 {
		t.Fatalf("fmt --json: %d", code)
	}
	code, back, _ := runCLI(t, asJSON, "fmt")
	want, _ := os.ReadFile("../../testdata/golden/deploy.json")
	if code != 0 || back != string(want) {
		t.Errorf("fmt keeps JSON as JSON:\n%s", back)
	}
	code, asXML, _ := runCLI(t, asJSON, "fmt", "--xml")
	want, _ = os.ReadFile("../../testdata/golden/deploy.tml")
	if code != 0 || asXML != string(want) {
		t.Errorf("fmt --xml:\n%s", asXML)
	}
	if code, _, errOut := runCLI(t, "<tml", "fmt"); code != 1 || !strings.Contains(errOut, "syntax") {
		t.Errorf("fmt of broken text: %d %q", code, errOut)
	}

	code, out, _ = runCLI(t, "", "schema")
	if code != 0 || out != string(tml.Schema()) {
		t.Errorf("schema: %d", code)
	}
	if code, _, _ := runCLI(t, "", "nope"); code != 2 {
		t.Errorf("an unknown command exits %d, want 2", code)
	}
}
