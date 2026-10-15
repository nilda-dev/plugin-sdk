package nilda

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// THE SHOP'S ORDERS, READ BY A PLUGIN THAT DELIVERS SOMETHING FOR THEM (ORDERS.md; commerce unit 10, plan 14).
//
// A plugin that delivers what an order bought — a licence key, a gift card's code, an event ticket — hears the shop's
// ecommerce.order_paid, ecommerce.order_refunded and ecommerce.order_cancelled events, which carry only the order's
// id. It then reads the order here: its lines (which SKUs, how many, how many came back), the buyer's email and the
// language the shop writes to them in. The shop PROVIDES shop.orders/v1; such a plugin REQUIRES it.
//
// What the plugin delivered comes back to the buyer through shop.deliverables/v1, which the plugin PROVIDES and the
// shop USES: the shop shows the items on its thank-you page and in the buyer's account, and treats the SKUs the
// plugin delivers as digital content at checkout (the EU waiver of the right of withdrawal, as for a download).

// ContractShopOrders is the shop's order reads.
const ContractShopOrders = "shop.orders/v1"

// OrderGet is its one method.
const OrderGet = "order.get"

// An order's status, as the shop derives it from its money, goods and cancellation.
const (
	OrderStatusPending    = "pending"
	OrderStatusProcessing = "processing"
	OrderStatusCompleted  = "completed"
	OrderStatusRefunded   = "refunded"
	OrderStatusCancelled  = "cancelled"
	OrderStatusFailed     = "failed"
)

// ShopOrderRequest asks for one order by its id (the id the shop's events carry).
type ShopOrderRequest struct {
	OrderID string `json:"order_id"`
}

// Validate refuses a request with no id.
func (r ShopOrderRequest) Validate() error {
	if strings.TrimSpace(r.OrderID) == "" || len(r.OrderID) > 64 {
		return errors.New("nilda: an order is asked for by its id")
	}
	return nil
}

// ShopOrderResult is one order, or Found false.
type ShopOrderResult struct {
	Found bool       `json:"found"`
	Order *ShopOrder `json:"order,omitempty"`
}

// Validate checks the order.
func (r ShopOrderResult) Validate() error {
	if r.Found != (r.Order != nil) {
		return errors.New("nilda: a found order carries the order, and only then")
	}
	if r.Order != nil {
		return r.Order.Validate()
	}
	return nil
}

// ShopOrder is one order as a delivering plugin needs it.
type ShopOrder struct {
	ID string `json:"id"`
	// Ref is the reference the buyer reads in every mail ("NLD-1042").
	Ref string `json:"ref"`
	// Status is one of the OrderStatus words; Paid says the money is in (processing or completed), the moment a
	// delivery is owed.
	Status string `json:"status"`
	Paid   bool   `json:"paid"`
	// Email is the buyer's; Locale the language the shop writes to them in.
	Email  string `json:"email"`
	Locale string `json:"locale"`
	// Currency is the order's (ISO 4217); TotalMinor what it cost, in minor units.
	Currency   string          `json:"currency"`
	TotalMinor int64           `json:"total_minor"`
	PlacedAt   time.Time       `json:"placed_at"`
	Lines      []ShopOrderLine `json:"lines"`
}

// Validate refuses an order a plugin could not deliver against.
func (o ShopOrder) Validate() error {
	if strings.TrimSpace(o.ID) == "" || strings.TrimSpace(o.Ref) == "" {
		return errors.New("nilda: an order has an id and a reference")
	}
	switch o.Status {
	case OrderStatusPending, OrderStatusProcessing, OrderStatusCompleted, OrderStatusRefunded, OrderStatusCancelled, OrderStatusFailed:
	default:
		return fmt.Errorf("nilda: order %s has status %q", o.Ref, o.Status)
	}
	if err := validCurrency("the order's currency", o.Currency); err != nil {
		return err
	}
	for _, l := range o.Lines {
		if strings.TrimSpace(l.SKU) == "" || l.Quantity < 0 || l.QuantityRefunded < 0 || l.QuantityRefunded > l.Quantity {
			return fmt.Errorf("nilda: order %s has a line with no SKU or impossible quantities", o.Ref)
		}
	}
	return nil
}

// ShopOrderLine is one line: the variant's SKU, what it was called, how many were bought and how many came back.
type ShopOrderLine struct {
	SKU        string `json:"sku"`
	ProductSKU string `json:"product_sku,omitempty"`
	Title      string `json:"title"`
	// Quantity is how many were bought; QuantityRefunded how many of them were refunded since.
	Quantity         int64 `json:"quantity"`
	QuantityRefunded int64 `json:"quantity_refunded,omitempty"`
	UnitPriceMinor   int64 `json:"unit_price_minor"`
	// OfferID is the offer (shop.offers/v1) the line was bought through, "" for one the buyer added themselves — what
	// an offers plugin counts its sales by.
	OfferID string `json:"offer_id,omitempty"`
}

