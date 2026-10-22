package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// guestBook is a holder the way a booking plugin is one: rows keyed by email, one kept for the accounts after it is
// anonymised and one held by an open dispute.
type guestBook struct {
	asked  []PrivacyRequest
	answer PrivacyExport
	erased PrivacyErasure
	err    error
}

func (g *guestBook) Init(context.Context, *Core) (InitResult, error)            { return InitResult{}, nil }
func (g *guestBook) HandleHook(context.Context, string, []byte) ([]byte, error) { return nil, nil }
func (g *guestBook) HandleEvent(context.Context, string, []byte) error          { return nil }

func (g *guestBook) ExportPersonalData(_ context.Context, req PrivacyRequest) (PrivacyExport, error) {
	g.asked = append(g.asked, req)
	return g.answer, g.err
}

func (g *guestBook) ErasePersonalData(_ context.Context, req PrivacyRequest) (PrivacyErasure, error) {
	g.asked = append(g.asked, req)
	return g.erased, g.err
}

// Implementing the interface is the whole job: Serve subscribes both hooks and answers them, the way a payment
// gateway's are answered — a holder subscribed and then failing every request is a site that cannot honour an
// erasure and does not know it.
func TestAPersonalDataHolderIsSubscribedAndAnswered(t *testing.T) {
	g := &guestBook{answer: PrivacyExport{Records: []PrivacyRecord{{Kind: "booking", ID: "b1",
		Email: "Guest@Example.com", Data: map[string]any{"name": "Lena"}}}}}
	hooks := withProvidedHooks(g, nil)
	for _, want := range []string{HookPrivacyExport, HookPrivacyErase} {
		if !slices.Contains(hooks, want) {
			t.Errorf("a PersonalDataHolder is not subscribed to %s: %v", want, hooks)
		}
	}
	out, handled, err := dispatchProvided(context.Background(), g, HookPrivacyExport,
		[]byte(`{"emails":["guest@example.com"],"request_id":"r1"}`))
	if !handled || err != nil {
		t.Fatalf("privacy.export: handled=%v err=%v", handled, err)
	}
	var got PrivacyExport
	if err := json.Unmarshal(out, &got); err != nil || len(got.Records) != 1 || got.Records[0].ID != "b1" {
		t.Fatalf("the answer is not the holder's records: %s (%v)", out, err)
	}
	if len(g.asked) != 1 || g.asked[0].RequestID != "r1" || !g.asked[0].Asked("GUEST@example.com ") {
		t.Errorf("the holder was not handed the request Core sent: %+v", g.asked)
	}

	g.erased = PrivacyErasure{Erased: 1}
	out, handled, err = dispatchProvided(context.Background(), g, HookPrivacyErase,
		[]byte(`{"emails":["guest@example.com"],"request_id":"r2"}`))
	if !handled || err != nil || !strings.Contains(string(out), `"erased":1`) {
		t.Fatalf("privacy.erase: handled=%v err=%v out=%s", handled, err, out)
	}
}

// One record about an address nobody asked about makes the whole answer wrong: it is somebody else's data on its way
// into a stranger's archive. The SDK refuses to send it, so a holder learns in its own tests rather than from Core.
func TestAHolderAnswerAboutAnotherAddressIsRefused(t *testing.T) {
	req := PrivacyRequest{Emails: []string{"guest@example.com"}}
	bad := PrivacyExport{Records: []PrivacyRecord{
		{Kind: "booking", ID: "b1", Email: "guest@example.com", Data: map[string]any{}},
		{Kind: "booking", ID: "b2", Email: "neighbour@example.com", Data: map[string]any{}},
	}}
	if err := bad.Validate(req); err == nil || !strings.Contains(err.Error(), "not asked about") {
		t.Fatalf("a record about another address passed: %v", err)
	}
	g := &guestBook{answer: bad}
	_, handled, err := dispatchProvided(context.Background(), g, HookPrivacyExport,
		[]byte(`{"emails":["guest@example.com"],"request_id":"r1"}`))
	if !handled || err == nil {
		t.Fatalf("the SDK sent an answer about another address: handled=%v err=%v", handled, err)
	}

	// No id, no kind, or no address: the record cannot be matched to what erase later says it kept or held.
	for name, r := range map[string]PrivacyRecord{
		"no id":    {Kind: "booking", Email: "guest@example.com"},
		"no kind":  {ID: "b1", Email: "guest@example.com"},
		"no email": {Kind: "booking", ID: "b1"},
	} {
		if err := (PrivacyExport{Records: []PrivacyRecord{r}}).Validate(req); err == nil {
			t.Errorf("%s: a record Core cannot match passed", name)
		}
	}
	tooMany := PrivacyExport{Records: make([]PrivacyRecord, MaxPrivacyRecords+1)}
	for i := range tooMany.Records {
		tooMany.Records[i] = PrivacyRecord{Kind: "booking", ID: "b", Email: "guest@example.com"}
	}
	if err := tooMany.Validate(req); err == nil {
		t.Errorf("an answer over MaxPrivacyRecords passed")
	}
}

