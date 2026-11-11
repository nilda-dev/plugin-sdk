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

// B31 (plugin-booking's close-out D17): a phone is compared by ONE rule, booking's blocklist rule moved here so Core,
// booking and every plugin compare alike — the digits, a leading 00 dropped, 6 to 20 digits or nothing. Exact on that
// normal form and never fuzzy: an erase that matched the wrong guest cannot be taken back. The national and the
// international form of one number are two numbers to this rule, on purpose; Core's screen asks for both.
func TestNormalizePrivacyPhoneIsBookingsRule(t *testing.T) {
	for in, want := range map[string]string{
		"+43 660 1234567":         "436601234567",
		"0043 660 1234567":        "436601234567",
		"0660 1234567":            "06601234567",
		"(0660) 123-45-67":        "06601234567",
		"tel: +43 660 1234567 ab": "436601234567",
		"12345":                   "",
		"123456":                  "123456",
		"12345678901234567890":    "12345678901234567890",
		"123456789012345678901":   "",
		"":                        "",
		"no digits at all":        "",
		"00 12345":                "",
	} {
		if got := NormalizePrivacyPhone(in); got != want {
			t.Errorf("NormalizePrivacyPhone(%q) = %q, want %q", in, got, want)
		}
	}
}

// A record names the ONE contact it matched, and it must be one the request asked about: a record naming neither
// cannot be matched to what the erase later says, one naming both claims a match the plugin did not make, and one
// about a phone nobody asked about is somebody else's data on its way into a stranger's archive.
func TestAPrivacyRecordNamesOneAskedContact(t *testing.T) {
	req := PrivacyRequest{Emails: []string{"guest@example.com"}, Phones: []string{"436601234567"}}
	for name, r := range map[string]PrivacyRecord{
		"neither":        {Kind: "booking", ID: "b1"},
		"both":           {Kind: "booking", ID: "b1", Email: "guest@example.com", Phone: "436601234567"},
		"an unasked one": {Kind: "booking", ID: "b1", Phone: "436609999999"},
	} {
		if err := (PrivacyExport{Records: []PrivacyRecord{r}}).Validate(req); err == nil {
			t.Errorf("%s: a record naming %q / %q passed", name, r.Email, r.Phone)
		}
	}
	for name, r := range map[string]PrivacyRecord{
		"an asked phone":                {Kind: "booking", ID: "b1", Phone: "436601234567"},
		"an asked phone, as typed":      {Kind: "booking", ID: "b1", Phone: "+43 660 1234567"},
		"an asked address, as before":   {Kind: "booking", ID: "b2", Email: "guest@example.com"},
		"an asked address, other cases": {Kind: "booking", ID: "b3", Email: " Guest@Example.com"},
	} {
		if err := (PrivacyExport{Records: []PrivacyRecord{r}}).Validate(req); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
	// An unasked phone refuses the WHOLE answer, as an unasked address does.
	bad := PrivacyExport{Records: []PrivacyRecord{
		{Kind: "booking", ID: "b1", Phone: "436601234567"},
		{Kind: "booking", ID: "b2", Phone: "436609999999"},
	}}
	if err := bad.Validate(req); err == nil || !strings.Contains(err.Error(), "not asked about") {
		t.Errorf("an answer with one record about another phone passed: %v", err)
	}
	// A phone that normalises to nothing is never a match, not even for a request that also normalises to nothing.
	if (PrivacyRequest{Phones: []string{"12"}}).AskedPhone("12") {
		t.Error("a phone too short to be one matched")
	}
	if !req.AskedPhone("+43 (660) 123 45 67") || req.AskedPhone("0660 1234567") {
		t.Error("AskedPhone does not compare by NormalizePrivacyPhone")
	}
}

// Core sends phones as the operator typed them; the SDK hands the holder their normal form, and drops what is no phone
// at all, so a holder never matches a row against "".
func TestDispatchNormalisesPhones(t *testing.T) {
	g := &guestBook{answer: PrivacyExport{Records: []PrivacyRecord{{Kind: "booking", ID: "b1", Phone: "436601234567",
		Data: map[string]any{"name": "Lena"}}}}}
	out, handled, err := dispatchProvided(context.Background(), g, HookPrivacyExport,
		[]byte(`{"phones":["+43 660 1234567","12","0660 1234567",""],"request_id":"r1"}`))
	if !handled || err != nil {
		t.Fatalf("privacy.export by phone: handled=%v err=%v", handled, err)
	}
	if len(g.asked) != 1 || !slices.Equal(g.asked[0].Phones, []string{"436601234567", "06601234567"}) {
		t.Fatalf("the holder was not handed the phones' normal forms: %+v", g.asked)
	}
	if len(g.asked[0].Emails) != 0 {
		t.Errorf("a phone-only request grew an address: %+v", g.asked[0].Emails)
	}
	if !strings.Contains(string(out), `"phone":"436601234567"`) || strings.Contains(string(out), `"email"`) {
		t.Errorf("a phone record is not written as the phone it matched: %s", out)
	}
	g.erased = PrivacyErasure{Erased: 1}
	if _, handled, err := dispatchProvided(context.Background(), g, HookPrivacyErase,
		[]byte(`{"phones":["0043 660 1234567"],"request_id":"r2"}`)); !handled || err != nil {
		t.Fatalf("privacy.erase by phone: handled=%v err=%v", handled, err)
	}
	if !slices.Equal(g.asked[1].Phones, []string{"436601234567"}) {
		t.Errorf("the erase was not handed the phone's normal form: %+v", g.asked[1].Phones)
	}
}

// Additive both ways: a Core built before phones sends none, and its request still decodes into this one; nothing
// is asked by phone then, so a holder that matches by phone finds nothing.
func TestAnOlderCoreSendsNoPhones(t *testing.T) {
	var req PrivacyRequest
	dec := json.NewDecoder(strings.NewReader(`{"emails":["guest@example.com"],"request_id":"r1"}`))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("an older Core's request does not decode: %v", err)
	}
	if req.Phones != nil {
		t.Errorf("an older Core's request carries phones: %v", req.Phones)
	}
	for _, p := range []string{"", "436601234567", "+43 660 1234567"} {
		if req.AskedPhone(p) {
			t.Errorf("AskedPhone(%q) is true for a request that names no phone", p)
		}
	}
	// And this SDK writes no phones key to an older plugin when there are none.
	b, err := json.Marshal(PrivacyRequest{Emails: []string{"guest@example.com"}, RequestID: "r1"})
	if err != nil || strings.Contains(string(b), "phones") {
		t.Errorf("a request without phones writes a phones key: %s (%v)", b, err)
	}
}

// TestAPhoneTypedInPersianOrArabicDigitsIsTheSameNumber is the 2026-11-11 review's finding 9.
func TestAPhoneTypedInPersianOrArabicDigitsIsTheSameNumber(t *testing.T) {
	for _, in := range []string{"09123456789", "۰۹۱۲ ۳۴۵ ۶۷۸۹", "٠٩١٢٣٤٥٦٧٨٩", "０９１２３４５６７８９"} {
		if got := NormalizePrivacyPhone(in); got != "09123456789" {
			t.Errorf("NormalizePrivacyPhone(%q) = %q, want 09123456789", in, got)
		}
	}
	if got := NormalizePrivacyPhone("۰۰۴۳ ۶۶۰ ۱۲۳۴۵۶۷"); got != "436601234567" {
		t.Errorf("a leading 00 in Persian digits: %q", got)
	}
	if !(PrivacyRequest{Phones: []string{"09123456789"}}).AskedPhone("۰۹۱۲۳۴۵۶۷۸۹") {
		t.Error("a row in Persian digits is not matched by the request")
	}
	if d, ok := decimalDigit('𝟗'); !ok || d != 9 {
		t.Errorf("a mathematical digit nine reads %d %v", d, ok)
	}
}
