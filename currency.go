package nilda

import (
	"fmt"
	"strconv"
	"strings"
)

// MONEY IS AN INTEGER IN THE CURRENCY'S SMALLEST UNIT — and "smallest unit" is not always a hundredth.
//
// A yen has no subunit, so ¥1,500 is 1500; a Kuwaiti dinar has three decimal places, so 1.250 KWD is 1250;
// a euro has two, so €12.34 is 1234. A shop that stores every price multiplied by a hundred sends a
// processor 150000 for ¥1,500 — a hundred times the price — and nothing along the way can tell. Every
// payment amount in this SDK is minor units by ISO 4217, converted ONCE, here.
//
// minorUnits is ISO 4217 List One as published on 2026-09-17 by SIX, the standard's maintenance agency
// (https://www.six-group.com — "list-one.xml"), generated from that file rather than typed: every code
// with a numeric minor unit. The thirteen with none ("N.A." — gold, silver, fund units, the test code XTS,
// "no currency" XXX) are absent on purpose: nobody is charged in ounces of gold, and a code this table
// does not know is refused rather than guessed at. To refresh it, regenerate from the current file and
// keep TestTheCurrencyTableIsISO4217ListOne's counts in step.
var minorUnits = func() map[string]int {
	m := map[string]int{}
	for digits, codes := range map[int]string{
		0: `BIF CLP DJF GNF ISK JPY KMF KRW PYG RWF UGX UYI VND VUV XAF XOF XPF`,
		2: `AED AFN ALL AMD AOA ARS AUD AWG AZN BAM BBD BDT BMD BND BOB BOV BRL BSD BTN BWP BYN BZD CAD CDF CHE
			CHF CHW CNY COP COU CRC CUP CVE CZK DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GTQ GYD
			HKD HNL HTG HUF IDR ILS INR IRR JMD KES KGS KHR KPW KYD KZT LAK LBP LKR LRD LSL MAD MDL MGA MKD MMK
			MNT MOP MRU MUR MVR MWK MXN MXV MYR MZN NAD NGN NIO NOK NPR NZD PAB PEN PGK PHP PKR PLN QAR RON RSD
			RUB SAR SBD SCR SDG SEK SGD SHP SLE SOS SRD SSP STN SVC SYP SZL THB TJS TMT TOP TRY TTD TWD TZS UAH
			USD USN UYU UZS VED VES WST XAD XCD XCG YER ZAR ZMW ZWG`,
		3: `BHD IQD JOD KWD LYD OMR TND`,
		4: `CLF UYW`,
	} {
		for _, code := range strings.Fields(codes) {
			m[code] = digits
		}
	}
	return m
}()

// MinorUnits reports how many decimal places the ISO 4217 currency has — 0 for JPY, 2 for EUR, 3 for KWD —
// and false for a code the standard does not give a minor unit to, or does not have. Case and surrounding
// space are forgiven; everything else is refused, because a payment in a currency nobody can name is not a
// payment anybody can settle.
//
// A processor may round differently from the standard in its own API (some take a zero-decimal currency in
// hundredths anyway); that translation belongs in the gateway plugin that knows the processor, never in a
// shop.
func MinorUnits(currency string) (int, bool) {
	d, ok := minorUnits[strings.ToUpper(strings.TrimSpace(currency))]
	return d, ok
}

// ParseMinor turns a decimal amount written the way a person writes it — "12.34", "1500", "1.250" — into
// minor units of currency, exactly: no float is involved at any point.
//
// It REFUSES rather than rounds. More decimal places than the currency has ("12.345" euros) is a rounding
// decision, and a rounding decision is a pricing rule that belongs to whoever set the price, not to the
// code moving the money. A negative or empty amount, a sign, an exponent, a thousands separator or a value
// past what an int64 holds are refused too.
func ParseMinor(amount, currency string) (int64, error) {
	digits, ok := MinorUnits(currency)
	if !ok {
		return 0, fmt.Errorf("nilda: %q is not an ISO 4217 currency with a minor unit", currency)
	}
	s := strings.TrimSpace(amount)
	whole, frac, dotted := strings.Cut(s, ".")
	if whole == "" || !allDigits(whole) || (dotted && (frac == "" || !allDigits(frac))) {
		return 0, fmt.Errorf("nilda: %q is not a plain decimal amount", amount)
	}
	if len(frac) > digits {
		return 0, fmt.Errorf("nilda: %q has more decimal places than %s's %d — round it before it is paid", amount,
			strings.ToUpper(strings.TrimSpace(currency)), digits)
	}
	n, err := strconv.ParseInt(whole+frac+strings.Repeat("0", digits-len(frac)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("nilda: %q is too large to be an amount", amount)
	}
	return n, nil
}

// FormatMinor writes minor units of currency as a plain decimal — 1234 EUR is "12.34", 1500 JPY is "1500",
// 1250 KWD is "1.250" — with no symbol, grouping or locale, which are presentation and the reader's
// language to decide. false for a currency MinorUnits does not know.
func FormatMinor(minor int64, currency string) (string, bool) {
	digits, ok := MinorUnits(currency)
	if !ok {
		return "", false
	}
	neg := minor < 0
	s := strconv.FormatInt(minor, 10)
	if neg {
		s = s[1:]
	}
	if digits > 0 {
		if len(s) <= digits {
			s = strings.Repeat("0", digits-len(s)+1) + s
		}
		s = s[:len(s)-digits] + "." + s[len(s)-digits:]
	}
	if neg {
		s = "-" + s
	}
	return s, true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
