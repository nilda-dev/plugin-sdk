package nilda

import (
	"encoding/json"
	"time"
)

// A PLUGIN'S KEPT DATA, AS CORE EXPORTS IT. Uninstalling a plugin keeps its schema; the site's Plugins screen exports
// that schema whole as one JSON file (Core's ExportRetainedDatastore). A plugin that takes such a file in — a successor
// bringing a retired plugin's data into its own tables — decodes it with these types instead of copying Core's.
// dataexport_test.go holds every field to Core's own struct when Core is beside the SDK.
//
// The values, per column, are what Postgres handed Core: a uuid as an array of its 16 bytes, a time as RFC 3339 text,
// a jsonb column as its JSON value, numbers and text as themselves, NULL as null. TableExport.Types names each
// column's Postgres type, so a reader knows which it is.

// SchemaExport is a retained plugin schema. Truncated is true when any part is missing — a table cut at Core's cap,
// or a table never read (named in Skipped) — and a reader that needs the whole must refuse such a file.
type SchemaExport struct {
	Plugin     string                 `json:"plugin"`
	Schema     string                 `json:"schema"`
	ExportedAt time.Time              `json:"exported_at"`
	Truncated  bool                   `json:"truncated,omitempty"`
	Note       string                 `json:"note,omitempty"`
	Skipped    []string               `json:"skipped,omitempty"`
	Tables     map[string]TableExport `json:"tables"`
}

// TableExport is one table: its column names, its rows (each value in its column's place, as raw JSON to decode by
// Types), how many rows the table held (RowCount; fewer rows than that means it was cut), and each column's type.
type TableExport struct {
	Columns   []string            `json:"columns"`
	Rows      [][]json.RawMessage `json:"rows"`
	RowCount  int                 `json:"row_count"`
	Truncated bool                `json:"truncated,omitempty"`
	Note      string              `json:"note,omitempty"`
	Types     map[string]string   `json:"types"`
}
