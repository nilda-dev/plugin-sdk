package nildatest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// AttributeShop is the shop's side of door D2 (docs/ATTRIBUTES.md), in memory, holding the rules the shop holds — so a
// plugin that owns fields, validates them or suggests addresses tests against the shop it will meet:
//
//   - it asks every field-owning plugin for its fields (describe, the last good answer standing in) and offers a buyer
//     only what each declares, where it declares it;
//   - an order is checked field by field (AttributeField.CheckValue, the SDK's one rule), its required fields held, and
//     each plugin that validates asked; one that does not answer blocks or allows as it declared;
//   - the order keeps a COPY of every value — the buyer's, the customer's, each line's product's — with the label in the
//     order's language, so it still says what was asked once the plugin is gone (D11's snapshot rule);
//   - it answers shop.attribute.values/v1 as the shop's write commands do (WRITE_COMMANDS.md): a key required and kept
//     for as long as the record, the plugin Core's word, its own fields only, an undo that brings back what a write
//     replaced, once, by its writer and not past a later write, and an event after each committed write;
//   - it proxies a visitor's address query only to a plugin that offers suggestions, bounded by the SDK's limiter.
//
// It does not know a catalogue or a customer list: any product and customer id is taken as one that exists.
//
//	desk := nildatest.NewServices()
//	shop := nildatest.NewAttributeShop(desk)
//	defer shop.Close()
//	desk.Provider("slots", mySlotPlugin)
//	order, err := shop.PlaceOrder(ctx, nildatest.AttributeOrderDraft{…})
type AttributeShop struct {
	mu      sync.Mutex
	srv     *httptest.Server
	api     *nilda.Services
	today   func() time.Time
	limiter *nilda.AddressSuggestLimiter

	orders   map[string]*AttributeOrder
	nOrders  int
	cells    map[string]map[string]*attrCell       // target key → plugin.field[.line] → value
	writes   map[string]nilda.AttributeWriteAnswer // caller|method|key → the first answer
	records  map[string]*attrRecord
	nRecords int
	events   []ShopEvent
}

// AttributeShopKey is the plugin key the kit's shop answers under — the shop's own.
const AttributeShopKey = "nilda_ecommerce"

// attrCell is one value the shop keeps, with the words it was shown with.
type attrCell struct {
	snap   AttributeSnapshot
	record string // the write that set it last ("" for the buyer's or the owner's)
}

// attrRecord is one write a plugin made, kept for its undo.
type attrRecord struct {
	caller, target, slot string
	prev                 *attrCell // what it replaced (nil: nothing)
	undone               bool
}

// NewAttributeShop installs the shop on desk as the provider of shop.attribute.values/v1, using shop.attributes/v1.
func NewAttributeShop(desk *Services) *AttributeShop {
	core, _, srv := desk.Core(AttributeShopKey, nilda.ContractShopAttributes)
	s := &AttributeShop{srv: srv, api: core.API().Services(), today: time.Now, limiter: nilda.NewAddressSuggestLimiter(),
		orders: map[string]*AttributeOrder{}, cells: map[string]map[string]*attrCell{}, writes: map[string]nilda.AttributeWriteAnswer{},
		records: map[string]*attrRecord{}}
	desk.Provider(AttributeShopKey, s)
	return s
}

// Close stops the shop's connection to the desk.
func (s *AttributeShop) Close() { s.srv.Close() }

// SetToday fixes the shop's date, for a date field's bounds (the shop's own time zone on a real site).
func (s *AttributeShop) SetToday(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.today = func() time.Time { return t }
}

// AttributeDraftLine is one basket line of an order being placed: its id, SKU and product.
type AttributeDraftLine struct {
	ID, SKU, Product string
	Quantity         int64
}

// AttributeOrderDraft is what a buyer places: the basket and the values typed at checkout and on product pages (each
// naming its plugin, field and, for a line field, its line).
type AttributeOrderDraft struct {
	Currency, Country, Customer, Locale string
	Lines                               []AttributeDraftLine
	Values                              []nilda.AttributeValue
}

// AttributeSnapshot is one value as the order keeps it: the value, the label in the order's language and the type at the
// moment it was kept, and whether the order shows it (the field is shown on the order).
type AttributeSnapshot struct {
	nilda.AttributeValue
	Label   string
	Type    string
	OnOrder bool
}

// AttributeOrder is a placed order: its lines, every value it keeps, and the plugins that validate and did not answer
// but allowed the order (their values are unchecked).
type AttributeOrder struct {
	ID         string
	Lines      []AttributeDraftLine
	Attributes []AttributeSnapshot
	Unchecked  []string
}

