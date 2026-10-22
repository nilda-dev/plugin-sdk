package nildatest

import (
	"context"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// memBook is a guest book in memory: rows by email, one with a paid deposit (kept for the accounts once anonymised)
// and one under an open chargeback (held whole).
type memBook struct {
	rows map[string]memRow
	// lie makes it report the erasure and keep the rows; recount makes the second erase count again; holdUnknown
	// holds a record it never had.
	lie, recount, holdUnknown bool
	erases                    int
}

type memRow struct {
	email           string
	deposit, appeal bool
}

func newMemBook() *memBook {
	return &memBook{rows: map[string]memRow{
		"b1": {email: "guest@example.com"},
		"b2": {email: "guest@example.com", deposit: true},
		"b3": {email: "guest@example.com", appeal: true},
		"b9": {email: "other@example.com"},
	}}
}

func (m *memBook) ExportPersonalData(_ context.Context, req nilda.PrivacyRequest) (nilda.PrivacyExport, error) {
	var out nilda.PrivacyExport
	for id, r := range m.rows {
		if req.Asked(r.email) {
			out.Records = append(out.Records, nilda.PrivacyRecord{Kind: "booking", ID: id, Email: r.email,
				Data: map[string]any{"deposit": r.deposit}})
		}
	}
	return out, nil
}

func (m *memBook) ErasePersonalData(_ context.Context, req nilda.PrivacyRequest) (nilda.PrivacyErasure, error) {
	var out nilda.PrivacyErasure
	for id, r := range m.rows {
		if !req.Asked(r.email) {
			continue
		}
		switch {
		case r.appeal:
			out.Held = append(out.Held, nilda.PrivacyHeld{Kind: "booking", ID: id, Basis: nilda.PrivacyBasisLegalClaims,
				Note: "a chargeback is open"})
			continue
		case r.deposit:
			out.Kept = append(out.Kept, nilda.PrivacyKept{Kind: "booking", ID: id, Basis: nilda.PrivacyBasisLegalObligation,
				Fields: []string{"amount", "currency", "paid_at"}})
		default:
			out.Erased++
		}
		if !m.lie {
			r.email = "" // anonymised: no longer found by the address
			m.rows[id] = r
		}
	}
	if m.recount && m.erases > 0 {
		out.Erased++ // the second run counts a record it already erased
	}
	m.erases++
	if m.holdUnknown {
		out.Held = append(out.Held, nilda.PrivacyHeld{Kind: "booking", ID: "nowhere", Basis: nilda.PrivacyBasisLegalClaims})
	}
	return out, nil
}

// The round Core runs — export, erase, export again — passes for a holder that does what it says, and names the
// lie for one that does not.
func TestCheckPersonalDataHolder(t *testing.T) {
	ctx := context.Background()
	if err := CheckPersonalDataHolder(ctx, newMemBook(), " Guest@Example.com"); err != nil {
		t.Fatalf("a correct holder failed the round: %v", err)
	}
	if got := newMemBook().rows["b9"].email; got != "other@example.com" {
		t.Fatalf("fixture: %q", got)
	}

	for name, c := range map[string]struct {
		book *memBook
		want string
	}{
		"says erased, keeps the rows":    {&memBook{rows: newMemBook().rows, lie: true}, "still returns"},
		"counts again on the second run": {&memBook{rows: newMemBook().rows, recount: true}, "second erase"},
		"holds a record it never had":    {&memBook{rows: newMemBook().rows, holdUnknown: true}, "never returned"},
	} {
		err := CheckPersonalDataHolder(ctx, c.book, "guest@example.com")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error saying %q", name, err, c.want)
		}
	}

	// A holder that finds nothing proves nothing: the author is told to seed a record first.
	if err := CheckPersonalDataHolder(ctx, newMemBook(), "nobody@example.com"); err == nil ||
		!strings.Contains(err.Error(), "found nothing") {
		t.Errorf("an empty round passed: %v", err)
	}
}
