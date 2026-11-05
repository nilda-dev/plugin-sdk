package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// repricingGateway re-prices: it answers payment.reprice with what a test sets, and records what it was asked.
type repricingGateway struct {
	recordingGateway
	reprice PaymentRepriceResult
	asked   []PaymentRepriceRequest
}

func (g *repricingGateway) RepricePayment(_ context.Context, r PaymentRepriceRequest) (PaymentRepriceResult, error) {
	g.asked = append(g.asked, r)
	return g.reprice, g.err
}

// stripeExpress is the express card method of a Stripe-like gateway: a token kind, its credential listed.
func stripeExpress() PaymentMethod {
	return PaymentMethod{Key: "wallet", Label: "Apple Pay, Google Pay, Link", CredentialTypes: []string{CredentialStripeConfirmationToken},
		Express: &PaymentExpress{Kind: ExpressToken, Script: "/express.js",
			Origins: PaymentExpressOrigins{
				Script:  []string{"https://js.stripe.com", "https://*.js.stripe.com"},
				Frame:   []string{"https://js.stripe.com", "https://*.js.stripe.com", "https://hooks.stripe.com"},
				Connect: []string{"https://api.stripe.com"}},
			Config: map[string]string{"publishable_key": "pk_live_51Hx", "merchant_country": "DE"}}}
}

// paypalExpress is a PayPal-like order kind: re-priced, its public client id in the config.
func paypalExpress() PaymentMethod {
	return PaymentMethod{Key: "paypal", Label: "PayPal", Supports: []string{SupportRefund, SupportReprice},
		Express: &PaymentExpress{Kind: ExpressOrder, Script: "/buttons.js",
			Origins: PaymentExpressOrigins{Script: []string{"https://www.paypal.com"}, Frame: []string{"https://*.paypal.com"}},
			Config:  map[string]string{"client_id": "AYSq3RDGsmBLJE-otTkBtM-jBRd1TCQwFf9RGfwddNXWz0uFU9ztymylOhRS"}}}
}

func startAnswer(t *testing.T, res PaymentStartResult) ([]byte, error) {
	t.Helper()
	g := &recordingGateway{start: res}
	out, _, err := DispatchPaymentGatewayHook(context.Background(), nil, g, HookPaymentStart,
		startPayload(t, PaymentSession{ID: "s1", AmountMinor: 4900, Currency: "EUR"}))
	return out, err
}

// X1: a redirected start sends the payer somewhere — the processor's page, or what the gateway's own script needs on the
// payer's page (a client secret for 3D Secure, a PayPal order) — and never nowhere. A client action belongs to a
// redirected payment only, and is one line of visible ASCII.
func TestRedirectedNeedsAURLOrAClientAction(t *testing.T) {
	t.Parallel()
	if _, err := startAnswer(t, PaymentStartResult{Status: PaymentRedirected}); err == nil ||
		!strings.Contains(err.Error(), "nowhere to go") {
		t.Fatalf("redirected with neither a page nor a client action: %v", err)
	}
	for name, res := range map[string]PaymentStartResult{
		"a page":             {Status: PaymentRedirected, RedirectURL: "https://checkout.stripe.com/c/pay/cs_1"},
		"a client action":    {Status: PaymentRedirected, ClientAction: "pi_3Q_secret_abc"},
		"both":               {Status: PaymentRedirected, RedirectURL: "https://www.paypal.com/checkoutnow?token=5O1", ClientAction: "5O190127TN364715T"},
		"a PayPal order id":  {Status: PaymentRedirected, ClientAction: "5O190127TN364715T", ProviderRef: "5O190127TN364715T"},
		"resolved, no extra": {Status: PaymentResolved},
	} {
		out, err := startAnswer(t, res)
		if err != nil {
			t.Errorf("%s: refused: %v", name, err)
			continue
		}
		var back PaymentStartResult
		if err := json.Unmarshal(out, &back); err != nil || back.ClientAction != res.ClientAction {
			t.Errorf("%s: the client action did not reach Core: %s", name, out)
		}
	}
	for name, res := range map[string]PaymentStartResult{
		"a client action on a resolved payment": {Status: PaymentResolved, ClientAction: "pi_3Q_secret_abc"},
		"a client action on a pending payment":  {Status: PaymentPending, ClientAction: "x"},
		"a client action with a space":          {Status: PaymentRedirected, ClientAction: "pi 3Q"},
		"a client action with a newline":        {Status: PaymentRedirected, ClientAction: "pi_3Q\nforged"},
		"a client action past its bound":        {Status: PaymentRedirected, ClientAction: ClientAction(strings.Repeat("a", maxClientAction+1))},
		"a page that is not absolute":           {Status: PaymentRedirected, RedirectURL: "/pay", ClientAction: "x"},
	} {
		if _, err := startAnswer(t, res); err == nil {
			t.Errorf("%s: went through", name)
		}
	}
}