// AttributeOrderError is one reason an order was refused: a value's code (nilda.AttributeRequired …), or the plugin's
// own Message.
type AttributeOrderError struct {
	Plugin, Field, Line, Code, Message string
}

// AttributeRefusal is PlaceOrder's refusal: the values refused, and the plugins that validate, did not answer and block.
type AttributeRefusal struct {
	Errors     []AttributeOrderError
	Unanswered []string
}

func (r *AttributeRefusal) Error() string {
	var parts []string
	for _, e := range r.Errors {
		parts = append(parts, fmt.Sprintf("%s.%s[%s]: %s%s", e.Plugin, e.Field, e.Line, e.Code, e.Message))
	}
	for _, p := range r.Unanswered {
		parts = append(parts, p+": did not answer")
	}
	return "nildatest: the order was refused — " + strings.Join(parts, "; ")
}

// ShopEvent is one event the shop emitted, after its write committed.
type ShopEvent struct {
	Type string
	Data map[string]any
}

// fieldsOf asks every field-owning plugin for its description, keyed by plugin.
func (s *AttributeShop) fieldsOf(ctx context.Context) (map[string]nilda.AttributeDescription, error) {
	answers, _, err := s.api.AttributeFields(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]nilda.AttributeDescription{}
	for _, a := range answers {
		out[a.Provider] = a.Description
	}
	return out, nil
}

func fieldIn(d nilda.AttributeDescription, key string) (nilda.AttributeField, bool) {
	for _, f := range d.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return nilda.AttributeField{}, false
}

// buyerFills reports whether a buyer fills f on line (or on the order, line "").
func buyerFills(f nilda.AttributeField, line *AttributeDraftLine) bool {
	switch f.Target {
	case nilda.AttributeOrder, nilda.AttributeCustomer:
		return line == nil && f.Shows(nilda.AttributeShownCheckout)
	case nilda.AttributeLine:
		return line != nil && f.ForSKU(line.SKU) && (f.Shows(nilda.AttributeShownCheckout) || f.Shows(nilda.AttributeShownProductPage))
	}
	return false
}