// What an erasure kept or held must say on what basis — the two Art. 17(3) grounds the contract names — or the owner
// is told "kept" with no reason they could repeat to the person who asked.
func TestAnErasureMustSayWhyItKeptOrHeld(t *testing.T) {
	ok := PrivacyErasure{Erased: 2,
		Kept: []PrivacyKept{{Kind: "booking", ID: "b3", Fields: []string{"amount", "currency", "paid_at"},
			Basis: PrivacyBasisLegalObligation, Note: "BAO §132: kept seven years"}},
		Held: []PrivacyHeld{{Kind: "booking", ID: "b4", Basis: PrivacyBasisLegalClaims, Note: "a chargeback is open"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a well-formed erasure was refused: %v", err)
	}
	for name, e := range map[string]PrivacyErasure{
		"kept with no basis":  {Kept: []PrivacyKept{{Kind: "booking", ID: "b3", Fields: []string{"amount"}}}},
		"held with no basis":  {Held: []PrivacyHeld{{Kind: "booking", ID: "b4"}}},
		"an invented basis":   {Held: []PrivacyHeld{{Kind: "booking", ID: "b4", Basis: "because"}}},
		"kept naming nothing": {Kept: []PrivacyKept{{Kind: "booking", ID: "b3", Basis: PrivacyBasisLegalObligation}}},
		"a negative count":    {Erased: -1},
		"held with no id":     {Held: []PrivacyHeld{{Kind: "booking", Basis: PrivacyBasisLegalClaims}}},
		"kept and held at once": {Kept: []PrivacyKept{{Kind: "booking", ID: "b5", Fields: []string{"amount"}, Basis: PrivacyBasisLegalObligation}},
			Held: []PrivacyHeld{{Kind: "booking", ID: "b5", Basis: PrivacyBasisLegalClaims}}},
	} {
		if err := e.Validate(); err == nil {
			t.Errorf("%s: passed", name)
		}
	}
	// A handler's own error stays the handler's: Core reports it as the plugin not answering.
	g := &guestBook{err: errors.New("the bookings database is down")}
	if _, _, err := dispatchProvided(context.Background(), g, HookPrivacyErase, []byte(`{"emails":["a@b.co"]}`)); err == nil ||
		!strings.Contains(err.Error(), "database is down") {
		t.Errorf("the holder's own error was lost: %v", err)
	}
}

// THE WIRE, as docs/PLUGIN_SDK.md shows it and Core's internal/plugin/personaldata_test.go holds the same text. Core
// cannot import this module's newest types until a tag carries them, so the two repositories pin one JSON instead.
const privacyWireExample = `{"request":{"emails":["guest@example.com"],"request_id":"0b6a1d7e-3f7e-4a43-9d1c-2a1a7f0c9e11"},` +
	`"export":{"records":[{"kind":"booking","id":"b-1042","email":"guest@example.com","data":{"name":"Lena Gruber","starts_at":"2026-11-02T18:00:00Z"}}]},` +
	`"erasure":{"erased":1,"kept":[{"kind":"booking","id":"b-0977","fields":["amount","currency","paid_at","reference","payment_status","provider_reference"],"basis":"legal_obligation","note":"BAO §132: seven years"}],` +
	`"held":[{"kind":"booking","id":"b-1050","basis":"legal_claims","note":"a chargeback is open"}]}}`

func TestThePrivacyWireIsTheDocumentedOne(t *testing.T) {
	var in struct {
		Request PrivacyRequest `json:"request"`
		Export  PrivacyExport  `json:"export"`
		Erasure PrivacyErasure `json:"erasure"`
	}
	dec := json.NewDecoder(strings.NewReader(privacyWireExample))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		t.Fatalf("the documented wire does not decode into the SDK's types: %v", err)
	}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != privacyWireExample {
		t.Errorf("the SDK's types do not write the documented wire:\n got %s\nwant %s", out, privacyWireExample)
	}
	if err := in.Export.Validate(in.Request); err != nil {
		t.Errorf("the documented export is not valid: %v", err)
	}
	if err := in.Erasure.Validate(); err != nil {
		t.Errorf("the documented erasure is not valid: %v", err)
	}
	doc := readText(t, "docs/PLUGIN_SDK.md")
	for _, line := range []string{`"emails": ["guest@example.com"]`, `"basis": "legal_claims"`, `"basis": "legal_obligation"`} {
		if !strings.Contains(doc, line) {
			t.Errorf("docs/PLUGIN_SDK.md does not show %s", line)
		}
	}
}