// A client action is the payer's (a Stripe client secret lets its holder read the PaymentIntent): printing a session or
// an answer by mistake must not show it.
func TestAClientActionNeverPrints(t *testing.T) {
	t.Parallel()
	const secret = "pi_3QabcDEF_secret_xyz123"
	s := PaymentSession{ID: "s1", ClientAction: secret}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if out := fmt.Sprintf(verb, s); strings.Contains(out, secret) {
			t.Errorf("%s printed the client action: %s", verb, out)
		}
	}
	if b, _ := json.Marshal(s); !strings.Contains(string(b), `"client_action":"`+secret+`"`) {
		t.Errorf("the wire must still carry it to the payer's browser: %s", b)
	}
	if ClientAction("").String() != "" {
		t.Error("an empty client action prints as nothing")
	}
}

// X1: the public configuration is written into a page every visitor is served, so a value shaped like a secret — or a
// key named like one — is refused, whichever processor it is from. PayPal's client secret has no shape to recognise:
// it is refused by its name, and its client id (which PayPal puts in the page's script URL) passes.
func TestAPublicConfigRefusesAnythingSecretShaped(t *testing.T) {
	t.Parallel()
	refused := map[string]map[string]string{
		"a Stripe live secret key":         {"key": "sk_live_51HxYz"},
		"a Stripe test secret key":         {"key": "sk_test_51HxYz"},
		"a Stripe organization key":        {"key": "sk_org_51HxYz"},
		"a Stripe restricted key":          {"key": "rk_live_51HxYz"},
		"a Stripe restricted test key":     {"api_key": "rk_test_51HxYz"},
		"a Stripe webhook secret":          {"endpoint": "whsec_abc123"},
		"a PaymentIntent's client secret":  {"intent": "pi_3Qabc_secret_xyz"},
		"a SetupIntent's client secret":    {"intent": "seti_1Mm_secret_xyz"},
		"a PayPal access token":            {"auth": "A21AAFEpH4PsADK7qSS7pSRsgzfENtu-Q1ysgEDVDESseMHBYXVJYE8ovjj68elIDy8nF26AwPhfXTIeWAZHSLIsQkSYz9ifg"},
		"a PayPal client secret":           {"client_secret": "EGnHDxD_qRPdaLdZz8iCr8N7_MzF-YHPTkjs6NKYQvQSBngp4PTTVWkPZRbL"},
		"a secret by any name":             {"secret_key": "anything"},
		"a password":                       {"password": "hunter2"},
		"a private key by name":            {"private_key": "x"},
		"a PEM key":                        {"cert": "-----BEGIN PRIVATE KEY-----"},
		"a bearer header":                  {"auth": "Bearer abc"},
		"a signed token":                   {"session": "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2ln"},
		"a per-session client token":       {"client_token": "abc"},
		"a webhook signing secret by name": {"webhook_signing": "abc"},
		"a key that is not snake case":     {"publishableKey": "pk_live_1"},
		"a value with a newline":           {"publishable_key": "pk_live_1\n<script>"},
	}
	for name, cfg := range refused {
		if err := validPublicConfig(cfg); err == nil {
			t.Errorf("%s: %v passed as public configuration", name, cfg)
		}
		// And the same through the describe a gateway answers: refused before Core ever reads it.
		m := stripeExpress()
		m.Express.Config = cfg
		g := &recordingGateway{methods: []PaymentMethod{m}}
		if _, _, err := DispatchPaymentGatewayHook(context.Background(), nil, g, HookPaymentDescribe, []byte(`{}`)); err == nil {
			t.Errorf("%s: a describe answer carrying it went through", name)
		}
	}
	for name, cfg := range map[string]map[string]string{
		"a Stripe publishable key": {"publishable_key": "pk_live_51HxYz", "merchant_country": "DE"},
		"a PayPal client id":       {"client_id": "AYSq3RDGsmBLJE-otTkBtM-jBRd1TCQwFf9RGfwddNXWz0uFU9ztymylOhRS"},
		"a currency and a locale":  {"currency": "EUR", "locale": "de-DE"},
		"nothing":                  nil,
	} {
		if err := validPublicConfig(cfg); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
	big := map[string]string{}
	for i := 0; i <= maxExpressConfig; i++ {
		big[fmt.Sprintf("k%d", i)] = "v"
	}
	if validPublicConfig(big) == nil {
		t.Errorf("a configuration of %d keys passed; the bound is %d", len(big), maxExpressConfig)
	}
}

// An express method is held to its kind and to what Core will put on a page: a script on the gateway's own route, one
// https host per origin, a token kind that names its credential, an order kind that re-prices — through a gateway that
// can — and never a card at a reader.
func TestAnExpressDescribeIsHeldToItsKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	describe := func(g PaymentGateway) error {
		_, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentDescribe, []byte(`{}`))
		return err
	}
	if err := describe(&recordingGateway{methods: []PaymentMethod{stripeExpress()}}); err != nil {
		t.Fatalf("a Stripe-like express method: %v", err)
	}
	if err := describe(&repricingGateway{recordingGateway: recordingGateway{methods: []PaymentMethod{paypalExpress()}}}); err != nil {
		t.Fatalf("a PayPal-like express method: %v", err)
	}
	// An order kind's re-price needs a gateway that answers payment.reprice.
	if err := describe(&recordingGateway{methods: []PaymentMethod{paypalExpress()}}); err == nil ||
		!strings.Contains(err.Error(), "PaymentRepricer") {
		t.Errorf("a gateway that cannot re-price listed reprice: %v", err)
	}
	broken := map[string]func(m *PaymentMethod){
		"no kind":                         func(m *PaymentMethod) { m.Express.Kind = "" },
		"an unknown kind":                 func(m *PaymentMethod) { m.Express.Kind = "sheet" },
		"a token kind with no credential": func(m *PaymentMethod) { m.CredentialTypes = nil },
		"an order kind that never re-prices": func(m *PaymentMethod) {
			m.Express.Kind = ExpressOrder
		},
		"a card at a reader": func(m *PaymentMethod) {
			m.Supports = []string{SupportCardPresent}
			m.Readers = []PaymentReader{{ID: "tmr_1", Label: "Till"}}
		},
		"a script on another host":    func(m *PaymentMethod) { m.Express.Script = "https://evil.test/x.js" },
		"a protocol-relative script":  func(m *PaymentMethod) { m.Express.Script = "//evil.test/x.js" },
		"a script out of the route":   func(m *PaymentMethod) { m.Express.Script = "/../admin.js" },
		"a script with a query":       func(m *PaymentMethod) { m.Express.Script = "/x.js?v=1" },
		"no script":                   func(m *PaymentMethod) { m.Express.Script = "" },
		"an http origin":              func(m *PaymentMethod) { m.Express.Origins.Script = []string{"http://js.stripe.com"} },
		"a bare wildcard":             func(m *PaymentMethod) { m.Express.Origins.Frame = []string{"https://*"} },
		"a wildcard top-level domain": func(m *PaymentMethod) { m.Express.Origins.Frame = []string{"https://*.com"} },
		"a keyword":                   func(m *PaymentMethod) { m.Express.Origins.Script = []string{"'unsafe-inline'"} },
		"an origin with a path":       func(m *PaymentMethod) { m.Express.Origins.Connect = []string{"https://api.stripe.com/v1"} },
		"two origins in one":          func(m *PaymentMethod) { m.Express.Origins.Img = []string{"https://a.test https://b.test"} },
		"a scheme alone":              func(m *PaymentMethod) { m.Express.Origins.Script = []string{"https:"} },
		"an upper-case host":          func(m *PaymentMethod) { m.Express.Origins.Script = []string{"https://JS.stripe.com"} },
		"seventeen origins in a directive": func(m *PaymentMethod) {
			m.Express.Origins.Connect = nil
			for i := 0; i <= maxExpressOrigins; i++ {
				m.Express.Origins.Connect = append(m.Express.Origins.Connect, fmt.Sprintf("https://h%d.stripe.com", i))
			}
		},
	}
	for name, breakIt := range broken {
		m := stripeExpress()
		breakIt(&m)
		g := &capturingRepricer{repricingGateway: repricingGateway{recordingGateway: recordingGateway{methods: []PaymentMethod{m}}}}
		if err := describe(g); err == nil {
			t.Errorf("%s: the describe answer went through", name)
		}
	}
}