// PlaceOrder places draft by the shop's rules, or refuses it with an *AttributeRefusal.
func (s *AttributeShop) PlaceOrder(ctx context.Context, d AttributeOrderDraft) (AttributeOrder, error) {
	descs, err := s.fieldsOf(ctx)
	if err != nil {
		return AttributeOrder{}, err
	}
	s.mu.Lock()
	today := s.today()
	s.mu.Unlock()
	lines := map[string]*AttributeDraftLine{}
	for i := range d.Lines {
		lines[d.Lines[i].ID] = &d.Lines[i]
	}
	refusal := &AttributeRefusal{}
	given := map[string]nilda.AttributeValue{} // plugin.field.line → value
	for _, v := range d.Values {
		line := lines[v.Line]
		desc, known := descs[v.Plugin]
		f, ok := fieldIn(desc, v.Field)
		if !known || !ok || (v.Line != "" && line == nil) || !buyerFills(f, line) {
			refusal.Errors = append(refusal.Errors, AttributeOrderError{Plugin: v.Plugin, Field: v.Field, Line: v.Line, Code: nilda.AttributeNotOffered})
			continue
		}
		if err := f.CheckValue(v.Value, today); err != nil {
			var ve *nilda.AttributeValueError
			errors.As(err, &ve)
			refusal.Errors = append(refusal.Errors, AttributeOrderError{Plugin: v.Plugin, Field: v.Field, Line: v.Line, Code: ve.Code})
			continue
		}
		given[v.Plugin+"."+v.Field+"."+v.Line] = v
	}
	plugins := make([]string, 0, len(descs))
	for p := range descs {
		plugins = append(plugins, p)
	}
	sort.Strings(plugins)
	// Required fields: each one the buyer is asked for, on the order and on each line it applies to — a customer field
	// the signed-in buyer already holds counts.
	for _, p := range plugins {
		for _, f := range descs[p].Fields {
			if !f.Required {
				continue
			}
			var where []*AttributeDraftLine
			if f.Target == nilda.AttributeLine {
				for i := range d.Lines {
					if buyerFills(f, &d.Lines[i]) {
						where = append(where, &d.Lines[i])
					}
				}
			} else if buyerFills(f, nil) {
				where = []*AttributeDraftLine{nil}
			}
			for _, l := range where {
				id := ""
				if l != nil {
					id = l.ID
				}
				if _, ok := given[p+"."+f.Key+"."+id]; ok {
					continue
				}
				if f.Target == nilda.AttributeCustomer && d.Customer != "" && s.cell(targetKey(nilda.AttributeCustomer, d.Customer, ""), p, f.Key) != nil {
					continue
				}
				refusal.Errors = append(refusal.Errors, AttributeOrderError{Plugin: p, Field: f.Key, Line: id, Code: nilda.AttributeRequired})
			}
		}
	}
	if len(refusal.Errors) > 0 {
		return AttributeOrder{}, refusal
	}
	// Each plugin that validates and has a field this order offers is asked, with its own values only.
	var unchecked []string
	basket := make([]nilda.AttributeBasketLine, 0, len(d.Lines))
	for _, l := range d.Lines {
		basket = append(basket, nilda.AttributeBasketLine{ID: l.ID, SKU: l.SKU, Quantity: l.Quantity})
	}
	for _, p := range plugins {
		desc := descs[p]
		if !desc.Validates || !offersAny(desc, d.Lines) {
			continue
		}
		req := nilda.AttributeValidateRequest{Currency: d.Currency, Country: d.Country, Customer: d.Customer, Locale: d.Locale,
			Lines: basket, Values: []nilda.AttributeValue{}}
		for _, v := range d.Values {
			if v.Plugin == p {
				f, _ := fieldIn(desc, v.Field)
				req.Values = append(req.Values, nilda.AttributeValue{Field: v.Field, Kind: f.Target, Line: v.Line, Value: v.Value})
			}
		}
		verdict, err := s.api.ValidateAttributes(ctx, p, desc, req)
		if err != nil {
			if desc.WhenUnanswered == nilda.AttributeAllow {
				unchecked = append(unchecked, p)
				continue
			}
			refusal.Unanswered = append(refusal.Unanswered, p)
			continue
		}
		for _, e := range verdict.Errors {
			refusal.Errors = append(refusal.Errors, AttributeOrderError{Plugin: p, Field: e.Field, Line: e.Line, Message: e.Message})
		}
	}
	if len(refusal.Errors) > 0 || len(refusal.Unanswered) > 0 {
		return AttributeOrder{}, refusal
	}
	return s.keep(d, descs, unchecked), nil
}

// offersAny reports whether desc has a field a buyer fills on this order.
func offersAny(desc nilda.AttributeDescription, lines []AttributeDraftLine) bool {
	for _, f := range desc.Fields {
		if buyerFills(f, nil) {
			return true
		}
		for i := range lines {
			if buyerFills(f, &lines[i]) {
				return true
			}
		}
	}
	return false
}

func snapOf(plugin string, f nilda.AttributeField, line string, value json.RawMessage, locale string) AttributeSnapshot {
	return AttributeSnapshot{AttributeValue: nilda.AttributeValue{Plugin: plugin, Field: f.Key, Kind: f.Target, Line: line,
		Value: append(json.RawMessage(nil), value...)}, Label: nilda.LabelIn(f.Label, locale), Type: f.Type,
		OnOrder: f.Shows(nilda.AttributeShownOrder)}
}

// keep stores the order: the buyer's values on the order and its lines, the customer's kept on the account and copied
// in, each line's product values copied onto it.
func (s *AttributeShop) keep(d AttributeOrderDraft, descs map[string]nilda.AttributeDescription, unchecked []string) AttributeOrder {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nOrders++
	id := fmt.Sprintf("o%d", s.nOrders)
	o := &AttributeOrder{ID: id, Lines: append([]AttributeDraftLine(nil), d.Lines...), Unchecked: unchecked}
	typedCustomer := map[string]bool{}
	for _, v := range d.Values {
		f, _ := fieldIn(descs[v.Plugin], v.Field)
		snap := snapOf(v.Plugin, f, v.Line, v.Value, d.Locale)
		switch f.Target {
		case nilda.AttributeOrder:
			s.put(targetKey(nilda.AttributeOrder, id, ""), &attrCell{snap: snap})
		case nilda.AttributeLine:
			s.put(targetKey(nilda.AttributeLine, id, v.Line), &attrCell{snap: snap})
		case nilda.AttributeCustomer:
			typedCustomer[v.Plugin+"."+v.Field] = true
			o.Attributes = append(o.Attributes, snap)
			if d.Customer != "" {
				s.put(targetKey(nilda.AttributeCustomer, d.Customer, ""), &attrCell{snap: snap})
			}
		}
	}
	if d.Customer != "" {
		for _, c := range s.cellsOf(targetKey(nilda.AttributeCustomer, d.Customer, "")) {
			if !typedCustomer[c.snap.Plugin+"."+c.snap.Field] {
				o.Attributes = append(o.Attributes, relabel(c.snap, descs, d.Locale))
			}
		}
	}
	for _, l := range d.Lines {
		for _, c := range s.cellsOf(targetKey(nilda.AttributeProduct, l.Product, "")) {
			cp := relabel(c.snap, descs, d.Locale)
			cp.Line = l.ID
			o.Attributes = append(o.Attributes, cp)
		}
	}
	s.orders[id] = o
	return s.view(o)
}

