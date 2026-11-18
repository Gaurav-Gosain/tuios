package config

import (
	"flag"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var updateDocs = flag.Bool("update", false, "write docs/config-options.md again from the option registry")

const configOptionsDoc = "../../docs/config-options.md"

// TestConfigOptionsDocMatchesRegistry holds docs/config-options.md to the
// option registry, the table tuios list-options, set-option and the settings
// page read. The page is generated, so an option added, renamed, removed or
// given a new default fails here until the page is written again with -update.
//
// Some defaults differ on macOS, so the page is the Linux one and the test
// runs on Linux only.
func TestConfigOptionsDocMatchesRegistry(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the page shows the Linux defaults")
	}
	want := renderConfigOptionsDoc()
	if *updateDocs {
		if err := os.WriteFile(configOptionsDoc, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(configOptionsDoc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("docs/config-options.md does not match the option registry. Run: go test ./internal/config -run TestConfigOptionsDocMatchesRegistry -update")
	}
}

// TestConfigurationTOMLKeysExist reads every TOML example in
// docs/CONFIGURATION.md and fails on a table or a key that UserConfig does
// not have, commented out or not. An example that names a removed or renamed
// key is one a person copies into a config that tuios then ignores.
func TestConfigurationTOMLKeysExist(t *testing.T) {
	data, err := os.ReadFile("../../docs/CONFIGURATION.md")
	if err != nil {
		t.Fatal(err)
	}
	header := regexp.MustCompile(`^#?\s*\[\[?([a-z0-9_."*]+)\]\]?\s*(#.*)?$`)
	key := regexp.MustCompile(`^#?\s*([a-z][a-z0-9_]*)\s*=`)
	var table []string
	inTOML, fenced := false, false
	checked := 0
	for i, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			inTOML = fenced && strings.HasPrefix(line, "```toml")
			table = nil
			continue
		}
		if !inTOML {
			continue
		}
		text := strings.TrimSpace(line)
		if m := header.FindStringSubmatch(text); m != nil {
			table = tomlPath(m[1])
			if !configPathExists(table) {
				t.Errorf("CONFIGURATION.md:%d: no table [%s] in the config", i+1, m[1])
			}
			continue
		}
		if m := key.FindStringSubmatch(text); m != nil {
			checked++
			path := append(slices.Clone(table), m[1])
			if !configPathExists(path) {
				t.Errorf("CONFIGURATION.md:%d: no key %s in the config: %s", i+1, strings.Join(path, "."), text)
			}
		}
	}
	if checked < 100 {
		t.Fatalf("found %d keys in the TOML examples, want the whole page", checked)
	}
}

