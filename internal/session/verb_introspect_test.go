package session

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestListVerbsDescribesEveryVerb is the contract an agent relies on: list-verbs
// alone is enough to drive the control plane. Every registered verb must carry a
// description and a parameter schema, and the reply must carry the protocol
// range, the error-code catalog, and the envelope shape.
func TestListVerbsDescribesEveryVerb(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"list-verbs"}`))

	if res["version"] != float64(VerbProtocolVersion) {
		t.Errorf("version = %v, want %d", res["version"], VerbProtocolVersion)
	}
	if res["min_version"] != float64(MinVerbProtocolVersion) {
		t.Errorf("min_version = %v, want %d", res["min_version"], MinVerbProtocolVersion)
	}
	if res["daemon_version"] != "test" {
		t.Errorf("daemon_version = %v, want test", res["daemon_version"])
	}

	envelope, ok := res["envelope"].(map[string]any)
	if !ok {
		t.Fatal("reply carries no envelope documentation")
	}
	for _, field := range []string{"transport", "request", "success", "failure", "hint"} {
		if s, _ := envelope[field].(string); s == "" {
			t.Errorf("envelope documentation is missing %q", field)
		}
	}

	codes, ok := res["error_codes"].([]any)
	if !ok || len(codes) == 0 {
		t.Fatal("reply carries no error-code catalog")
	}
	documented := map[string]bool{}
	for _, entry := range codes {
		e, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("error-code entry is not an object: %v", entry)
		}
		code, _ := e["code"].(string)
		desc, _ := e["description"].(string)
		if code == "" || desc == "" {
			t.Errorf("error-code entry is incomplete: %v", e)
		}
		documented[code] = true
	}
	// Every code the daemon can actually emit must be documented, or an agent
	// meets a code it cannot interpret.
	for _, code := range []string{
		ErrVerbInvalidRequest, ErrVerbUnknownVerb, ErrVerbInvalidParams,
		ErrVerbSessionNotFound, ErrVerbWindowNotFound, ErrVerbNoWindows,
		ErrVerbPTYNotFound, ErrVerbNeedsClient, ErrVerbOptionNotFound,
		ErrVerbCommandFailed, ErrVerbTimeout, ErrVerbProtocolMismatch, ErrVerbInternal,
	} {
		if !documented[code] {
			t.Errorf("error code %q is emitted by the daemon but not documented by list-verbs", code)
		}
	}

	verbs, ok := res["verbs"].([]any)
	if !ok || len(verbs) != len(verbRegistry) {
		t.Fatalf("verbs list wrong: got %v entries, want %d", len(verbs), len(verbRegistry))
	}

	var names []string
	for _, entry := range verbs {
		v, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("verb entry is not an object: %v", entry)
		}
		name, _ := v["verb"].(string)
		names = append(names, name)

		if desc, _ := v["description"].(string); desc == "" {
			t.Errorf("verb %q has no description", name)
		}
		params, ok := v["params"].([]any)
		if !ok {
			t.Errorf("verb %q has no params list (it must be present even when empty)", name)
			continue
		}
		for _, raw := range params {
			p, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("verb %q has a non-object param: %v", name, raw)
			}
			pname, _ := p["name"].(string)
			ptype, _ := p["type"].(string)
			pdesc, _ := p["description"].(string)
			if pname == "" || ptype == "" || pdesc == "" {
				t.Errorf("verb %q has an incompletely documented param: %v", name, p)
			}
			if !slices.Contains([]string{"string", "int", "bool", "[]string"}, ptype) {
				t.Errorf("verb %q param %q has unknown type %q", name, pname, ptype)
			}
		}
	}

	if !slices.IsSorted(names) {
		t.Errorf("verbs are not in a stable sorted order: %v", names)
	}
}

// TestListVerbsExamplesAreValidRequests keeps the documented examples honest: an
// agent that copies one must get a well-formed request naming a real verb.
func TestListVerbsExamplesAreValidRequests(t *testing.T) {
	for name, entry := range verbRegistry {
		for _, example := range entry.examples {
			var req verbRequest
			if err := json.Unmarshal([]byte(example), &req); err != nil {
				t.Errorf("verb %q has an example that is not valid JSON: %s (%v)", name, example, err)
				continue
			}
			if req.Verb != name {
				t.Errorf("verb %q has an example for a different verb %q: %s", name, req.Verb, example)
			}
			if _, ok := verbRegistry[req.Verb]; !ok {
				t.Errorf("example names an unregistered verb %q: %s", req.Verb, example)
			}
		}
	}
}

// TestListVerbsAcceptedValuesMatchTheImplementation guards the one way a schema
// silently rots: the accepted-value lists must be the same lists the handlers
// actually enforce.
func TestListVerbsAcceptedValuesMatchTheImplementation(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"list-verbs","params":{"verb":"wait-for"}}`))
	verbs, ok := res["verbs"].([]any)
	if !ok || len(verbs) != 1 {
		t.Fatalf("naming a verb should return exactly that verb, got %v", res["verbs"])
	}
	doc := verbs[0].(map[string]any)
	if doc["verb"] != "wait-for" {
		t.Fatalf("described the wrong verb: %v", doc["verb"])
	}

	var accepted []string
	for _, raw := range doc["params"].([]any) {
		p := raw.(map[string]any)
		if p["name"] != "condition" {
			continue
		}
		for _, v := range p["accepted"].([]any) {
			accepted = append(accepted, v.(string))
		}
	}
	if !slices.Equal(accepted, waitConditions) {
		t.Fatalf("documented conditions %v do not match the implemented set %v", accepted, waitConditions)
	}

	// And each documented condition must actually be accepted by the handler:
	// a rejected one comes back as invalid_params naming the condition param.
	for _, condition := range accepted {
		resp := c.call(t, `{"id":2,"verb":"wait-for","params":{"condition":"`+condition+`","timeout":1}}`)
		e, ok := resp["error"].(map[string]any)
		if !ok {
			continue // it matched immediately, which also means it was accepted
		}
		if code, _ := e["code"].(string); code == ErrVerbInvalidParams {
			hint, _ := e["hint"].(map[string]any)
			if param, _ := hint["param"].(string); param == "condition" {
				t.Errorf("condition %q is documented as accepted but the handler rejects it: %v", condition, e)
			}
		}
	}
}

