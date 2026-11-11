package nilda

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"
)

// countingDriver counts what database/sql asks it for, so the pool's bound is visible without a Postgres.
type countingDriver struct{ open chan struct{} }

type countingConn struct{ d *countingDriver }

func (d *countingDriver) Open(string) (driver.Conn, error) {
	d.open <- struct{}{}
	return countingConn{d}, nil
}
func (countingConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c countingConn) Close() error                      { <-c.d.open; return nil }
func (countingConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func init() { sql.Register("nilda-counting", &countingDriver{open: make(chan struct{}, 64)}) }

// TestTheDatastorePoolIsBounded is the 2026-11-11 review's finding 4: database/sql opens without a limit.
func TestTheDatastorePoolIsBounded(t *testing.T) {
	db, err := sql.Open("nilda-counting", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	LimitDatastorePool(db)
	if got := db.Stats().MaxOpenConnections; got != DatastoreMaxConns {
		t.Fatalf("the pool holds up to %d connections, want %d", got, DatastoreMaxConns)
	}
	var conns []*sql.Conn
	for i := 0; i < DatastoreMaxConns; i++ {
		c, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.Conn(ctx); err == nil {
		t.Fatal("a third connection was opened past the bound")
	}
	for _, c := range conns {
		_ = c.Close()
	}
	if LimitDatastorePool(nil) != nil {
		t.Fatal("a nil pool is not nil")
	}
}
