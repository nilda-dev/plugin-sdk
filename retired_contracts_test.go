package nilda

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// retiredContracts are the contracts whose features moved INTO the shop (commerce DR-19, Unit 12): wholesale
// (shop.buyers/v1), offers (shop.offers/v1), the agentic checkout (shop.checkout/v1) and back-in-stock requests
// (stock.alerts/v1) are the shop's own now, so no plugin provides or consumes them. Each with what made it, so nothing of it is left behind.
var retiredContracts = []struct {
	name, doc string
	idents    []string
}{
	{retiredShopBuyers, "docs/BUYERS.md", []string{"ContractShopBuyers", "BuyersProvider", "BuyersMethods", "BuyerTerms",
		"QuoteRequestInput", "QuoteOpenRequest"}},
	{retiredShopOffers, "docs/OFFERS.md", []string{"ContractShopOffers", "OffersProvider", "OffersMethods", "Offer",
		"OffersRequest", "OffersConfirmRequest"}},
	{retiredShopCheckout, "docs/CHECKOUT.md", []string{"ContractShopCheckout", "CheckoutProvider", "CheckoutMethods",
		"CheckoutDescription", "CheckoutCreateRequest", "CheckoutResult"}},
	// The form's StockAlertRequest and StockAlertResult stay: the shop answers Core's "notify me" form with them.
	{retiredStockAlerts, "docs/STOCK_ALERTS.md", []string{"ContractStockAlerts", "StockAlertProvider", "StockAlertMethods",
		"StockAlertsDescription", "StockAlertsDescribe", "StockAlertSubscribe"}},
}

// The names as text: the constants are gone with their files.
const (
	retiredShopBuyers   = "shop.buyers/v1"
	retiredShopOffers   = "shop.offers/v1"
	retiredShopCheckout = "shop.checkout/v1"
	retiredStockAlerts  = "stock.alerts/v1"
)

// A RETIRED CONTRACT IS GONE, NOT KEPT BESIDE THE SHOP (Unit 12 A3, Rule 0). Not published, no contract file, no
// exported name of it in the module, no document of it.
func TestContractsHaveNoRetiredOnes(t *testing.T) {
	published, err := Contracts()
	if err != nil {
		t.Fatal(err)
	}
	exported := map[string]bool{}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for n := range f.Scope.Objects {
			if ast.IsExported(n) {
				exported[n] = true
			}
		}
	}
	for _, r := range retiredContracts {
		for _, c := range published {
			if c.Name == r.name {
				t.Errorf("%s is still published; its feature is the shop's own (DR-19)", r.name)
			}
		}
		if _, err := os.Stat(filepath.Join("contracts", strings.TrimSuffix(r.name, "/v1"), "v1")); err == nil {
			t.Errorf("contracts/%s is still there", r.name)
		}
		if _, err := os.Stat(r.doc); err == nil {
			t.Errorf("%s still documents %s", r.doc, r.name)
		}
		for _, id := range r.idents {
			if exported[id] {
				t.Errorf("%s still exports %s", r.name, id)
			}
		}
	}
}
