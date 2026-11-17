package nilda

import (
	"testing"
	"time"
)

// A binned record says when it leaves the bin, and only a binned one does.
func TestABinnedRewardCarriesItsPurgeDay(t *testing.T) {
	purge := time.Now().Add(30 * 24 * time.Hour)
	for _, c := range []struct {
		issued RewardIssued
		ok     bool
	}{
		{RewardIssued{Kind: RewardDiscountRecord, Status: RewardBinned, PurgesAt: &purge}, true},
		{RewardIssued{Kind: RewardDiscountRecord, Status: RewardBinned}, false},
		{RewardIssued{Kind: RewardDiscountRecord, Status: RewardUndone, PurgesAt: &purge}, false},
		{RewardIssued{Kind: RewardDiscountRecord, Status: RewardUndone}, true},
	} {
		issued := c.issued
		if err := (RewardIssuedAnswer{Found: true, Issued: &issued}).Validate(); (err == nil) != c.ok {
			t.Errorf("%+v: %v, want valid %v", c.issued, err, c.ok)
		}
	}
}
