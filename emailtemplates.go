package nilda

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
