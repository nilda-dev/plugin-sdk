package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// MAIL TEMPLATES A PLUGIN OWNS (v0.43.0, Core's M3 "The mail door").
//
// A plugin's mail used to be words the plugin assembled and handed to SendTransactionalEmail: the site's owner could
// not read them, change them, switch one off or see it in the site's look. A plugin now DECLARES each mail it sends in
// plugin.json's "email_templates" — its words per language, who reads it, receipt or marketing, the data it reads —
// and Core keeps it in the same store as its own mail, where the owner and the assistant edit it. The plugin then
// sends by key with Core.SendTemplatedEmail and the data; Core renders the owner's words.
//
// The key is a public id: Core stores it as "<plugin key>.<key>", a plugin cannot name another's or Core's, and a key
// is never reused for another mail. Removing or renaming a declared variable is a breaking change for every owner who
// edited the template; adding one is not.

// Who reads a mail (EmailTemplateDecl.Recipient): the Email screen groups by it.
const (
	EmailToPerson = "person" // the person the mail is about: the buyer, the member
	EmailToOwner  = "owner"  // the site's owner: an alert
	EmailToOther  = "other"  // somebody else
)

// Which lane a mail takes (EmailTemplateDecl.Lane). The lane is the template's, never the sender's: a plugin cannot
// send marketing as a receipt.
const (
	EmailTransactional = "transactional" // a receipt: urgent, no unsubscribe line, reaches who left marketing
	EmailMarketing     = "marketing"     // the unsubscribe line, held back from who unsubscribed
)

// The kinds of value a template reads (EmailVariable.Kind). Every value is escaped where it is printed; only a variable
// of kind EmailHTML passes markup, kept to Core's mail allow-list (paragraphs, headings, lists, links, images).
const (
	EmailText   = "text"
	EmailHTML   = "html"
	EmailURL    = "url"
	EmailNumber = "number"
	EmailDate   = "date"
	EmailList   = "list"
	EmailObject = "object"
)

// EmailTemplateDecl is one mail a plugin owns, as plugin.json's "email_templates" declares it.
type EmailTemplateDecl struct {
	// Key is the plugin's own name for the mail: lowercase letters, digits and underscores, at most 48 characters.
	Key string `json:"key"`
	// Recipient is EmailToPerson (the default), EmailToOwner or EmailToOther.
	Recipient string `json:"recipient,omitempty"`
	// Lane is EmailTransactional (the default) or EmailMarketing.
	Lane string `json:"lane,omitempty"`
	// Required: the owner cannot switch it off (a legal acknowledgement). Never a marketing mail.
	Required bool `json:"required,omitempty"`
	// Title and Description are what the owner reads on the Email screen.
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Variables are the data the template reads. A default may read only these, and must read every one marked
	// Required — Core refuses an owner's save that drops one, in words.
	Variables []EmailVariable `json:"variables,omitempty"`
	// Defaults are the words per language ("en" is required): what the template starts as, and what "Revert to
	// default" restores. An update refreshes them; the live words follow only while the owner has not edited them.
	Defaults map[string]EmailTemplateText `json:"defaults"`
}

// EmailVariable is one value a template reads.
type EmailVariable struct {
	Name string `json:"name"`
	// Kind is one of EmailText (the default), EmailHTML, EmailURL, EmailNumber, EmailDate, EmailList, EmailObject.
	Kind string `json:"kind,omitempty"`
	// Sample is what a preview and a test send fill it with.
	Sample any `json:"sample,omitempty"`
	// Required: the template must keep reading it.
	Required bool `json:"required,omitempty"`
}

// EmailTemplateText is a template's words in one language, in Core's Liquid: `{{ order_number }}`,
// `{% for line in lines %}…{% endfor %}`, `{% if note %}…{% endif %}`. Subject at most 200 characters; Text and HTML
// at most 32 KB each. HTML is optional: without it Core writes the text as paragraphs in the site's mail layout. A
// variable of kind EmailHTML reads as its "html" half in HTML and its "text" half in Text and Subject.
type EmailTemplateText struct {
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html,omitempty"`
}

// TemplatedEmail is one send of a declared template (Core.SendTemplatedEmail).
type TemplatedEmail struct {
	// Key is the template's key as plugin.json declares it, never prefixed.
	Key string
	To  string
	// Locale is the recipient's language when you know it (the buyer's). Empty lets Core choose: the recipient's own
	// language on the site, then the site's mail language, then English — which is how an owner alert reaches the
	// owner in the language their panel is set to. Core then takes the closest language the template is written in.
	Locale string
	// Data is the template's variables, JSON-shaped. A variable of kind EmailHTML is map[string]any{"html": …,
	// "text": …}.
	Data map[string]any
	// ReplyTo is where a reply goes when the owner set none on the template; empty is the sender's address.
	ReplyTo string
	// Ref names what the mail is about ("order:1042"); the email log keeps it.
	Ref string
	// IdempotencyKey makes a retry safe: the same key within 24 hours sends once.
	IdempotencyKey string
}

// ErrTemplateOff is the answer for a template the owner switched off: nothing was sent, on purpose. It is not a
// failure — record "not sent: switched off by the owner" and go on.
var ErrTemplateOff = errors.New("nilda: the owner switched this mail off, so nothing was sent")

// mailReasonTemplateOff is the reason Core gives a switched-off template (core internal/plugin/mailstatus.go).
const mailReasonTemplateOff = "TEMPLATE_OFF"

// SendTemplatedEmail sends one of your declared templates (plugin.json "email_templates"; requires `email`). Core
// renders the owner's words for it — what they edited on the Email screen, or your default — in the site's mail
// layout, takes the lane from the template, adds the copies the owner asked for, and logs it with Ref.
//
// It answers as SendTransactionalEmail does — nil when a transactional mail was handed to the provider (a marketing
// one: queued), MailAddressBlocked and MailDeferred for a refusal — and ErrTemplateOff (errors.Is) when the owner
// switched the template off.
//
//	err := core.SendTemplatedEmail(ctx, nilda.TemplatedEmail{Key: "order_confirmation", To: order.Email,
//		Locale: order.Locale, Ref: "order:" + order.Number, Data: map[string]any{"order_number": order.Number}})
//	if errors.Is(err, nilda.ErrTemplateOff) { /* not sent: switched off by the owner */ }
func (c *Core) SendTemplatedEmail(ctx context.Context, m TemplatedEmail) error {
	data := m.Data
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("nilda: the mail's data is not JSON: %w", err)
	}
	_, err = c.host.SendTemplatedEmail(ctx, &contract.SendTemplatedEmailRequest{Key: m.Key, To: m.To, Locale: m.Locale,
		DataJson: raw, ReplyTo: m.ReplyTo, Ref: m.Ref, IdempotencyKey: m.IdempotencyKey})
	if st, ok := mailStatusOf(err); ok && st.Code() == codes.FailedPrecondition && mailReason(st) == mailReasonTemplateOff {
		return templateOff{err}
	}
	return err
}

// templateOff is Core's refusal read as ErrTemplateOff, keeping the status for anything that reads it.
type templateOff struct{ err error }

func (t templateOff) Error() string        { return ErrTemplateOff.Error() + ": " + t.err.Error() }
func (t templateOff) Unwrap() error        { return t.err }
func (t templateOff) Is(target error) bool { return target == ErrTemplateOff }