// capturingRepricer can do everything, so only the express rule under test can refuse a describe answer.
type capturingRepricer struct {
	repricingGateway
}

func (g *capturingRepricer) CapturePayment(context.Context, PaymentCaptureRequest) (PaymentCaptureResult, error) {
	return PaymentCaptureResult{}, nil
}
func (g *capturingRepricer) VoidPayment(context.Context, PaymentVoidRequest) (PaymentVoidResult, error) {
	return PaymentVoidResult{}, nil
}

func repricePayload(t *testing.T, s PaymentSession, amount int64, opts ...PaymentShippingOption) []byte {
	t.Helper()
	b, err := json.Marshal(PaymentRepriceRequest{Session: s, AmountMinor: amount, ShippingOptions: opts})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// X1: a session is re-priced only while its payer has not approved — created or redirected, the processor's order still
// open — and never one a credential, a kept method or a reader started, or whose method does not re-price. The gateway
// is not asked otherwise; its refusal and an answer Core cannot act on are said, not guessed.
func TestRepriceOnlyBeforeThePayerApproves(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	open := PaymentSession{ID: "s1", AmountMinor: 4900, Currency: "EUR", Status: PaymentRedirected,
		Supports: []string{SupportRefund, SupportReprice}, ClientAction: "5O190127TN364715T"}
	g := &repricingGateway{reprice: PaymentRepriceResult{Repriced: true}}
	opts := []PaymentShippingOption{{ID: "dhl", Label: "DHL", AmountMinor: 490, Selected: true},
		{ID: "packstation", Label: "DHL Packstation", AmountMinor: 390}}
	out, handled, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentReprice, repricePayload(t, open, 5390, opts...))
	if err != nil || !handled || string(out) != `{"repriced":true}` {
		t.Fatalf("an open order re-priced: %s, %v, %v", out, handled, err)
	}
	if len(g.asked) != 1 || g.asked[0].AmountMinor != 5390 || len(g.asked[0].ShippingOptions) != 2 {
		t.Fatalf("the gateway was asked %+v", g.asked)
	}
	created := open
	created.Status = PaymentCreated
	if _, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentReprice, repricePayload(t, created, 5390)); err != nil {
		t.Errorf("a session whose start has not answered yet is still open: %v", err)
	}

	after := map[string]func(s *PaymentSession){
		"approved and held":               func(s *PaymentSession) { s.Status = PaymentAuthorized },
		"approved and pending":            func(s *PaymentSession) { s.Status = PaymentPending },
		"paid":                            func(s *PaymentSession) { s.Status = PaymentResolved },
		"ended":                           func(s *PaymentSession) { s.Status = PaymentRejected },
		"expired":                         func(s *PaymentSession) { s.Status = PaymentExpired },
		"a kept method's charge":          func(s *PaymentSession) { s.ChargedSaved = "sm_1" },
		"a card at a reader":              func(s *PaymentSession) { s.Reader = "tmr_1" },
		"a method that is kept":           func(s *PaymentSession) { s.Save = &PaymentMandate{Purpose: MandatePreOrder} },
		"a method that does not re-price": func(s *PaymentSession) { s.Supports = []string{SupportRefund} },
	}
	for name, change := range after {
		s := open
		change(&s)
		before := len(g.asked)
		if _, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentReprice, repricePayload(t, s, 5390)); err == nil {
			t.Errorf("%s: re-priced", name)
		}
		if len(g.asked) != before {
			t.Errorf("%s: the gateway was asked", name)
		}
	}
	for name, payload := range map[string][]byte{
		"no amount":            repricePayload(t, open, 0),
		"no session id":        repricePayload(t, PaymentSession{AmountMinor: 1, Currency: "EUR", Status: PaymentRedirected, Supports: []string{SupportReprice}}, 1),
		"two options selected": repricePayload(t, open, 5390, PaymentShippingOption{ID: "a", Label: "A", Selected: true}, PaymentShippingOption{ID: "b", Label: "B", Selected: true}),
		"none selected":        repricePayload(t, open, 5390, PaymentShippingOption{ID: "a", Label: "A"}),
		"one id twice":         repricePayload(t, open, 5390, PaymentShippingOption{ID: "a", Label: "A", Selected: true}, PaymentShippingOption{ID: "a", Label: "B"}),
		"a negative price":     repricePayload(t, open, 5390, PaymentShippingOption{ID: "a", Label: "A", AmountMinor: -1, Selected: true}),
	} {
		if _, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentReprice, payload); err == nil {
			t.Errorf("%s: reached the processor", name)
		}
	}
	// The processor's refusal is an answer (Core: 409, the amount kept); an answer Core cannot read is an error.
	g.reprice = PaymentRepriceResult{FailureMessage: "the payer approved the order"}
	if out, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentReprice, repricePayload(t, open, 5390)); err != nil ||
		!strings.Contains(string(out), `"repriced":false`) {
		t.Errorf("the processor's refusal: %s, %v", out, err)
	}
	g.reprice = PaymentRepriceResult{Repriced: true, FailureMessage: "both"}
	if _, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentReprice, repricePayload(t, open, 5390)); err == nil {
		t.Error("repriced with a failure message went through")
	}
	// A gateway that does not re-price was never subscribed: the hook is not its.
	if _, handled, _ := DispatchPaymentGatewayHook(ctx, nil, &recordingGateway{}, HookPaymentReprice, repricePayload(t, open, 5390)); handled {
		t.Error("a gateway that does not re-price handled payment.reprice")
	}
}