// TestCaptureSourcesMatchTheImplementation is the capture-pane half of the same
// contract: the documented source list must equal the enforced set, and every
// documented source must survive the handler's validation. This is what stops a
// source from being advertised while doing nothing, which is how
// "recent-unwrapped" became a silent alias for "recent".
func TestCaptureSourcesMatchTheImplementation(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"list-verbs","params":{"verb":"capture-pane"}}`))
	doc := res["verbs"].([]any)[0].(map[string]any)

	var accepted []string
	for _, raw := range doc["params"].([]any) {
		p := raw.(map[string]any)
		if p["name"] != "source" {
			continue
		}
		for _, v := range p["accepted"].([]any) {
			accepted = append(accepted, v.(string))
		}
	}
	if !slices.Equal(accepted, captureSources) {
		t.Fatalf("documented sources %v do not match the implemented set %v", accepted, captureSources)
	}

	// Each documented source must capture rather than be rejected, and must be
	// echoed back as the source that was actually used.
	for _, source := range accepted {
		res := result(t, c.call(t, `{"id":2,"verb":"capture-pane","params":{"session":"work","source":"`+source+`"}}`))
		if got, _ := res["source"].(string); got != source {
			t.Errorf("capture with source %q reported source %q", source, got)
		}
	}

	// A retired source must not merely be undocumented, it must be refused, so a
	// caller still passing it finds out instead of silently getting "recent".
	for retired := range retiredCaptureSources {
		if slices.Contains(captureSources, retired) {
			t.Errorf("%q is listed as both accepted and retired", retired)
		}
		resp := c.call(t, `{"id":3,"verb":"capture-pane","params":{"session":"work","source":"`+retired+`"}}`)
		e, ok := resp["error"].(map[string]any)
		if !ok {
			t.Errorf("retired source %q was accepted: %v", retired, resp)
			continue
		}
		if code, _ := e["code"].(string); code != ErrVerbInvalidParams {
			t.Errorf("retired source %q rejected with %q, want %q", retired, code, ErrVerbInvalidParams)
		}
	}
}

// TestListVerbsForUnknownVerbSuggestsOne checks the introspection verb is itself
// self-explaining when misused.
func TestListVerbsForUnknownVerbSuggestsOne(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	resp := c.call(t, `{"id":1,"verb":"list-verbs","params":{"verb":"capture-pain"}}`)
	e := errorOf(t, resp)
	if code, _ := e["code"].(string); code != ErrVerbUnknownVerb {
		t.Fatalf("code = %v, want %v", e["code"], ErrVerbUnknownVerb)
	}
	hint := hintOf(t, e)
	if got, _ := hint["did_you_mean"].(string); got != "capture-pane" {
		t.Errorf("did_you_mean = %q, want capture-pane", got)
	}
}

// TestEveryVerbIsDocumented is the guard that makes the schema a build-time
// obligation: adding a verb without documenting it fails here rather than
// silently shipping an undiscoverable verb.
func TestEveryVerbIsDocumented(t *testing.T) {
	for name, entry := range verbRegistry {
		if entry.description == "" {
			t.Errorf("verb %q has no description", name)
		}
		if !strings.HasSuffix(entry.description, ".") {
			t.Errorf("verb %q description should read as a sentence: %q", name, entry.description)
		}
		if entry.handler == nil {
			t.Errorf("verb %q has no handler", name)
		}
		if len(entry.examples) == 0 {
			t.Errorf("verb %q has no example request", name)
		}
	}
}
