package nilda

import "time"

// CORE'S OWN EVENTS (owner decision D10; EventUserEmailVerified, the one already named, is in accountviews.go): what Core itself publishes, by the names it publishes them under, for a
// plugin that subscribes (the `events` capability, returned at Init). A plugin spelling them by hand guessed both the
// names and the data; `coreevents_test.go` holds this list to Core's `events.KnownEvents` when Core is beside the SDK.
const (
	EventContentCreated     = "content.created"
	EventContentUpdated     = "content.updated"
	EventContentPublished   = "content.published"
	EventContentUnpublished = "content.unpublished"
	EventContentTrashed     = "content.trashed"
	EventContentRestored    = "content.restored"
	EventContentDeleted     = "content.deleted"
	EventContentImported    = "content.imported"
	EventFormSubmitted      = "form.submitted"
	EventFormSubscribe      = "form.subscribe"
	// EventUserErased is the push half of a privacy erasure: a person's account was erased (a right-to-be-forgotten
	// request, or the person deleting their own account). Its subject is the account id. A plugin keeping something
	// per account takes it out here instead of asking Core about every account on a schedule.
	EventUserErased = "user.erased"
)

// CoreEventData is the data of one of Core's own events, as a plugin is handed it in HandleEvent: the entity it is
// about and when, beside the event's own fields. EventOf(ctx).Subject is EntityID too. Decode the event's data into
// it (json.Unmarshal); a field an event does not carry stays empty.
type CoreEventData struct {
	// EntityID is the id of what the event is about: a content item's, a form's, an account's.
	EntityID string `json:"entity_id"`
	// EntityKind names what that is: "content", "form", "user".
	EntityKind string `json:"entity_kind"`
	// OccurredAt is when the change was committed.
	OccurredAt time.Time `json:"occurred_at"`
	// Type is a content event's content type key ("page", "post", a plugin's own type); "" for other events.
	Type string `json:"type,omitempty"`
	// Slug is a content event's address slug; "" for other events.
	Slug string `json:"slug,omitempty"`
}