// ---- the shop's side ---------------------------------------------------------------------------------------

// OrdersProvider is the shop. Serve finds it by itself; declare "provides": ["shop.orders/v1"] with
// service_provider.
type OrdersProvider interface {
	ShopOrder(ctx context.Context, req ShopOrderRequest) (ShopOrderResult, error)
}

// OrdersMethods are the shop's order methods, each checking the request before the shop sees it and the answer before
// it leaves.
func OrdersMethods(p OrdersProvider) []ServiceMethod {
	return []ServiceMethod{
		Serves(ContractShopOrders, OrderGet, func(ctx context.Context, req ShopOrderRequest) (ShopOrderResult, error) {
			if err := req.Validate(); err != nil {
				return ShopOrderResult{}, err
			}
			r, err := p.ShopOrder(ctx, req)
			if r.Order != nil && r.Order.Lines == nil {
				r.Order.Lines = []ShopOrderLine{} // none is an empty list on the wire, never null
			}
			return checked(r, err)
		}),
	}
}

// ---- a delivering plugin's side ----------------------------------------------------------------------------

// ShopOrder reads one order from the shop. One shop answers, so none answering is an error.
func (s *Services) ShopOrder(ctx context.Context, orderID string) (ShopOrderResult, error) {
	req := ShopOrderRequest{OrderID: orderID}
	if err := req.Validate(); err != nil {
		return ShopOrderResult{}, err
	}
	res, err := s.Call(ctx, ContractShopOrders, OrderGet, req, ServiceCallOptions{})
	if err != nil {
		return ShopOrderResult{}, err
	}
	if len(res.Answers) == 0 {
		return ShopOrderResult{}, fmt.Errorf("nilda: no shop answered %s %s", ContractShopOrders, OrderGet)
	}
	var r ShopOrderResult
	if err := res.Answers[0].Decode(&r); err != nil {
		return ShopOrderResult{}, err
	}
	return r, r.Validate()
}

// ============================================================================================================
// shop.deliverables/v1
// ============================================================================================================

// ContractShopDeliverables is what a plugin delivers for the shop's orders.
const ContractShopDeliverables = "shop.deliverables/v1"

// Its methods.
const (
	DeliverablesDescribe   = "describe"
	DeliverablesOrderItems = "order.items"
)

// A delivered item's kind, for the shop to draw it.
const (
	// DeliverableKey is a code the buyer types or pastes: a licence key, a gift card's code, a game key.
	DeliverableKey = "key"
	// DeliverableLink is a page the buyer opens: a ticket, an account to claim.
	DeliverableLink = "link"
)

// DeliverablesDescription is which SKUs a plugin delivers. The shop treats each as digital content: the buyer is
// asked the EU waiver of the right of withdrawal at checkout, as for a download.
type DeliverablesDescription struct {
	SKUs []string `json:"skus"`
}

// Validate refuses an empty SKU.
func (d DeliverablesDescription) Validate() error {
	for _, s := range d.SKUs {
		if strings.TrimSpace(s) == "" {
			return errors.New("nilda: a deliverables description lists an empty SKU")
		}
	}
	return nil
}

// DeliverablesRequest asks what was delivered for one order.
type DeliverablesRequest struct {
	OrderID string `json:"order_id"`
	// Locale is the language the shop shows the page in; Label and Note are written in it.
	Locale string `json:"locale,omitempty"`
}

// Validate refuses a request with no order.
func (r DeliverablesRequest) Validate() error {
	return ShopOrderRequest{OrderID: r.OrderID}.Validate()
}

// DeliverablesResult is what one plugin delivered for the order — nothing yet is an empty list.
type DeliverablesResult struct {
	Items []DeliveredItem `json:"items"`
}

