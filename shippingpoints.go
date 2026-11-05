package nilda

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// SERVICE POINTS: A PARCEL DELIVERED TO A SHOP COUNTER OR A LOCKER THE BUYER CHOSE (v0.58.0, EXPERIMENTAL; commerce plan
// 10.9, step S7; docs/SHIPPING.md §3 "Service points").
//
// A service that delivers to a service point — a parcel shop, a locker, a post office — lists CarrierSupportServicePoints.
// The shop asks rates with RatesRequest.ServicePoints set when it can let the buyer choose one; a carrier offers a rate
// that needs one (CarrierRate.NeedsServicePoint) only then, so a shop built before never sells a point delivery it cannot
// complete. The buyer picks a rate that needs a point, the shop asks that carrier for the points near the delivery address
// (service_points: ServicePointsRequest → ServicePointsResult, bounded), the buyer chooses one, and the label carries it
// (LabelRequest.ServicePoint). Every rule is checked by CarrierMethods on the carrier's side.
//
// Read on 2026-11-05: Sendcloud's Service Points API v3 (sendcloud.dev/.openapi/v3/service-points/openapi.yaml — search by
// country_code and a free-text address, structured address_* or latitude/longitude; limit at most 200, default 100; a
// point's id, carrier_service_point_id, general_shop_type servicepoint/locker/post_office/carrier_depot, address,
// position, opening_times, distance in metres, requirements.post_number) and its shipments' to_service_point
// (sendcloud.dev/.openapi/v3/shipments/openapi.yaml, service-point-request: id or carrier_service_point_id; a required
// post number goes in to_address.po_box). Shippo's public API (docs.goshippo.com/spec/shippoapi/public-api.yaml) has no
// service point search; plugin-shippo declares none.

// CarrierServicePointsGet is the contract's method that lists the service points near an address.
const CarrierServicePointsGet = "service_points"

// CarrierSupportServicePoints: the service delivers to a service point the buyer chooses, not to the door. Its rates
// carry NeedsServicePoint and its labels need ServicePoint; a carrier listing it implements CarrierServicePoints.
const CarrierSupportServicePoints = "service_points"

// CarrierInvalidServicePoint is the failure code of a label whose service point is missing, unknown to the carrier, on
// a service that delivers to none, or no longer taking parcels: the shop asks the buyer to choose another.
const CarrierInvalidServicePoint = "invalid_service_point"

// The kinds of service point (ServicePoint.Kind) — Sendcloud's general_shop_type, renamed for a buyer; "" when the carrier
// does not say.
const (
	ServicePointShop       = "parcel_shop"
	ServicePointLocker     = "locker"
	ServicePointPostOffice = "post_office"
	ServicePointDepot      = "depot"
)

// The bounds of a search.
const (
	// DefaultServicePoints is how many points a search with no Limit asks for: the shop lists the ten nearest.
	DefaultServicePoints = 10
	// MaxServicePoints is the most one search may ask for or be answered with — a list a buyer reads, not a map's
	// worth (Sendcloud allows 200).
	MaxServicePoints = 50
	maxPointHours    = 50 // seven days, a few spans each
	maxPostNumber    = 32
)

// ServicePointsRequest asks one carrier for the service points of one of its point services near an address. The
// address is the delivery address's place — its country and at least its postal code or city, or coordinates — never
// the buyer's name or contact.
type ServicePointsRequest struct {
	// Service is the point service the buyer chose a rate of (CarrierRate.Service).
	Service string `json:"service"`
	// Country is ISO 3166-1 alpha-2 and always present.
	Country    string `json:"country"`
	PostalCode string `json:"postal_code,omitempty"`
	City       string `json:"city,omitempty"`
	// Line1 is the street and number, which places the search more precisely.
	Line1 string `json:"line1,omitempty"`
	// Latitude and Longitude, both or neither, search around a point instead.
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
	// Limit is how many points, nearest first: 1 to MaxServicePoints, 0 for DefaultServicePoints.
	Limit int `json:"limit,omitempty"`
	// Locale is the buyer's language, for a carrier that names its points in more than one.
	Locale string `json:"locale,omitempty"`
}

// ServicePointHours is one span a point is open on one day.
type ServicePointHours struct {
	// Day is monday … sunday.
	Day string `json:"day"`
	// Opens and Closes are local times, "HH:MM", 24-hour; Closes may be "24:00".
	Opens  string `json:"opens"`
	Closes string `json:"closes"`
}