// TestConfigurationDefaultsMatch fails on a default that docs/CONFIGURATION.md
// states and the option registry does not have. It reads the forms the page
// uses: "# key = v  # d, the default" and "key = d  # the default" in a TOML
// example, and "`key` is `d` by default" and "`key` (default d)" in the
// text. For an option whose 0 means "use the default", the number its
// description gives for 0 counts too.
func TestConfigurationDefaultsMatch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the page gives the Linux defaults")
	}
	data, err := os.ReadFile("../../docs/CONFIGURATION.md")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Option{}
	byLeaf := map[string][]Option{}
	for _, o := range Options() {
		byPath[o.Path] = o
		leaf := o.Path[strings.LastIndex(o.Path, ".")+1:]
		byLeaf[leaf] = append(byLeaf[leaf], o)
	}
	resolve := func(table, key string) (Option, bool) {
		if o, ok := byPath[key]; ok {
			return o, true
		}
		if o, ok := byPath[table+"."+key]; ok && table != "" {
			return o, true
		}
		if l := byLeaf[key]; len(l) == 1 {
			return l[0], true
		}
		return Option{}, false
	}
	zeroUses := regexp.MustCompile(`\b0 uses (?:the default, )?(\S+?)[.,]`)
	matches := func(o Option, claim string) bool {
		if claim == o.Default {
			return true
		}
		if o.Default == "0" || o.Default == "" {
			if m := zeroUses.FindStringSubmatch(o.Description); m != nil && m[1] == claim {
				return true
			}
		}
		return false
	}
	header := regexp.MustCompile(`^#?\s*\[([a-z0-9_.]+)\]\s*(#.*)?$`)
	commented := regexp.MustCompile(`^#\s*([a-z][a-z0-9_]*)\s*=\s*\S+.*#\s*"?([^",#]+?)"?,? the default\s*$`)
	set := regexp.MustCompile(`^([a-z][a-z0-9_]*)\s*=\s*"?([^"#]*?)"?\s*#\s*the default\b`)
	byDefault := regexp.MustCompile("`([a-z][a-z0-9_.]*)` is `\"?([^`\"]+)\"?` by default")
	paren := regexp.MustCompile("`([a-z][a-z0-9_.]*)` \\(default `?\"?([^`\")]+)\"?`?\\)")
	checked := 0
	check := func(line int, table, key, claim string) {
		o, ok := resolve(table, key)
		if !ok {
			return
		}
		checked++
		if claim = strings.TrimSpace(claim); !matches(o, claim) {
			t.Errorf("CONFIGURATION.md:%d: %s: the page says the default is %q, the code has %q", line, o.Path, claim, o.Default)
		}
	}
	table := ""
	inTOML, fenced := false, false
	for i, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			inTOML = fenced && strings.HasPrefix(line, "```toml")
			table = ""
			continue
		}
		text := strings.TrimSpace(line)
		switch {
		case inTOML:
			if m := header.FindStringSubmatch(text); m != nil {
				table = m[1]
			} else if m := commented.FindStringSubmatch(text); m != nil {
				check(i+1, table, m[1], m[2])
			} else if m := set.FindStringSubmatch(text); m != nil {
				check(i+1, table, m[1], m[2])
			}
		case !fenced:
			for _, m := range byDefault.FindAllStringSubmatch(line, -1) {
				check(i+1, "", m[1], m[2])
			}
			for _, m := range paren.FindAllStringSubmatch(line, -1) {
				check(i+1, "", m[1], m[2])
			}
		}
	}
	if checked < 10 {
		t.Fatalf("found %d default claims, want the ones the page makes", checked)
	}
}

// tomlPath splits a TOML table name into its keys, without the quotes of a
// quoted key such as hosts."*".
func tomlPath(name string) []string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = strings.Trim(p, `"`)
	}
	return parts
}

// configPathExists reports whether path names a table or a key of
// UserConfig. A map takes any key, and a key under a value that is not a
// table, such as an inline table, is not checked.
func configPathExists(path []string) bool {
	t := reflect.TypeFor[UserConfig]()
	for _, p := range path {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
			t = t.Elem()
		}
		switch t.Kind() {
		case reflect.Map:
			t = t.Elem()
			continue
		case reflect.Struct:
		default:
			return true
		}
		f, ok := tomlField(t, p)
		if !ok {
			return false
		}
		t = f
	}
	return true
}

// tomlField finds the field of struct t whose toml name is name, inside
// embedded structs too.
func tomlField(t reflect.Type, name string) (reflect.Type, bool) {
	for f := range t.Fields() {
		tag, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		if tag == "-" {
			continue
		}
		if f.Anonymous && tag == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if found, ok := tomlField(ft, name); ok {
				return found, true
			}
			continue
		}
		if tag == name {
			return f.Type, true
		}
	}
	return nil, false
}