// Validate checks every item.
func (r DeliverablesResult) Validate() error {
	for _, it := range r.Items {
		if err := it.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// DeliveredItem is one thing delivered for one unit of an order line.
type DeliveredItem struct {
	// SKU is the line's; Title what the buyer bought ("Photo Editor Pro").
	SKU   string `json:"sku"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	// Label names the value ("Licence key"); Value is the key itself, or the link's address (absolute).
	Label string `json:"label"`
	Value string `json:"value"`
	// Note is one sentence under it ("Activate on up to 3 devices. Valid until 15 October 2027.").
	Note string `json:"note,omitempty"`
	// Pending: owed and not delivered yet (the plugin has none to give right now); Value is then empty and Note
	// says when it comes.
	Pending bool `json:"pending,omitempty"`
}

// Validate refuses an item the shop could not draw safely.
func (it DeliveredItem) Validate() error {
	if strings.TrimSpace(it.SKU) == "" || strings.TrimSpace(it.Label) == "" {
		return errors.New("nilda: a delivered item names its SKU and its label")
	}
	if len(it.Value) > 2048 || len(it.Note) > 500 || len(it.Label) > 100 || len(it.Title) > 300 {
		return errors.New("nilda: a delivered item's text is too long")
	}
	switch it.Kind {
	case DeliverableKey:
	case DeliverableLink:
		if !it.Pending && !strings.HasPrefix(it.Value, "https://") && !strings.HasPrefix(it.Value, "http://") {
			return errors.New("nilda: a delivered link is an absolute address")
		}
	default:
		return fmt.Errorf("nilda: a delivered item of kind %q", it.Kind)
	}
	if it.Pending != (it.Value == "") {
		return errors.New("nilda: a delivered item carries its value, or is pending and carries none")
	}
	return nil
}

// DeliverablesProvider is a plugin that delivers what orders bought. Serve finds it by itself; declare
// "provides": ["shop.deliverables/v1"] with service_provider.
type DeliverablesProvider interface {
	DescribeDeliverables(ctx context.Context) (DeliverablesDescription, error)
	OrderDeliverables(ctx context.Context, req DeliverablesRequest) (DeliverablesResult, error)
}

// DeliverablesMethods are the provider's contract methods.
func DeliverablesMethods(p DeliverablesProvider) []ServiceMethod {
	return []ServiceMethod{
		Serves(ContractShopDeliverables, DeliverablesDescribe, func(ctx context.Context, _ struct{}) (DeliverablesDescription, error) {
			d, err := p.DescribeDeliverables(ctx)
			if d.SKUs == nil {
				d.SKUs = []string{} // none is an empty list on the wire, never null
			}
			return checked(d, err)
		}),
		Serves(ContractShopDeliverables, DeliverablesOrderItems, func(ctx context.Context, req DeliverablesRequest) (DeliverablesResult, error) {
			if err := req.Validate(); err != nil {
				return DeliverablesResult{}, err
			}
			r, err := p.OrderDeliverables(ctx, req)
			if r.Items == nil {
				r.Items = []DeliveredItem{}
			}
			return checked(r, err)
		}),
	}
}

// ---- the shop's side of deliverables -----------------------------------------------------------------------

// DeliveredSKUs is every SKU some plugin delivers, from every provider that answered. A provider that is there and
// did not answer is an error, not "none": a checkout that skipped the waiver on that would sell digital content
// without it.
func (s *Services) DeliveredSKUs(ctx context.Context) (map[string]bool, error) {
	res, err := s.Call(ctx, ContractShopDeliverables, DeliverablesDescribe, struct{}{}, ServiceCallOptions{})
	if err != nil {
		return nil, err
	}
	if len(res.Missed) > 0 {
		return nil, fmt.Errorf("nilda: %s did not answer %s %s (%s)", res.Missed[0].Provider,
			ContractShopDeliverables, DeliverablesDescribe, res.Missed[0].Reason)
	}
	out := map[string]bool{}
	for _, a := range res.Answers {
		var d DeliverablesDescription
		if err := a.Decode(&d); err != nil {
			return nil, err
		}
		if err := d.Validate(); err != nil {
			return nil, err
		}
		for _, sku := range d.SKUs {
			out[sku] = true
		}
	}
	return out, nil
}

// OrderDeliverables is everything every delivering plugin delivered for one order, in the providers' order. A
// provider that did not answer is left out and named in missed, so a page can say some items could not be shown.
func (s *Services) OrderDeliverables(ctx context.Context, req DeliverablesRequest) (items []DeliveredItem, missed int, err error) {
	if err := req.Validate(); err != nil {
		return nil, 0, err
	}
	res, err := s.Call(ctx, ContractShopDeliverables, DeliverablesOrderItems, req, ServiceCallOptions{})
	if err != nil {
		return nil, 0, err
	}
	for _, a := range res.Answers {
		var r DeliverablesResult
		if err := a.Decode(&r); err != nil {
			missed++
			continue
		}
		if r.Validate() != nil {
			missed++
			continue
		}
		items = append(items, r.Items...)
	}
	return items, missed + len(res.Missed), nil
}