// ServicePoint is one place a parcel can be delivered to for the buyer to collect.
type ServicePoint struct {
	// ID is what the carrier plugin finds the point by again — sent back as LabelRequest.ServicePoint.ID.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Carrier is who runs the point, as a buyer recognises it: "DHL", "PostNL".
	Carrier string `json:"carrier,omitempty"`
	// Kind is one of the ServicePoint kinds, or "".
	Kind string `json:"kind,omitempty"`
	// Address is the point's own address; its Country is always present, and it names no person.
	Address   ShipAddress `json:"address"`
	Latitude  *float64    `json:"latitude,omitempty"`
	Longitude *float64    `json:"longitude,omitempty"`
	// DistanceMeters is how far it is from the place searched; nil when the carrier does not say.
	DistanceMeters *int64 `json:"distance_meters,omitempty"`
	// Hours are when it is open this week; empty when the carrier does not say.
	Hours []ServicePointHours `json:"hours,omitempty"`
	// RequiresPostNumber: the carrier needs the receiver's own number for this point (DHL's Postnummer for a
	// Packstation): the shop asks the buyer and sends it as LabelRequest.ServicePoint.PostNumber.
	RequiresPostNumber bool `json:"requires_post_number,omitempty"`
}

// ServicePointsResult is service_points' answer: the points, nearest first — none is an answer too, "nothing near" —
// or a failure code when the carrier could not say.
type ServicePointsResult struct {
	Points         []ServicePoint `json:"points,omitempty"`
	FailureCode    string         `json:"failure_code,omitempty"`
	FailureMessage string         `json:"failure_message,omitempty"`
}

// ChosenServicePoint is the point the buyer chose, on a rates question (RatesRequest.ServicePoint, a price to that very
// point) and on a label (LabelRequest.ServicePoint).
type ChosenServicePoint struct {
	// ID is ServicePoint.ID, as the same carrier listed it.
	ID string `json:"id"`
	// PostNumber is the receiver's own number the point requires (ServicePoint.RequiresPostNumber).
	PostNumber string `json:"post_number,omitempty"`
}

// CarrierServicePoints is a carrier that delivers to service points: it lists them. A carrier whose describe lists a
// service with CarrierSupportServicePoints must be one; without it, service_points answers "unsupported".
type CarrierServicePoints interface {
	ServicePoints(ctx context.Context, req ServicePointsRequest) (ServicePointsResult, error)
}

// Validate refuses a search no carrier could place: one with no service or no ISO country, one that names no place
// within the country — a postal code, a city or a position — half a position or one off the globe, a limit past
// MaxServicePoints, and text past its bound or not one plain line.
func (r ServicePointsRequest) Validate() error {
	switch {
	case strings.TrimSpace(r.Service) == "" || len(r.Service) > maxCarrierKey:
		return fmt.Errorf("nilda: a service point search names the point service, 1 to %d characters", maxCarrierKey)
	case r.Limit < 0 || r.Limit > MaxServicePoints:
		return fmt.Errorf("nilda: a service point search asks for 1 to %d points (0 for %d), got %d", MaxServicePoints,
			DefaultServicePoints, r.Limit)
	case (r.Latitude == nil) != (r.Longitude == nil):
		return errors.New("nilda: a service point search gives a latitude and a longitude, or neither")
	case r.Latitude == nil && strings.TrimSpace(r.PostalCode) == "" && strings.TrimSpace(r.City) == "":
		return errors.New("nilda: a service point search names a postal code, a city or a position — a country alone is the whole country")
	case r.Locale != "" && !validLocale(r.Locale):
		return fmt.Errorf("nilda: %q is not a language tag (\"de\", \"pt-BR\")", r.Locale)
	}
	if err := validCountry("a service point search's country", r.Country); err != nil {
		return err
	}
	if r.Latitude != nil {
		if err := validPosition(*r.Latitude, *r.Longitude); err != nil {
			return err
		}
	}
	return validPlace("a service point search's", r.Line1, r.City, r.PostalCode)
}

// validPosition refuses a position off the globe.
func validPosition(lat, lng float64) error {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 || lat != lat || lng != lng {
		return fmt.Errorf("nilda: a position is a latitude of -90 to 90 and a longitude of -180 to 180, got %v, %v", lat, lng)
	}
	return nil
}

// validPlace holds a street, a city and a postal code to the address bounds, each one plain line.
func validPlace(whose, line1, city, postal string) error {
	for _, f := range []struct {
		name, v string
		max     int
	}{{"line1", line1, maxAddressLine}, {"city", city, maxAddressCity}, {"postal_code", postal, maxAddressPostal}} {
		if utf8.RuneCountInString(f.v) > f.max || !plainLine(f.v) {
			return fmt.Errorf("nilda: %s %s is one plain line of at most %d characters", whose, f.name, f.max)
		}
	}
	return nil
}