// Serve subscribes payment.reprice for a gateway that re-prices, and for no other.
func TestAGatewayThatRepricesIsSubscribedToReprice(t *testing.T) {
	t.Parallel()
	type repricer struct {
		plainOnly
		*repricingGateway
	}
	if got := withProvidedHooks(repricer{repricingGateway: &repricingGateway{}}, nil); !slices.Contains(got, HookPaymentReprice) {
		t.Errorf("a gateway that re-prices is subscribed to %v", got)
	}
	if got := withProvidedHooks(gatewayOnly{}, nil); slices.Contains(got, HookPaymentReprice) {
		t.Errorf("a gateway that does not re-price is subscribed to %v", got)
	}
}

// The consumer's re-price goes where Core serves it, keyed, and the client refuses what Core would.
func TestARepriceIsSentAsCoreReadsIt(t *testing.T) {
	t.Parallel()
	var method, path, key, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		raw, _ := json.Marshal(b)
		method, path, key, body = r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"), string(raw)
		_, _ = w.Write([]byte(`{"data":{"id":"s1","amount_minor":5390,"currency":"EUR","status":"redirected"}}`))
	}))
	defer srv.Close()
	pay := newAPI(srv.URL, "tok", nil).Payments()
	ctx := context.Background()
	s, err := pay.Reprice(ctx, "order-1:quote-2", "s1", PaymentReprice{AmountMinor: 5390,
		ShippingOptions: []PaymentShippingOption{{ID: "dhl", Label: "DHL", AmountMinor: 490, Selected: true}}})
	if err != nil || s.AmountMinor != 5390 {
		t.Fatalf("re-price: %+v, %v", s, err)
	}
	if method != http.MethodPost || path != "/payments/sessions/s1/reprice" || key != "order-1:quote-2" ||
		body != `{"amount_minor":5390,"shipping_options":[{"amount_minor":490,"id":"dhl","label":"DHL","selected":true}]}` {
		t.Errorf("sent %s %s key %q body %s", method, path, key, body)
	}
	for name, call := range map[string]func() error{
		"no key":     func() error { _, err := pay.Reprice(ctx, "", "s1", PaymentReprice{AmountMinor: 1}); return err },
		"no session": func() error { _, err := pay.Reprice(ctx, "k", "", PaymentReprice{AmountMinor: 1}); return err },
		"no amount":  func() error { _, err := pay.Reprice(ctx, "k", "s1", PaymentReprice{}); return err },
	} {
		if call() == nil {
			t.Errorf("%s: sent", name)
		}
	}
}