// relabel says a kept value's label in the order's language, from its plugin's current description when it answers.
func relabel(snap AttributeSnapshot, descs map[string]nilda.AttributeDescription, locale string) AttributeSnapshot {
	if f, ok := fieldIn(descs[snap.Plugin], snap.Field); ok {
		snap.Label = nilda.LabelIn(f.Label, locale)
		snap.OnOrder = f.Shows(nilda.AttributeShownOrder)
	}
	snap.Value = append(json.RawMessage(nil), snap.Value...)
	return snap
}

// view is the order as the shop shows it: its own and its lines' current values, then the copies. Callers hold s.mu.
func (s *AttributeShop) view(o *AttributeOrder) AttributeOrder {
	out := *o
	out.Attributes = nil
	for _, c := range s.cellsOf(targetKey(nilda.AttributeOrder, o.ID, "")) {
		out.Attributes = append(out.Attributes, c.snap)
	}
	for _, l := range o.Lines {
		for _, c := range s.cellsOf(targetKey(nilda.AttributeLine, o.ID, l.ID)) {
			out.Attributes = append(out.Attributes, c.snap)
		}
	}
	out.Attributes = append(out.Attributes, o.Attributes...)
	out.Lines = append([]AttributeDraftLine(nil), o.Lines...)
	out.Unchecked = append([]string(nil), o.Unchecked...)
	return out
}

// Order returns a placed order as the shop shows it.
func (s *AttributeShop) Order(id string) (AttributeOrder, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return AttributeOrder{}, false
	}
	return s.view(o), true
}

func targetKey(kind, id, line string) string { return kind + "\x00" + id + "\x00" + line }

func slotKey(plugin, field string) string { return plugin + "." + field }

// cell returns one kept value. Callers may hold s.mu or not; it locks nothing.
func (s *AttributeShop) cell(target, plugin, field string) *attrCell {
	return s.cells[target][slotKey(plugin, field)]
}

// put keeps c on target. Callers hold s.mu (keep, the writes) or are single-threaded.
func (s *AttributeShop) put(target string, c *attrCell) {
	if s.cells[target] == nil {
		s.cells[target] = map[string]*attrCell{}
	}
	s.cells[target][slotKey(c.snap.Plugin, c.snap.Field)] = c
}

// cellsOf is target's values, in a stable order.
func (s *AttributeShop) cellsOf(target string) []*attrCell {
	keys := make([]string, 0, len(s.cells[target]))
	for k := range s.cells[target] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*attrCell, 0, len(keys))
	for _, k := range keys {
		out = append(out, s.cells[target][k])
	}
	return out
}

