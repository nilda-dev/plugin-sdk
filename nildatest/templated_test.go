package nildatest

import (
	"context"
	"errors"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

func shopTemplates() []nilda.EmailTemplateDecl {
	return []nilda.EmailTemplateDecl{{
		Key: "order_confirmation", Title: "Order confirmation",
		Variables: []nilda.EmailVariable{{Name: "order_number", Required: true}, {Name: "lines", Kind: nilda.EmailList},
			{Name: "note"}, {Name: "contract", Kind: nilda.EmailHTML}},
		Defaults: map[string]nilda.EmailTemplateText{
			"en": {Subject: "Order {{ order_number }}",
				Text: "Thanks for order {{ order_number }}.\n{% for l in lines %}{{ l.qty }} x {{ l.name }}\n{% endfor %}{% if note %}Note: {{ note }}{% endif %}{{ contract }}",
				HTML: "<p>Order {{ order_number }}</p>{{ contract }}"},
			"de": {Subject: "Bestellung {{ order_number }}", Text: "Danke für Bestellung {{ order_number }}."},
		},
	}}
}

// A plugin's tests read the real words of its declared mail: the fake renders the manifest's default in the
// locale asked (falling back to English) and records what Core would log — key, ref, locale and data.
func TestTheFakeRendersTheDeclaredDefault(t *testing.T) {
	core, host := New("shop", "email")
	host.UseEmailTemplates(shopTemplates())
	ctx := context.Background()
	data := map[string]any{"order_number": "1042", "lines": []any{map[string]any{"qty": 2, "name": "Tea"}},
		"contract": map[string]any{"html": "<p>Terms</p>", "text": "Terms."}}
	if err := core.SendTemplatedEmail(ctx, nilda.TemplatedEmail{Key: "order_confirmation", To: "a@b.test",
		Locale: "fr", Data: data, Ref: "order:1042", ReplyTo: "shop@b.test", IdempotencyKey: "o1042"}); err != nil {
		t.Fatal(err)
	}
	got := host.Emails()
	if len(got) != 1 {
		t.Fatalf("recorded %d mails", len(got))
	}
	e := got[0]
	if e.Key != "order_confirmation" || e.Ref != "order:1042" || e.Locale != "en" || e.Data["order_number"] != "1042" ||
		e.ReplyTo != "shop@b.test" || e.IdempotencyKey != "o1042" {
		t.Fatalf("recorded %+v", e)
	}
	if e.Subject != "Order 1042" || e.Body != "Thanks for order 1042.\n2 x Tea\nTerms." || e.HTML != "<p>Order 1042</p><p>Terms</p>" {
		t.Fatalf("the fake did not render the default: %q / %q / %q", e.Subject, e.Body, e.HTML)
	}
	if err := core.SendTemplatedEmail(ctx, nilda.TemplatedEmail{Key: "order_confirmation", To: "a@b.test", Locale: "de",
		Data: data}); err != nil || host.Emails()[1].Subject != "Bestellung 1042" {
		t.Fatalf("German: %v %+v", err, host.Emails())
	}
	if err := core.SendTemplatedEmail(ctx, nilda.TemplatedEmail{Key: "gift_card", To: "a@b.test"}); err == nil {
		t.Fatal("an undeclared template was sent")
	}
}

// A switched-off template answers ErrTemplateOff, as Core does, and records nothing.
func TestASwitchedOffTemplateIsErrTemplateOff(t *testing.T) {
	core, host := New("shop", "email")
	host.UseEmailTemplates(shopTemplates())
	host.SwitchOffTemplate("order_confirmation")
	err := core.SendTemplatedEmail(context.Background(), nilda.TemplatedEmail{Key: "order_confirmation", To: "a@b.test",
		Data: map[string]any{"order_number": "1"}})
	if !errors.Is(err, nilda.ErrTemplateOff) || len(host.Emails()) != 0 {
		t.Fatalf("err %v, recorded %d", err, len(host.Emails()))
	}
	noCap, _ := New("shop")
	if err := noCap.SendTemplatedEmail(context.Background(), nilda.TemplatedEmail{Key: "x", To: "a@b.test"}); err == nil {
		t.Fatal("a plugin without `email` sent a templated mail")
	}
}