// X1: the address a wallet shows before its payer confirms carries enough to price delivery and tax — country, region,
// city, postal code — and nothing more: no street and no name, in the type and on the wire; and nothing personal travels
// before complete, when the payer arrives whole.
func TestAPartialWalletAddressCarriesNoStreetOrName(t *testing.T) {
	t.Parallel()
	var fields []string
	typ := reflect.TypeOf(WalletAddress{})
	for i := 0; i < typ.NumField(); i++ {
		fields = append(fields, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
	}
	if strings.Join(fields, ",") != "country,region,city,postal_code" {
		t.Fatalf("a wallet's partial address carries %v; it carries country, region, city and postal_code, nothing else", fields)
	}
	quote := func(address string) string {
		return `{"step":"quote","gateway":"stripe","method":"wallet","source":"cart","address":` + address + `}`
	}
	if _, err := DecodeExpressRequest(strings.NewReader(quote(`{"country":"DE","region":"BY","city":"München","postal_code":"80331"}`))); err != nil {
		t.Fatalf("a partial address: %v", err)
	}
	for name, address := range map[string]string{
		"a street":       `{"country":"DE","line1":"Marienplatz 1"}`,
		"a second line":  `{"country":"DE","line2":"3. OG"}`,
		"a name":         `{"country":"DE","name":"Mia Huber"}`,
		"a phone":        `{"country":"DE","phone":"+49 89 1234"}`,
		"no country":     `{"city":"München"}`,
		"a country name": `{"country":"Germany"}`,
	} {
		if _, err := DecodeExpressRequest(strings.NewReader(quote(address))); err == nil {
			t.Errorf("%s in the partial address was taken", name)
		}
	}
	payer := &ExpressPayer{Email: "mia@example.test", Name: "Mia Huber",
		ShippingAddress: &PaymentAddress{Name: "Mia Huber", Line1: "Packstation 123", City: "München", PostalCode: "80331", Country: "DE"}}
	for name, r := range map[string]ExpressRequest{
		"a payer with a quote": {Step: ExpressStepQuote, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, Payer: payer},
		"a payer with a start": {Step: ExpressStepStart, Gateway: "paypal", Method: "paypal", Source: ExpressFromCart, Press: "p1", Payer: payer},
		"a partial address with complete": {Step: ExpressStepComplete, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, Press: "p1",
			Payer: payer, Address: &WalletAddress{Country: "DE"}, Credential: &PaymentCredential{Type: CredentialStripeConfirmationToken, Token: "ctoken_1"}},
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%s went through", name)
		}
	}
	done := ExpressRequest{Step: ExpressStepComplete, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, Press: "p1",
		Payer: payer, Credential: &PaymentCredential{Type: CredentialStripeConfirmationToken, Token: "ctoken_1"}}
	if err := done.Validate(); err != nil {
		t.Errorf("the payer arrives whole with complete: %v", err)
	}
}

