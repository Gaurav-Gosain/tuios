package applist

import (
	"slices"
	"testing"
)

func TestExecTokenizing(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"plain", `firefox`, []string{"firefox"}},
		{"args", `code-oss --new-window`, []string{"code-oss", "--new-window"}},
		{"file code dropped", `gimp %F`, []string{"gimp"}},
		{"url code dropped", `firefox %u`, []string{"firefox"}},
		{"quoted path with space", `"/opt/My App/run" --flag`, []string{"/opt/My App/run", "--flag"}},
		{"escaped quote inside quotes", `sh "a\"b"`, []string{"sh", `a"b`}},
		{"escaped dollar inside quotes", `x "a\$b"`, []string{"x", `a$b`}},
		{"escaped backslash inside quotes", `x "a\\b"`, []string{"x", `a\b`}},
		{"percent literal", `x 100%%`, []string{"x", "100%"}},
		{"deprecated removed", `x %d %D %n %N %v %m y`, []string{"x", "y"}},
		{"unknown code removed", `x --flag=%z y`, []string{"x", "--flag=", "y"}},
		{"embedded code in arg", `x --file=%f`, []string{"x", "--file="}},
		{"trailing lone percent", `x abc%`, []string{"x", "abc"}},
		{"real world quoted arg with url template",
			`kde-geo-uri-handler --t "https://x/?a=<A>&b=<B>" %u`,
			[]string{"kde-geo-uri-handler", "--t", "https://x/?a=<A>&b=<B>"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseExec(c.in, "/p/x.desktop", "Name", "")
			if err != nil {
				t.Fatalf("parseExec(%q): %v", c.in, err)
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("parseExec(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	// Refused: an unterminated quote, and an Exec with no program to run,
	// whether it is blank or only a removed field code.
	for _, in := range []string{`app "unclosed`, `%f`, ``} {
		if got, err := parseExec(in, "/p/x.desktop", "n", ""); err == nil {
			t.Errorf("parseExec(%q) = %q, want an error", in, got)
		}
	}
}

func TestExecFieldCodes(t *testing.T) {
	// %i expands to two arguments, or to none when there is no Icon.
	got, err := parseExec(`app %i --x`, "/p/x.desktop", "The Name", "myicon")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"app", "--icon", "myicon", "--x"}; !slices.Equal(got, want) {
		t.Fatalf("%%i with icon = %q, want %q", got, want)
	}

	got, err = parseExec(`app %i --x`, "/p/x.desktop", "The Name", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"app", "--x"}; !slices.Equal(got, want) {
		t.Fatalf("%%i without icon = %q, want %q", got, want)
	}

	// %c is the translated name, %k the file location.
	got, err = parseExec(`app %c %k`, "/p/x.desktop", "The Name", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"app", "The Name", "/p/x.desktop"}; !slices.Equal(got, want) {
		t.Fatalf("%%c %%k = %q, want %q", got, want)
	}
}

// TestExecIsNotAShell is the security property: an Exec value is tokenized per
// the spec and exec'd as argv, so shell metacharacters have no meaning. A
// .desktop file can be dropped anywhere on the XDG search path, so this is the
// difference between a launcher and a remote-code-execution primitive.
func TestExecIsNotAShell(t *testing.T) {
	argv, err := parseExec(`app; rm -rf ~ | tee /dev/null & echo $(whoami)`,
		"/p/x.desktop", "n", "")
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != "app;" {
		t.Fatalf("expected the semicolon to stay glued to the literal token, got %q", argv)
	}
	// The metacharacters survive as literal argv elements, which is the point:
	// nothing downstream will ever interpret them.
	if !slices.Contains(argv, "|") || !slices.Contains(argv, "&") {
		t.Fatalf("expected the reserved characters to survive as plain arguments, got %q", argv)
	}
	for _, a := range argv {
		if a == "sh" || a == "/bin/sh" || a == "bash" {
			t.Fatalf("a shell appeared in argv: %q", argv)
		}
	}
}
