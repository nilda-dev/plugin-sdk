package nilda

// EVENTS THE SITE CAN LISTEN FOR (v0.43.0, Core's M3 card A9).
//
// A plugin's event reached other plugins and nobody else. Declared in plugin.json's "emits", it joins Core's own
// catalogue: the owner can start an automation on "Shop: Order paid" and an outside system can subscribe a webhook
// to it, signed and delivered as Core's own events are. An event emitted without a declaration still reaches the
// plugins that subscribe to it, and nothing else.

// EventDecl is one event a plugin declares it emits, as plugin.json's "emits" lists it.
type EventDecl struct {
	// Type is the name Emit sends it under: your key's namespace ("nilda_shop.order_paid") or a capability's
	// ("ecommerce.order_paid").
	Type string `json:"type"`
	// Title is what the owner reads in the automation and webhook pickers, after your plugin's name:
	// "Shop: Order paid".
	Title string `json:"title"`
	// Fields are the payload's fields an automation or a receiver can rely on.
	Fields []EventField `json:"fields,omitempty"`
}

// EventField is one field of a declared event's payload.
type EventField struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
