package nilda

import (
	"database/sql"
	"time"
)

// YOUR DATASTORE'S CONNECTIONS ARE THE SITE'S CONNECTIONS (`datastore`).
//
// Core.DatastoreDSN opens your own schema on the site's own Postgres, and every connection you hold is one the site
// cannot use: a Nilda Cloud site has a pool of FIVE. database/sql opens as many as it is asked for by default — no
// limit at all — and Core calls a plugin up to sixteen times at once, besides the public traffic on its route. So a
// plugin that opens the DSN and does nothing else can hold every connection the site has, and the site waits for it
// (the 2026-11-11 review of the SDK, finding 4).
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
// in hand and one for the one beside it; past that a query waits for a free one — inside its ctx — instead of taking
// a third from the site.
const DatastoreMaxConns = 2

// LimitDatastorePool bounds a pool opened on Core.DatastoreDSN to DatastoreMaxConns connections, one kept idle, each
// closed after 30 minutes (5 idle), so a restarted Postgres or a failover is reconnected rather than held broken. It
// returns db. Lower it further if you like; raising it spends connections the site needs.
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