// renderConfigOptionsDoc writes the option reference from the registry, one
// table per section in the order the registry declares them.
func renderConfigOptionsDoc() string {
	var b strings.Builder
	w := func(parts ...string) {
		for _, p := range parts {
			b.WriteString(p)
		}
	}
	w("# Option reference\n\n",
		"Do not edit this file by hand. A test writes it from the option registry, the same table that `tuios list-options` prints.\n",
		"To write it again, run `go test ./internal/config -run TestConfigOptionsDocMatchesRegistry -update`.\n\n",
		"Each option is a key in `config.toml`. `tuios set-config` and the settings page change the same options.\n",
		"[CONFIGURATION.md](CONFIGURATION.md) tells more about the options that need it.\n",
		"The defaults are the ones on Linux. Some defaults are different on macOS.\n")
	var sections []string
	bySection := map[string][]Option{}
	for _, o := range Options() {
		if _, ok := bySection[o.Section]; !ok {
			sections = append(sections, o.Section)
		}
		bySection[o.Section] = append(bySection[o.Section], o)
	}
	for _, s := range sections {
		w("\n## ", s, "\n\n| Option | Type | Default | Description |\n|---|---|---|---|\n")
		for _, o := range bySection[s] {
			w("| <a id=\"", o.Path, "\"></a>`", o.Path, "` | ", o.Type, " | ", optionDefaultCell(o), " | ", optionText(o), " |\n")
		}
	}
	w("\n## Keys set in the file only\n\n",
		"These keys are read from `config.toml` only. `tuios set-config` and the settings page cannot change them.\n",
		"NAME stands for a name that you choose, such as the name of a host.\n",
		"The description of a table names its keys.\n")
	sections = nil
	byFileSection := map[string][]FileKey{}
	for _, k := range FileKeys() {
		if _, ok := byFileSection[k.Section]; !ok {
			sections = append(sections, k.Section)
		}
		byFileSection[k.Section] = append(byFileSection[k.Section], k)
	}
	for _, s := range sections {
		w("\n### ", s, "\n\n| Key | Type | Default | Description |\n|---|---|---|---|\n")
		for _, k := range byFileSection[s] {
			w("| <a id=\"", k.Path, "\"></a>`", k.Path, "` | ", k.Type, " | ", fileKeyDefaultCell(k), " | ", fileKeyText(k), " |\n")
		}
	}
	return b.String()
}

// optionDefaultCell is an option's default as the page shows it.
func optionDefaultCell(o Option) string {
	switch {
	case o.Follows != "" && o.Default == "":
		return "follows `" + o.Follows + "`"
	case o.Default == "":
		return "empty"
	}
	return "`" + docCell(o.Default) + "`"
}

// optionText is an option's description with its values, its environment
// variable, its flag and why it is deprecated.
func optionText(o Option) string {
	parts := []string{docCell(o.Description)}
	if len(o.Accepted) > 0 && !o.Color {
		vals := make([]string, len(o.Accepted))
		for i, v := range o.Accepted {
			if v == "" {
				v = "(empty)"
			}
			vals[i] = "`" + docCell(v) + "`"
		}
		parts = append(parts, "Values: "+strings.Join(vals, ", ")+".")
	}
	if o.Max != 0 {
		parts = append(parts, fmt.Sprintf("Range: %d to %d.", o.Min, o.Max))
	}
	if o.Env != "" {
		parts = append(parts, "The `"+o.Env+"` environment variable sets it too, and wins over the file.")
	}
	if o.Flag != "" {
		parts = append(parts, "`--"+o.Flag+"` sets it for one run.")
	}
	if o.Deprecated != "" {
		parts = append(parts, "Deprecated: "+docCell(o.Deprecated))
	}
	return strings.Join(parts, " ")
}

// fileKeyWords are the defaults written in words, not as a value.
var fileKeyWords = []string{"none", "required", "built in", "not set", "every stage", "the receive folder", "the systems that run tuios"}

// fileKeyDefaultCell is a file key's default as the page shows it.
func fileKeyDefaultCell(k FileKey) string {
	switch {
	case k.Default == "":
		return "empty"
	case slices.Contains(fileKeyWords, k.Default):
		return k.Default
	}
	return "`" + docCell(k.Default) + "`"
}

// fileKeyText is a file key's description with its values.
func fileKeyText(k FileKey) string {
	text := docCell(k.Description)
	if len(k.Accepted) > 0 {
		vals := make([]string, len(k.Accepted))
		for i, v := range k.Accepted {
			vals[i] = "`" + docCell(v) + "`"
		}
		text += " Values: " + strings.Join(vals, ", ") + "."
	}
	return text
}

// docCell makes s safe inside a table cell.
func docCell(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", "\\|")
}