// Each step carries what it needs and nothing else, and a press keys the start and the complete.
func TestAnExpressRequestCarriesWhatItsStepNeeds(t *testing.T) {
	t.Parallel()
	payer := &ExpressPayer{Email: "mia@example.test"}
	token := &PaymentCredential{Type: CredentialStripeConfirmationToken, Token: "ctoken_1"}
	item := &ExpressItem{Product: "WHEEL-28", Options: map[string]string{"size": "28"}, Quantity: 1}
	good := map[string]ExpressRequest{
		"a quote of the cart, no address yet": {Step: ExpressStepQuote, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart},
		"a quote of one product":              {Step: ExpressStepQuote, Gateway: "stripe", Method: "wallet", Source: ExpressFromProduct, Item: item, Address: &WalletAddress{Country: "AT", PostalCode: "8010"}},
		"a re-price of an order kind":         {Step: ExpressStepQuote, Gateway: "paypal", Method: "paypal", Source: ExpressFromCart, Session: "s1", ShippingOption: "dhl", Address: &WalletAddress{Country: "DE"}},
		"an order kind's start":               {Step: ExpressStepStart, Gateway: "paypal", Method: "paypal", Source: ExpressFromCart, Press: "p-1"},
		"a token kind's complete":             {Step: ExpressStepComplete, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, Press: "p-1", Payer: payer, Credential: token},
		"an order kind's complete":            {Step: ExpressStepComplete, Gateway: "paypal", Method: "paypal", Source: ExpressFromCart, Press: "p-1", Payer: payer, Session: "s1"},
	}
	for name, r := range good {
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string]ExpressRequest{
		"no gateway":                     {Step: ExpressStepQuote, Method: "wallet", Source: ExpressFromCart},
		"an unknown step":                {Step: "pay", Gateway: "stripe", Method: "wallet", Source: ExpressFromCart},
		"an unknown source":              {Step: ExpressStepQuote, Gateway: "stripe", Method: "wallet", Source: "wishlist"},
		"a product with no item":         {Step: ExpressStepQuote, Gateway: "stripe", Method: "wallet", Source: ExpressFromProduct},
		"a product of no quantity":       {Step: ExpressStepQuote, Gateway: "stripe", Method: "wallet", Source: ExpressFromProduct, Item: &ExpressItem{Product: "W"}},
		"the cart with an item":          {Step: ExpressStepQuote, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, Item: item},
		"a start with no press":          {Step: ExpressStepStart, Gateway: "paypal", Method: "paypal", Source: ExpressFromCart},
		"a start naming a session":       {Step: ExpressStepStart, Gateway: "paypal", Method: "paypal", Source: ExpressFromCart, Press: "p", Session: "s1"},
		"a credential with a quote":      {Step: ExpressStepQuote, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, Credential: token},
		"a complete with no press":       {Step: ExpressStepComplete, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, Payer: payer, Credential: token},
		"a complete with no payer":       {Step: ExpressStepComplete, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, Press: "p", Credential: token},
		"a complete with no email":       {Step: ExpressStepComplete, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, Press: "p", Payer: &ExpressPayer{}, Credential: token},
		"a complete with both":           {Step: ExpressStepComplete, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, Press: "p", Payer: payer, Credential: token, Session: "s1"},
		"a complete with neither":        {Step: ExpressStepComplete, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, Press: "p", Payer: payer},
		"a press that is not url-safe":   {Step: ExpressStepStart, Gateway: "paypal", Method: "paypal", Source: ExpressFromCart, Press: "p 1"},
		"a credential with no token":     {Step: ExpressStepComplete, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, Press: "p", Payer: payer, Credential: &PaymentCredential{Type: CredentialStripeConfirmationToken}},
		"a delivery option on two lines": {Step: ExpressStepQuote, Gateway: "stripe", Method: "wallet", Source: ExpressFromCart, ShippingOption: "a\nb"},
	}
	for name, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%s went through", name)
		}
	}
	if _, err := DecodeExpressRequest(strings.NewReader(`{"step":"quote"` + strings.Repeat(" ", maxExpressBody) + `}`)); err == nil {
		t.Error("a body past 64 KiB was read")
	}
	if s := fmt.Sprintf("%v", good["a token kind's complete"]); strings.Contains(s, "ctoken_1") {
		t.Errorf("a request printed its credential: %s", s)
	}
}