// SetProductValue is the owner's product form: plugin's product field on product, checked by its rule. A placed order's
// copy does not move with it.
func (s *AttributeShop) SetProductValue(ctx context.Context, product, plugin, field string, value json.RawMessage) error {
	descs, err := s.fieldsOf(ctx)
	if err != nil {
		return err
	}
	f, ok := fieldIn(descs[plugin], field)
	if !ok || f.Target != nilda.AttributeProduct || !f.Shows(nilda.AttributeShownProductForm) {
		return fmt.Errorf("nildatest: the product form offers no %s.%s", plugin, field)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := f.CheckValue(value, s.today()); err != nil {
		return err
	}
	s.put(targetKey(nilda.AttributeProduct, product, ""), &attrCell{snap: snapOf(plugin, f, "", value, "")})
	return nil
}

// ErrAddressesNotOffered is the proxy's refusal to ask a plugin whose description offers no address suggestions.
var ErrAddressesNotOffered = errors.New("nildatest: that plugin offers no address suggestions")

// suggester checks the visitor's bound, then that provider offers suggestions.
func (s *AttributeShop) suggester(ctx context.Context, visitor, provider string) error {
	s.mu.Lock()
	now := s.today()
	s.mu.Unlock()
	if !s.limiter.Allow(visitor, now) {
		return nilda.ErrAddressSuggestLimited
	}
	descs, err := s.fieldsOf(ctx)
	if err != nil {
		return err
	}
	if !descs[provider].AddressSuggestions {
		return ErrAddressesNotOffered
	}
	return nil
}

// SuggestAddresses is the shop's address proxy for one visitor (their basket's or session's id): bounded, then asked of
// provider; a plugin that did not answer is no suggestions.
func (s *AttributeShop) SuggestAddresses(ctx context.Context, visitor, provider string, req nilda.AddressSuggestRequest) (nilda.AddressSuggestAnswer, error) {
	if err := req.Validate(); err != nil {
		return nilda.AddressSuggestAnswer{}, err
	}
	if err := s.suggester(ctx, visitor, provider); err != nil {
		return nilda.AddressSuggestAnswer{}, err
	}
	return s.api.SuggestAddresses(ctx, provider, req)
}

// ResolveAddress is the proxy's second step, under the same bound: the address of a picked suggestion.
func (s *AttributeShop) ResolveAddress(ctx context.Context, visitor, provider string, req nilda.AddressResolveRequest) (nilda.AddressResolveAnswer, error) {
	if err := req.Validate(); err != nil {
		return nilda.AddressResolveAnswer{}, err
	}
	if err := s.suggester(ctx, visitor, provider); err != nil {
		return nilda.AddressResolveAnswer{}, err
	}
	return s.api.ResolveAddress(ctx, provider, req)
}

// Events returns every event the shop emitted, oldest first.
func (s *AttributeShop) Events() []ShopEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ShopEvent(nil), s.events...)
}

// ServiceMethods answers shop.attribute.values/v1, each request checked by the SDK first.
func (s *AttributeShop) ServiceMethods() []nilda.ServiceMethod {
	return nilda.AttributeValuesMethods(s)
}

// The two refusals that are errors: a call no plugin made through Core, and a write without a key — what a plugin built
// on the SDK never sends. Every other refusal is the answer's Refused code.
var (
	ErrAttributeNotThroughCore = errors.New("nildatest: a plugin writes into the shop only through Core's service desk")
	ErrAttributeNeedsKey       = errors.New("nildatest: a write into the shop needs an idempotency key — derive it from what the write is for")
)

func refuse(code string) (nilda.AttributeWriteAnswer, error) {
	return nilda.AttributeWriteAnswer{Refused: code}, nil
}

// caller is the calling plugin and its key, as Core says them.
func caller(ctx context.Context) (string, string, error) {
	ci := nilda.CallOf(ctx)
	switch {
	case ci.Caller == "":
		return "", "", ErrAttributeNotThroughCore
	case ci.IdempotencyKey == "":
		return "", "", ErrAttributeNeedsKey
	}
	return ci.Caller, ci.IdempotencyKey, nil
}

