package nilda

import (
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestTheCurrencyTableIsISO4217ListOne holds the table to ISO 4217 List One as SIX published it on
// 2026-09-17. When it was generated from that file, every three-letter code was checked both ways: the 165
// codes with a numeric minor unit are the table, exactly, and the 13 marked N.A. are refused. The fingerprint
// below is that checked table; an edit by hand changes it. To refresh: regenerate minorUnits from the current
// list-one.xml, check it against the file the same way, and put the new counts and fingerprint here.
func TestTheCurrencyTableIsISO4217ListOne(t *testing.T) {
	t.Parallel()
	var lines []string
	counts := map[int]int{}
	for a := 'A'; a <= 'Z'; a++ {
		for b := 'A'; b <= 'Z'; b++ {
			for c := 'A'; c <= 'Z'; c++ {
				code := string([]rune{a, b, c})
				if d, ok := MinorUnits(code); ok {
					counts[d]++
					lines = append(lines, code+":"+strconv.Itoa(d))
				}
			}
		}
	}
	if want := map[int]int{0: 17, 2: 139, 3: 7, 4: 2}; fmt.Sprint(counts) != fmt.Sprint(want) {
		t.Errorf("minor units per digit count = %v, List One has %v", counts, want)
	}
	sort.Strings(lines)
	const fingerprint = "bbc3743bcc43f718e687816469ca793c6f9a0ad4a535265c8aa00b6780fbf02c"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(lines, "\n")))); got != fingerprint {
		t.Errorf("the table is not the one checked against List One (fingerprint %s) — regenerate it from the file", got)
	}
	for code, want := range map[string]int{"JPY": 0, "KRW": 0, "EUR": 2, "USD": 2, "IRR": 2, "KWD": 3, "BHD": 3, "CLF": 4} {
		if got, ok := MinorUnits(code); !ok || got != want {
			t.Errorf("MinorUnits(%s) = %d, %v; List One says %d", code, got, ok, want)
		}
	}
	// N.A. in List One: metals, fund units, the test code and "no currency". Nobody is charged in them.
	for _, code := range []string{"XAG", "XAU", "XBA", "XBB", "XBC", "XBD", "XDR", "XPD", "XPT", "XSU", "XTS", "XUA", "XXX"} {
		if _, ok := MinorUnits(code); ok {
			t.Errorf("MinorUnits(%s) answered; List One gives it no minor unit", code)
		}
	}
}

func TestMinorUnitsForgivesCaseAndSpaceAndNothingElse(t *testing.T) {
	t.Parallel()
	if d, ok := MinorUnits(" jpy "); !ok || d != 0 {
		t.Errorf("MinorUnits(\" jpy \") = %d, %v", d, ok)
	}
	for _, code := range []string{"", "EU", "EURO", "E UR", "€", "978"} {
		if _, ok := MinorUnits(code); ok {
			t.Errorf("MinorUnits(%q) answered", code)
		}
	}
}

func TestParseMinorIsExactAndRefusesRatherThanRounds(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		amount, currency string
		want             int64
	}{
		{"12.34", "EUR", 1234}, {"12", "EUR", 1200}, {"12.3", "EUR", 1230}, {"0.05", "eur", 5},
		{"1500", "JPY", 1500}, {"1.250", "KWD", 1250}, {"0.0001", "CLF", 1}, {" 7.50 ", "USD", 750},
		{"0", "EUR", 0}, {"92233720368547758.07", "EUR", math.MaxInt64},
	} {
		got, err := ParseMinor(c.amount, c.currency)
		if err != nil || got != c.want {
			t.Errorf("ParseMinor(%q, %s) = %d, %v; want %d", c.amount, c.currency, got, err, c.want)
		}
	}
	for _, c := range []struct{ amount, currency string }{
		{"12.345", "EUR"}, // more places than a euro has: a rounding decision is a pricing rule
		{"12.3", "JPY"},   // a yen has none
		{"-1", "EUR"}, {"+1", "EUR"}, {"", "EUR"}, {" ", "EUR"}, {"1e3", "EUR"}, {"1,000", "EUR"},
		{".5", "EUR"}, {"5.", "EUR"}, {"1.2.3", "EUR"}, {"١٢", "EUR"}, {"0x10", "EUR"},
		{"92233720368547758.08", "EUR"}, // one past what an int64 holds
		{"12.34", "XAU"}, {"12.34", ""},
	} {
		if got, err := ParseMinor(c.amount, c.currency); err == nil {
			t.Errorf("ParseMinor(%q, %q) = %d, want a refusal", c.amount, c.currency, got)
		}
	}
}

func TestFormatMinorWritesThePlainDecimal(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		minor    int64
		currency string
		want     string
	}{
		{1234, "EUR", "12.34"}, {5, "EUR", "0.05"}, {0, "EUR", "0.00"}, {-5, "EUR", "-0.05"},
		{1500, "JPY", "1500"}, {1250, "KWD", "1.250"}, {1, "CLF", "0.0001"},
		{math.MinInt64, "EUR", "-92233720368547758.08"},
	} {
		if got, ok := FormatMinor(c.minor, c.currency); !ok || got != c.want {
			t.Errorf("FormatMinor(%d, %s) = %q, %v; want %q", c.minor, c.currency, got, ok, c.want)
		}
		if c.minor >= 0 {
			if back, err := ParseMinor(c.want, c.currency); err != nil || back != c.minor {
				t.Errorf("ParseMinor(FormatMinor(%d, %s)) = %d, %v", c.minor, c.currency, back, err)
			}
		}
	}
	if got, ok := FormatMinor(100, "XAU"); ok {
		t.Errorf("FormatMinor in a currency with no minor unit answered %q", got)
	}
}