// The shop's answer is one Core's widget can draw truthfully: a total and its words, at most ten delivery options with
// one chosen, and what the page does next.
func TestAnExpressAnswerIsOneTheWidgetCanDraw(t *testing.T) {
	t.Parallel()
	ok := ExpressAnswer{Available: true, Currency: "EUR", TotalMinor: 48490, Total: "€484.90", Capture: CaptureManual,
		Lines:           []ExpressLine{{Label: "Wheel 28\"", AmountMinor: 48000}, {Label: "DHL", AmountMinor: 490}},
		ShippingOptions: []PaymentShippingOption{{ID: "dhl", Label: "DHL", AmountMinor: 490, Selected: true}},
		Notice:          "By paying you accept the terms.", TermsURL: "https://site.test/terms",
		Session: "s1", Status: PaymentRedirected, ClientAction: "5O190127TN364715T", OrderURL: "https://site.test/shop/orders/o1"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a good answer: %v", err)
	}
	if err := (ExpressAnswer{Message: "This basket needs your consent to the download terms; use the checkout."}).Validate(); err != nil {
		t.Errorf("not available, with the reason: %v", err)
	}
	for name, change := range map[string]func(a *ExpressAnswer){
		"no total":                           func(a *ExpressAnswer) { a.TotalMinor = 0 },
		"no currency":                        func(a *ExpressAnswer) { a.Currency = "" },
		"no words for the total":             func(a *ExpressAnswer) { a.Total = "" },
		"an unknown capture":                 func(a *ExpressAnswer) { a.Capture = "later" },
		"a status no session has":            func(a *ExpressAnswer) { a.Status = "paid" },
		"a client action on a paid session":  func(a *ExpressAnswer) { a.Status = PaymentResolved },
		"an order page that is not absolute": func(a *ExpressAnswer) { a.OrderURL = "javascript:alert(1)" },
		"eleven delivery options": func(a *ExpressAnswer) {
			a.ShippingOptions = nil
			for i := 0; i <= maxShippingOptions; i++ {
				a.ShippingOptions = append(a.ShippingOptions, PaymentShippingOption{ID: fmt.Sprint(i), Label: "x", Selected: i == 0})
			}
		},
		"a notice on two lines": func(a *ExpressAnswer) { a.Notice = "By paying\n<script>" },
	} {
		a := ok
		change(&a)
		if err := a.Validate(); err == nil {
			t.Errorf("%s went through", name)
		}
	}
}