// limit is the request's Limit, or the default.
func (r ServicePointsRequest) limit() int {
	if r.Limit == 0 {
		return DefaultServicePoints
	}
	return r.Limit
}

var servicePointKinds = map[string]bool{"": true, ServicePointShop: true, ServicePointLocker: true,
	ServicePointPostOffice: true, ServicePointDepot: true}

var weekDays = map[string]bool{"monday": true, "tuesday": true, "wednesday": true, "thursday": true, "friday": true,
	"saturday": true, "sunday": true}

// clock reports whether v is "HH:MM", 00:00 to 24:00.
func clock(v string) bool {
	if len(v) != 5 || v[2] != ':' {
		return false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return (v[:2] < "24" && v[3:] < "60") || v == "24:00"
}

// Validate refuses a point the shop could not show or send back: no id or name, a kind outside the four, an address
// with no country, half a position, a negative distance, or opening hours that are not a day and two times in order.
func (p ServicePoint) Validate() error {
	switch {
	case strings.TrimSpace(p.ID) == "" || len(p.ID) > maxCarrierKey || !plainLine(p.ID):
		return fmt.Errorf("nilda: a service point has an id of 1 to %d characters, one plain line", maxCarrierKey)
	case strings.TrimSpace(p.Name) == "":
		return fmt.Errorf("nilda: service point %q has no name", p.ID)
	case !servicePointKinds[p.Kind]:
		return fmt.Errorf("nilda: service point %q's kind is parcel_shop, locker, post_office, depot or none, got %q", p.ID, p.Kind)
	case (p.Latitude == nil) != (p.Longitude == nil):
		return fmt.Errorf("nilda: service point %q gives a latitude and a longitude, or neither", p.ID)
	case p.DistanceMeters != nil && *p.DistanceMeters < 0:
		return fmt.Errorf("nilda: service point %q is a negative distance away", p.ID)
	case len(p.Hours) > maxPointHours:
		return fmt.Errorf("nilda: service point %q has more than %d opening spans", p.ID, maxPointHours)
	}
	for _, f := range []struct{ name, v string }{{"name", p.Name}, {"carrier", p.Carrier}} {
		if utf8.RuneCountInString(f.v) > maxCarrierText || !plainLine(f.v) {
			return fmt.Errorf("nilda: service point %q's %s is one plain line of at most %d characters", p.ID, f.name, maxCarrierText)
		}
	}
	if err := p.Address.Validate(); err != nil {
		return fmt.Errorf("service point %q: %w", p.ID, err)
	}
	if p.Latitude != nil {
		if err := validPosition(*p.Latitude, *p.Longitude); err != nil {
			return err
		}
	}
	for _, h := range p.Hours {
		if !weekDays[h.Day] || !clock(h.Opens) || !clock(h.Closes) || h.Opens >= h.Closes {
			return fmt.Errorf("nilda: service point %q opens %q %q–%q: a day monday … sunday and two times HH:MM in order",
				p.ID, h.Day, h.Opens, h.Closes)
		}
	}
	return nil
}

// Validate refuses an answer the shop could not list: points beside a failure code, a code the contract does not know,
// more than MaxServicePoints, one point twice, or a point that does not validate. No points and no code is "none near".
func (r ServicePointsResult) Validate() error {
	if r.FailureCode != "" && len(r.Points) > 0 {
		return errors.New("nilda: a service point answer is points or a failure_code, not both")
	}
	if err := validFailure(r.FailureCode, r.FailureMessage, false); err != nil {
		return err
	}
	if len(r.Points) > MaxServicePoints {
		return fmt.Errorf("nilda: a service point answer has at most %d points", MaxServicePoints)
	}
	seen := map[string]bool{}
	for _, p := range r.Points {
		if err := p.Validate(); err != nil {
			return err
		}
		if seen[p.ID] {
			return fmt.Errorf("nilda: service point %q is listed twice", p.ID)
		}
		seen[p.ID] = true
	}
	return nil
}

// ValidateFor is Validate with the search it answers: never more points than were asked for.
func (r ServicePointsResult) ValidateFor(req ServicePointsRequest) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if len(r.Points) > req.limit() {
		return fmt.Errorf("nilda: %d service points answer a search for %d", len(r.Points), req.limit())
	}
	return nil
}

