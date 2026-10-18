package nilda

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestLinkTargetValidateMatchesTheRules holds Validate to the rules Core applies to a link target before it
// offers one: the key pattern, the counts, and the path rules. An author who runs it at Init — with the
// route prefix Core gave them — hears the same words Core would otherwise say only in ITS log, on a site
// the author cannot see.
func TestLinkTargetValidateMatchesTheRules(t *testing.T) {
	t.Parallel()
	params := func(n int) []LinkParam {
		out := make([]LinkParam, 0, n)
		for i := range n {
			out = append(out, LinkParam{Key: "p" + strconv.Itoa(i), Label: "P", Type: LinkParamSelect})
		}
		return out
	}
	choices := func(n int) []LinkChoice {
		out := make([]LinkChoice, 0, n)
		for i := range n {
			out = append(out, LinkChoice{Value: "v" + strconv.Itoa(i), Label: "V"})
		}
		return out
	}
	good := LinkTarget{
		Key: "book", Label: "Book", Group: "Booking", Path: "/booking/go",
		Params: []LinkParam{
			{Key: "setup", Label: "What to book", Type: LinkParamSelect,
				Choices: []LinkChoice{{Value: "dinner", Label: "Dinner"}}},
			{Key: "party", Label: "Guests", Type: LinkParamNumber},
		},
	}

	for _, c := range []struct {
		name   string
		target LinkTarget
		prefix string
		want   string // a word the refusal must carry; "" = valid
	}{
		{"good", good, "/booking", ""},
		{"the path may be the prefix itself", LinkTarget{Key: "hub", Label: "Booking", Path: "/booking"}, "/booking", ""},
		{"four params is the bound", LinkTarget{Key: "book", Label: "Book", Path: "/booking/go", Params: params(4)}, "/booking", ""},
		{"a hundred choices is the bound", LinkTarget{Key: "book", Label: "Book", Path: "/booking/go",
			Params: []LinkParam{{Key: "setup", Label: "S", Type: LinkParamSelect, Choices: choices(100)}}}, "/booking", ""},
		{"path outside the prefix", good, "/shop", "route prefix"},
		{"a query in the path", LinkTarget{Key: "book", Label: "Book", Path: "/booking/go?setup=dinner"}, "/booking", "query"},
		{"a fragment in the path", LinkTarget{Key: "book", Label: "Book", Path: "/booking/go#book"}, "/booking", "fragment"},
		{"a double slash in the path", LinkTarget{Key: "book", Label: "Book", Path: "/booking//go"}, "/booking", "double slash"},
		{"a parent step in the path", LinkTarget{Key: "book", Label: "Book", Path: "/booking/../go"}, "/booking", "parent"},
		{"a path that is not root-relative", LinkTarget{Key: "book", Label: "Book", Path: "booking/go"}, "/booking", "root-relative"},
		{"five params", LinkTarget{Key: "book", Label: "Book", Path: "/booking/go", Params: params(5)}, "/booking", "params"},
		{"a hundred and one choices", LinkTarget{Key: "book", Label: "Book", Path: "/booking/go",
			Params: []LinkParam{{Key: "setup", Label: "S", Type: LinkParamSelect, Choices: choices(101)}}}, "/booking", "choices"},
		{"a bad key", LinkTarget{Key: "Book", Label: "Book", Path: "/booking/go"}, "/booking", "key"},
		{"a bad param key", LinkTarget{Key: "book", Label: "Book", Path: "/booking/go",
			Params: []LinkParam{{Key: "Party", Label: "Guests", Type: LinkParamNumber}}}, "/booking", "key"},
		{"a bad param type", LinkTarget{Key: "book", Label: "Book", Path: "/booking/go",
			Params: []LinkParam{{Key: "party", Label: "Guests", Type: "text"}}}, "/booking", "type"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.target.Validate(c.prefix)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("Validate(%q) = %v; want nil", c.prefix, err)
			case c.want != "" && err == nil:
				t.Fatalf("Validate(%q) = nil; want a refusal naming the %s", c.prefix, c.want)
			case c.want != "" && !strings.Contains(err.Error(), c.want):
				t.Fatalf("Validate(%q) = %q; the refusal does not name the %s", c.prefix, err, c.want)
			}
		})
	}
}