// AttributeValues answers values.get: the caller's own values on one record.
func (s *AttributeShop) AttributeValues(ctx context.Context, req nilda.AttributeValuesRequest) (nilda.AttributeValuesAnswer, error) {
	who := nilda.CallOf(ctx).Caller
	if who == "" {
		return nilda.AttributeValuesAnswer{}, ErrAttributeNotThroughCore
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var snaps []AttributeSnapshot
	t := req.Target
	switch t.Kind {
	case nilda.AttributeOrder:
		o, ok := s.orders[t.ID]
		if !ok {
			return nilda.AttributeValuesAnswer{}, fmt.Errorf("nildatest: no order %s", t.ID)
		}
		snaps = s.view(o).Attributes
	default:
		for _, c := range s.cellsOf(targetKey(t.Kind, t.ID, t.Line)) {
			snaps = append(snaps, c.snap)
		}
	}
	out := nilda.AttributeValuesAnswer{Values: []nilda.AttributeValue{}}
	for _, sn := range snaps {
		if sn.Plugin == who {
			out.Values = append(out.Values, sn.AttributeValue)
		}
	}
	return out, nil
}

// SetAttribute answers value.set as the shop's write commands do.
func (s *AttributeShop) SetAttribute(ctx context.Context, req nilda.AttributeSetRequest) (nilda.AttributeWriteAnswer, error) {
	who, key, err := caller(ctx)
	if err != nil {
		return nilda.AttributeWriteAnswer{}, err
	}
	wk := who + "\x00" + nilda.AttributeValueSet + "\x00" + key
	s.mu.Lock()
	if prior, ok := s.writes[wk]; ok {
		s.mu.Unlock()
		prior.Already = true
		return prior, nil
	}
	s.mu.Unlock()
	descs, err := s.fieldsOf(ctx)
	if err != nil {
		return nilda.AttributeWriteAnswer{}, err
	}
	f, ok := fieldIn(descs[who], req.Field)
	if !ok || f.Target != req.Target.Kind {
		return refuse(nilda.AttributeNotOffered) // not one of the caller's fields, or not on that kind of record
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.writes[wk]; ok { // a concurrent call with the same key committed first
		prior.Already = true
		return prior, nil
	}
	if req.Target.Kind == nilda.AttributeOrder || req.Target.Kind == nilda.AttributeLine {
		o, ok := s.orders[req.Target.ID]
		if !ok || (req.Target.Kind == nilda.AttributeLine && !hasLine(o, req.Target.Line)) {
			return refuse(nilda.AttributeNoRecord)
		}
	}
	if err := f.CheckValue(req.Value, s.today()); err != nil {
		var ve *nilda.AttributeValueError
		if errors.As(err, &ve) {
			return refuse(ve.Code)
		}
		return nilda.AttributeWriteAnswer{}, err
	}
	target := targetKey(req.Target.Kind, req.Target.ID, req.Target.Line)
	s.nRecords++
	id := fmt.Sprintf("w%d", s.nRecords)
	rec := &attrRecord{caller: who, target: target, slot: slotKey(who, f.Key), prev: s.cell(target, who, f.Key)}
	if string(req.Value) == "null" || len(req.Value) == 0 || string(req.Value) == `""` {
		delete(s.cells[target], rec.slot)
	} else {
		s.put(target, &attrCell{snap: snapOf(who, f, req.Target.Line, req.Value, ""), record: id})
	}
	s.records[id] = rec
	answer := nilda.AttributeWriteAnswer{WriteAnswer: nilda.WriteAnswer{RecordID: id}}
	s.writes[wk] = answer
	// Committed: now it is announced.
	s.events = append(s.events, ShopEvent{Type: nilda.EventAttributeSet, Data: map[string]any{"record_id": id, "plugin": who,
		"field": f.Key, "kind": req.Target.Kind, "id": req.Target.ID, "line": req.Target.Line}})
	return answer, nil
}

func hasLine(o *AttributeOrder, line string) bool {
	for _, l := range o.Lines {
		if l.ID == line {
			return true
		}
	}
	return false
}

// UndoAttribute answers value.undo: the value the write replaced returns.
func (s *AttributeShop) UndoAttribute(ctx context.Context, req nilda.UndoInput) (nilda.AttributeWriteAnswer, error) {
	who, key, err := caller(ctx)
	if err != nil {
		return nilda.AttributeWriteAnswer{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	wk := who + "\x00" + nilda.AttributeValueUndo + "\x00" + key
	if prior, ok := s.writes[wk]; ok {
		prior.Already = true
		return prior, nil
	}
	rec, ok := s.records[req.RecordID]
	switch {
	case !ok || rec.caller != who:
		return refuse(nilda.AttributeUnknownWrite)
	case rec.undone:
		return refuse(nilda.AttributeUndoneAlready)
	}
	cur := s.cells[rec.target][rec.slot]
	if (cur == nil && s.latestWrite(rec.target, rec.slot) != req.RecordID) || (cur != nil && cur.record != req.RecordID) {
		return refuse(nilda.AttributeReplaced)
	}
	if rec.prev == nil {
		delete(s.cells[rec.target], rec.slot)
	} else {
		s.cells[rec.target][rec.slot] = rec.prev
	}
	rec.undone = true
	answer := nilda.AttributeWriteAnswer{WriteAnswer: nilda.WriteAnswer{RecordID: req.RecordID}}
	s.writes[wk] = answer
	s.events = append(s.events, ShopEvent{Type: nilda.EventAttributeUndone, Data: map[string]any{"record_id": req.RecordID, "plugin": who}})
	return answer, nil
}

// latestWrite is the last write that touched slot on target and is not undone — for a write that cleared a value.
func (s *AttributeShop) latestWrite(target, slot string) string {
	best, n := "", 0
	for id, r := range s.records {
		var k int
		if _, err := fmt.Sscanf(id, "w%d", &k); err == nil && r.target == target && r.slot == slot && !r.undone && k > n {
			best, n = id, k
		}
	}
	return best
}
