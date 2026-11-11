package nilda

import (
	"database/sql"
	"time"
)

// YOUR DATASTORE'S CONNECTIONS ARE THE DATABASE SERVER'S CONNECTIONS (`datastore`).
//
// Core.DatastoreDSN logs in as your plugin's own role on the site's Postgres. Core caps that role — at most 20
// connections, a statement at most a minute, a transaction idle at most a minute, a lock waited for at most ten seconds
// (core's internal/plugin/datastore.go, pluginRoleBounds) — so a plugin cannot take the site's own pool, but every
// connection it holds is still one the database server serves, beside the site, its backups and every other plugin.
// database/sql opens as many as it is asked for by default — no limit at all — and Core calls a plugin up to sixteen
// times at once, besides the public traffic on its route: a plugin that opens the DSN and does nothing else runs into
// the role's cap under load, and its own calls then fail (the 2026-11-11 review of the SDK, finding 4).
//
// Bound the pool the moment you open it:
//
//	db, err := sql.Open("pgx", core.DatastoreDSN) // any Postgres driver; the SDK imports none
//	if err != nil {
//		return nilda.InitResult{}, err
//	}
//	nilda.LimitDatastorePool(db)
//
// and keep three rules that no setting can keep for you:
//
//   - Pass the call's ctx to every query (QueryContext, ExecContext, BeginTx): a hook has Core's per-call budget, and
//     a query that outlives it holds a connection nobody is waiting for.
//   - Never hold a transaction — or a connection — while you call Core (core.API(), Services, Payments) or anything
//     over the network. The call may need the very connection you are holding, and both wait until a timeout.
//   - Build SQL with placeholders ($1, $2) and nothing else. A table or column you choose at run time comes from a
//     fixed list in your code, never from a request; nothing a visitor, the owner or a model sends becomes SQL text.

// DatastoreMaxConns is how many connections LimitDatastorePool lets a plugin hold at once. Two: one for the request
// in hand and one for the one beside it; past that a query waits for a free one — inside its ctx — instead of opening
// another on the database server.
const DatastoreMaxConns = 2

// LimitDatastorePool bounds a pool opened on Core.DatastoreDSN to DatastoreMaxConns connections, one kept idle, each
// closed after 30 minutes (5 idle), so a restarted Postgres or a failover is reconnected rather than held broken. It
// returns db. Raise it only as far as you measured you need, and below the role's cap of 20.
func LimitDatastorePool(db *sql.DB) *sql.DB {
	if db == nil {
		return nil
	}
	db.SetMaxOpenConns(DatastoreMaxConns)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return db
}