// linkTargetOnly implements the plain Handler and LinkTargetProvider, which is the shape Serve has to
// subscribe from.
type linkTargetOnly struct{ plainOnly }

func (linkTargetOnly) LinkTargets(context.Context, LinkTargetsRequest) ([]LinkTarget, error) {
	return nil, nil
}

// TestServeSubscribesLinkTargets — Core only delivers a hook a plugin SUBSCRIBED to, by name, before any
// capability is consulted. Implementing the interface without a providerHooks row is a plugin that runs and
// is never asked where its links lead.
func TestServeSubscribesLinkTargets(t *testing.T) {
	t.Parallel()
	if got := withProvidedHooks(linkTargetOnly{}, nil); !slices.Contains(got, LinkTargetsHook) {
		t.Fatalf("withProvidedHooks gave %v; a plugin implementing LinkTargetProvider must subscribe %q",
			got, LinkTargetsHook)
	}
	out, err := (&pluginServer{handler: linkTargetOnly{}}).wire(InitResult{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(out.Hooks, LinkTargetsHook) {
		t.Fatalf("Serve wired %v; the plugin is never asked where its links lead", out.Hooks)
	}
}

// TestDispatchLinkTargetsRoundTrip is the wire: the request Core sends reaches the provider, the answer is
// the envelope Core reads — "targets" and nothing else — and the JSON keys are exactly the ones in the
// design, written out rather than round-tripped (a rename on both sides would round-trip green).
//
// It also holds the dispatch to what it does with a bad target: it is DROPPED and logged, never failed —
// the good targets in the same answer still reach the page builder.
func TestDispatchLinkTargetsRoundTrip(t *testing.T) {
	t.Parallel()
	var got LinkTargetsRequest
	one, fifty := 1.0, 50.0
	good := LinkTarget{
		Key: "book", Label: "Book", Group: "Booking", Path: "/booking/go",
		Params: []LinkParam{
			{Key: "setup", Label: "What to book", Type: LinkParamSelect,
				Choices: []LinkChoice{{Value: "", Label: "Let the visitor choose"}, {Value: "dinner", Label: "Dinner"}}},
			{Key: "party", Label: "Guests", Type: LinkParamNumber, Min: &one, Max: &fifty},
		},
	}
	bad := LinkTarget{Key: "Book", Label: "Book", Path: "/booking/go"} // an uppercase key: Core would drop it

	out, ok, err := DispatchLinkTargets(context.Background(), LinkTargetsHook, []byte(`{"locale":"fa"}`),
		func(_ context.Context, req LinkTargetsRequest) ([]LinkTarget, error) {
			got = req
			return []LinkTarget{good, bad}, nil
		})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.Locale != "fa" {
		t.Fatalf("the request did not reach the provider: %+v", got)
	}
	const want = `{"targets":[{"key":"book","label":"Book","group":"Booking","path":"/booking/go",` +
		`"params":[{"key":"setup","label":"What to book","type":"select",` +
		`"choices":[{"value":"","label":"Let the visitor choose"},{"value":"dinner","label":"Dinner"}]},` +
		`{"key":"party","label":"Guests","type":"number","min":1,"max":50}]}]}`
	if string(out) != want {
		t.Fatalf("the answer is\n  %s\nwant\n  %s", out, want)
	}

	// An unrelated hook is not this dispatch's, and a provider that is absent fails the call rather than
	// answering null.
	if _, handled, err := DispatchLinkTargets(context.Background(), "content.saved", []byte(`{}`), nil); handled || err != nil {
		t.Fatalf("content.saved: handled=%v err=%v", handled, err)
	}
	if _, handled, err := DispatchLinkTargets(context.Background(), LinkTargetsHook, []byte(`{}`), nil); !handled || err == nil {
		t.Fatalf("no handler: handled=%v err=%v; want a refusal", handled, err)
	}

	// The automatic path, through the interface: dispatchProvided answers with the same envelope.
	out, handled, err := dispatchProvided(context.Background(), linkTargetOnly{}, LinkTargetsHook, []byte(`{}`))
	if err != nil || !handled {
		t.Fatalf("dispatchProvided: handled=%v err=%v", handled, err)
	}
	var res struct {
		Targets []LinkTarget `json:"targets"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if res.Targets == nil || len(res.Targets) != 0 {
		t.Fatalf("a provider offering nothing must answer [], got %s", out)
	}
}
