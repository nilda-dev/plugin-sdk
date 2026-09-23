package nilda

// UPGRADING: the one-time data step.
//
// Your SCHEMA already evolves without you. Core applies your manifest's table declaration on every update —
// additively and idempotently — so a new column, a new table and a new index all arrive, and an existing row
// picks up a declared default. You do not write DDL and you could not if you wanted to: Core owns the
// schema, which is what keeps a plugin from dropping the site owner's orders.
//
// What Core cannot do for you is the DATA. Filling a new column from an old one, normalising a stored value,
// re-keying a row — those are decisions only your code knows how to make. This is how you know when to make
// them, and, just as importantly, when not to.

// IsFirstRun reports whether this is the plugin's first completed start on this site.
//
// Use it for seed data: the default categories, the starter template, the row your plugin cannot work
// without. On an upgrade it is false, so the seed does not overwrite what the owner has since changed.
func (c *Core) IsFirstRun() bool {
	return c != nil && c.PreviousVersion == ""
}

// UpgradedFrom reports whether the plugin was previously running one of the given versions.
//
//	if core.UpgradedFrom("1.0.0", "1.0.1") {
//		// fill price_num from price, once
//	}
//
// It is exact-match on purpose rather than a range: a data step is written against a specific shape, and
// "anything before 1.1" quietly includes versions that never existed and shapes you never shipped.
//
// Init has its own time budget for this — 30 seconds by default, the site's PLUGIN_INIT_TIMEOUT — longer
// than the 5 seconds a hook call gets; a step that needs more should move rows in batches across restarts,
// never in one statement that cannot finish.
//
// RUNS ONCE, NEARLY — write the step so a second run changes nothing. Core records the version only after
// your Init RETURNS, so a plugin the supervisor restarts after a crash is told it is running the version it
// already initialised at, and this is false. If your Init fails, the record is not written and the step runs
// again on the next attempt — the direction you want, because half-finished is the one state you cannot
// detect from inside. And one more case: your Init SUCCEEDED but Core could not write the record (a database
// hiccup at that moment). The plugin stays up — failing a live site over bookkeeping would be worse — and on
// its next start this is true again, so the step runs a second time over data it already moved. "Fill
// price_num where it is still empty" survives that; "multiply every price by 100" does not.
func (c *Core) UpgradedFrom(versions ...string) bool {
	if c == nil || c.PreviousVersion == "" {
		return false
	}
	for _, v := range versions {
		if v == c.PreviousVersion {
			return true
		}
	}
	return false
}

// IsUpgrade reports whether this start follows a DIFFERENT version — any upgrade at all.
//
// For work that is safe to repeat and cheap to check: re-registering something external, warming a cache,
// re-reading a file. For anything that is neither, name the versions with UpgradedFrom.
func (c *Core) IsUpgrade(currentVersion string) bool {
	return c != nil && c.PreviousVersion != "" && c.PreviousVersion != currentVersion
}
