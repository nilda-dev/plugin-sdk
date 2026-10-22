package nildatest

import (
	"context"
	"fmt"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// CheckPersonalDataHolder runs one privacy round against h the way Core does — privacy.export, privacy.erase, then
// privacy.export again to check the erasure — and returns what Core would report to the site's owner as wrong:
//
//	if err := nildatest.CheckPersonalDataHolder(ctx, plugin, "guest@example.com"); err != nil { t.Fatal(err) }
//
// Seed at least one record for email first: a holder that finds nothing proves nothing. Beyond Core's own checks it
// holds the rules docs/PLUGIN_SDK.md states: every record found is accounted for by the erasure (erased, kept or
// held), a held record is one the export returned, and erasing again changes nothing more.
func CheckPersonalDataHolder(ctx context.Context, h nilda.PersonalDataHolder, email string) error {
	req := nilda.PrivacyRequest{Emails: []string{nilda.NormalizePrivacyEmail(email)}, RequestID: "nildatest-privacy"}

	before, err := exportChecked(ctx, h, req)
	if err != nil {
		return err
	}
	if len(before.Records) == 0 {
		return fmt.Errorf("nildatest: privacy.export found nothing for %s — seed a record first; an empty round proves nothing", req.Emails[0])
	}
	found := map[string]bool{}
	for _, r := range before.Records {
		found[r.Kind+"\x00"+r.ID] = true
	}

	erased, err := eraseChecked(ctx, h, req)
	if err != nil {
		return err
	}
	held := map[string]bool{}
	for _, x := range erased.Held {
		if !found[x.Kind+"\x00"+x.ID] {
			return fmt.Errorf("nildatest: privacy.erase holds %s %s, which privacy.export never returned", x.Kind, x.ID)
		}
		held[x.Kind+"\x00"+x.ID] = true
	}
	for _, x := range erased.Kept {
		if !found[x.Kind+"\x00"+x.ID] {
			return fmt.Errorf("nildatest: privacy.erase kept %s %s, which privacy.export never returned", x.Kind, x.ID)
		}
	}
	if n := erased.Erased + len(erased.Kept) + len(erased.Held); n != len(before.Records) {
		return fmt.Errorf("nildatest: privacy.export returned %d records and privacy.erase accounts for %d (erased %d, kept %d, held %d)",
			len(before.Records), n, erased.Erased, len(erased.Kept), len(erased.Held))
	}

	after, err := exportChecked(ctx, h, req)
	if err != nil {
		return err
	}
	for _, r := range after.Records {
		if !held[r.Kind+"\x00"+r.ID] {
			return fmt.Errorf("nildatest: after privacy.erase, privacy.export still returns %s %s, which the erasure did not declare held — Core reports this plugin's erasure incomplete", r.Kind, r.ID)
		}
	}

	again, err := eraseChecked(ctx, h, req)
	if err != nil {
		return err
	}
	if again.Erased != 0 || len(again.Kept) != 0 {
		return fmt.Errorf("nildatest: a second erase with the same request erased %d and kept %d more — erasing twice must change nothing",
			again.Erased, len(again.Kept))
	}
	return nil
}

func exportChecked(ctx context.Context, h nilda.PersonalDataHolder, req nilda.PrivacyRequest) (nilda.PrivacyExport, error) {
	out, err := h.ExportPersonalData(ctx, req)
	if err != nil {
		return out, fmt.Errorf("nildatest: privacy.export: %w", err)
	}
	if err := out.Validate(req); err != nil {
		return out, fmt.Errorf("nildatest: privacy.export: %w", err)
	}
	return out, nil
}

func eraseChecked(ctx context.Context, h nilda.PersonalDataHolder, req nilda.PrivacyRequest) (nilda.PrivacyErasure, error) {
	out, err := h.ErasePersonalData(ctx, req)
	if err != nil {
		return out, fmt.Errorf("nildatest: privacy.erase: %w", err)
	}
	if err := out.Validate(); err != nil {
		return out, fmt.Errorf("nildatest: privacy.erase: %w", err)
	}
	return out, nil
}