// Validate refuses a choice with no point, or text that is not one plain short line.
func (c ChosenServicePoint) Validate() error {
	switch {
	case strings.TrimSpace(c.ID) == "" || len(c.ID) > maxCarrierKey || !plainLine(c.ID):
		return fmt.Errorf("nilda: a chosen service point has an id of 1 to %d characters, one plain line", maxCarrierKey)
	case utf8.RuneCountInString(c.PostNumber) > maxPostNumber || !plainLine(c.PostNumber):
		return fmt.Errorf("nilda: a post number is one plain line of at most %d characters", maxPostNumber)
	}
	return nil
}

// pointServices are the keys of a description's services that deliver to a service point.
func pointServices(d CarrierDescription) map[string]bool {
	out := map[string]bool{}
	for _, s := range d.Services {
		if Supports(s.Supports, CarrierSupportServicePoints) {
			out[s.Key] = true
		}
	}
	return out
}

// listsItsPoints refuses a description with a point service from a carrier that cannot list points: the shop would sell
// a delivery the buyer can never complete.
func listsItsPoints(c Carrier, d CarrierDescription) error {
	if _, ok := c.(CarrierServicePoints); ok {
		return nil
	}
	for key := range pointServices(d) {
		return fmt.Errorf("nilda: service %q delivers to service points and this carrier does not list them — "+
			"implement CarrierServicePoints", key)
	}
	return nil
}

// pointRatesFor refuses a rate that needs a service point from a carrier that cannot list them, or to a shop that did
// not say it can let the buyer choose one.
func pointRatesFor(c Carrier, req RatesRequest, res RatesResult) error {
	_, finder := c.(CarrierServicePoints)
	for _, r := range res.Rates {
		switch {
		case !r.NeedsServicePoint:
		case !finder:
			return fmt.Errorf("nilda: rate %q needs a service point and this carrier does not list them", r.Service)
		case !req.ServicePoints && req.ServicePoint == nil:
			return fmt.Errorf("nilda: rate %q needs a service point and the shop did not ask for point rates "+
				"(RatesRequest.ServicePoints): offer it only to a shop that can let the buyer choose one", r.Service)
		}
	}
	return nil
}

// labelPointRule answers, without asking the carrier to buy, a label whose service point does not fit its service: a
// point service with no point chosen, or a point on a door service. The carrier's description says which is which —
// settings, never a live call.
func labelPointRule(ctx context.Context, c Carrier, req LabelRequest) (*LabelResult, error) {
	d, err := c.DescribeCarrier(ctx)
	if err != nil {
		return nil, err
	}
	known := false
	for _, s := range d.Services {
		known = known || s.Key == req.Service
	}
	if !known {
		return nil, nil // the carrier answers a service it does not describe in its own words
	}
	point := pointServices(d)[req.Service]
	switch {
	case point && req.ServicePoint == nil:
		return &LabelResult{Status: LabelRejected, FailureCode: CarrierInvalidServicePoint,
			FailureMessage: "this service delivers to a service point: the buyer chooses one before the label is bought"}, nil
	case !point && req.ServicePoint != nil:
		return &LabelResult{Status: LabelRejected, FailureCode: CarrierInvalidServicePoint,
			FailureMessage: "this service delivers to the door, not to a service point"}, nil
	}
	return nil, nil
}

// ShippingServicePoints asks the carrier whose point rate the buyer chose for the points near the delivery address. One
// carrier, never every one: a point is only good for the carrier that listed it.
func (s *Services) ShippingServicePoints(ctx context.Context, provider string, req ServicePointsRequest) (ServicePointsResult, error) {
	if err := req.Validate(); err != nil {
		return ServicePointsResult{}, err
	}
	var out ServicePointsResult
	if err := s.callOne(ctx, provider, CarrierServicePointsGet, req, "", &out); err != nil {
		return out, err
	}
	return out, out.ValidateFor(req)
}

// servicePointsMethod is the carrier's side of service_points.
func servicePointsMethod(c Carrier) ServiceMethod {
	return Serves(ContractShippingCarrier, CarrierServicePointsGet, func(ctx context.Context, req ServicePointsRequest) (ServicePointsResult, error) {
		if err := req.Validate(); err != nil {
			return ServicePointsResult{}, err
		}
		f, ok := c.(CarrierServicePoints)
		if !ok {
			return ServicePointsResult{FailureCode: CarrierUnsupported, FailureMessage: "this carrier delivers to no service point"}, nil
		}
		res, err := f.ServicePoints(ctx, req)
		if err != nil {
			return res, err
		}
		if err := res.ValidateFor(req); err != nil {
			return ServicePointsResult{}, err
		}
		return res, nil
	})
}
