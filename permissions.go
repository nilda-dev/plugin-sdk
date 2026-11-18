package nilda

// STAFF PERMISSIONS (v0.70.0, EXPERIMENTAL). Roles live in Core and only in Core: a plugin never stores users or
// decides who may do what. What a plugin may do is DECLARE the parts of itself a role can be given, so the owner can
// make "Order handler" — sees orders, ships them, cannot refund or change the shop's settings — on Core's own Roles
// screen, beside every other permission of the site.
//
// In plugin.json:
//
//	"permissions": [
//	  {"key": "orders.view",   "label": "See orders"},
//	  {"key": "orders.fulfil", "label": "Ship orders", "help": "Labels, packing slips, mark shipped"}
//	],
//	"role_presets": [
//	  {"key": "order_handler", "label": "Order handler", "permissions": ["orders.view", "orders.fulfil"]}
//	]
//
// and a `permission` on each admin page (AdminPage.Permission: who may OPEN it), row action, bulk action
// (RowAction.Permission, BulkAction.Permission: who may PRESS it), form (PageForm.Permission: who may SAVE it) and
// ability (the manifest's abilities[].permission, Ability.Permission: who may have the assistant run it).
//
// Core registers each as the Core permission `plugin.<your key>.<permission key>`. The plugin's
// `plugin.<key>.configure` — and `plugin.manage` — still opens everything, so the owner loses nothing; an entry
// with no `permission` stays open to them alone. A permission you add in a new version is held by nobody until an
// admin ticks it: nothing is widened silently. Uninstalling drops them from every role.
//
// The assistant acting for a person is held to the same permissions: an ability whose permission the person lacks
// is refused, in the panel's chat and over a connector alike. CallOf(ctx).ActorUserID still names who pressed —
// attribution, never a permission set: checking is Core's.

// MaxManifestPermissions bounds a plugin's declared permissions.
const MaxManifestPermissions = 24

// MaxManifestRolePresets bounds a plugin's suggested roles.
const MaxManifestRolePresets = 6

// PermissionDecl is one part of your plugin a role can be given.
type PermissionDecl struct {
	// Key is lowercase letters, digits, '_' and at most one '.', at most 48 characters — "orders.refund". Never
	// "configure", which is Core's for the whole plugin.
	Key string `json:"key"`
	// Label is what the Roles screen shows beside the box, translated through your translations.
	Label string `json:"label"`
	// Help is one sentence under it: what holding it lets a person do. At most 200 characters.
	Help string `json:"help,omitempty"`
}

// RolePreset is a role you suggest: the Roles screen offers "New role from preset", which fills the role's boxes
// with these permissions for the owner to change before saving. Nothing is created or assigned by itself, and a role
// made from a preset is the site's own from then on.
type RolePreset struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Help  string `json:"help,omitempty"`
	// Permissions are keys of your PermissionDecl list.
	Permissions []string `json:"permissions"`
}
