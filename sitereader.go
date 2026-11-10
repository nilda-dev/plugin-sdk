package nilda

import "encoding/json"

// A PLUGIN THAT READS ANOTHER PLATFORM'S SITE (SITE_READER.md; site-import stage 2, plan docs/plans/site-import-content/PLAN.md in
// Core). EXPERIMENTAL.
//
// The reader PROVIDES site.reader/v1; only Core's importer asks it. It reads a WordPress export file or a live WordPress (later
// Shopify, Wix, Squarespace) and answers in pieces of the neutral description in package siteimport; Core checks and caps every piece,
// writes the site as drafts and keeps the report, the publish act and the undo.

// ContractSiteReader is the contract's name.
const ContractSiteReader = "site.reader/v1"

// The contract's methods.
const (
	SiteReaderSources = "sources"
	SiteReaderStart   = "start"
	SiteReaderFeed    = "feed"
	SiteReaderNext    = "next"
	SiteReaderCancel  = "cancel"
)

// The contract's bounds.
const (
	// SiteReaderPieceMax is the most one piece of a read holds, in bytes; SiteReaderElementMax the most one element of it (one item,
	// one term) — a longer body is cut by the reader and named as a loss.
	SiteReaderPieceMax   = 4 << 20
	SiteReaderElementMax = 1 << 20
	// SiteReaderChunkMax is the most of the uploaded file one feed call carries, before base64.
	SiteReaderChunkMax = 1 << 20
	// SiteReaderIdleMinutes is how long a read nobody asks for lives; then the reader drops it and everything it held.
	SiteReaderIdleMinutes = 30
	// SiteReaderRestarted is the error next answers when the plugin was restarted since start: never "last".
	SiteReaderRestarted = "reader_restarted"
	// SiteReaderSnapshotVersion is the version of Core's snapshot encoding this contract's pieces use. Core compares it for equality:
	// a reader that names another number is refused as a whole. Version 5 added an author's bio and link (siteimport.Author).
	SiteReaderSnapshotVersion = 5
)

// The kinds of a source and of a field the owner fills in.
const (
	SiteSourceFile = "file"
	SiteSourceLive = "live"

	SiteFieldText   = "text"
	SiteFieldURL    = "url"
	SiteFieldSecret = "secret"
	SiteFieldFile   = "file"
	SiteFieldChoice = "choice"
)

// SiteReaderSourcesRequest asks which platforms the plugin reads; Locale is the reader's language, for the labels.
type SiteReaderSourcesRequest struct {
	Locale string `json:"locale,omitempty"`
}

// SiteReaderChoice is one option of a choice field.
type SiteReaderChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// SiteReaderField is one thing the owner fills in; Core draws it.
type SiteReaderField struct {
	Name     string             `json:"name"`
	Label    string             `json:"label"`
	Type     string             `json:"type"`
	Required bool               `json:"required,omitempty"`
	Choices  []SiteReaderChoice `json:"choices,omitempty"`
}

// SiteReaderSource is one thing the plugin can read: a platform and a way of reading it.
type SiteReaderSource struct {
	Key      string            `json:"key"`
	Label    string            `json:"label"`
	Platform string            `json:"platform"`
	Kind     string            `json:"kind"`
	Fields   []SiteReaderField `json:"fields"`
}

// SiteReaderSourcesAnswer is the answer to sources.
type SiteReaderSourcesAnswer struct {
	SnapshotVersion int                `json:"snapshot_version"`
	Sources         []SiteReaderSource `json:"sources"`
}

// SiteReaderStartRequest begins one read. Input holds the values of the source's fields by name.
type SiteReaderStartRequest struct {
	Source string                     `json:"source"`
	Input  map[string]json.RawMessage `json:"input"`
	Locale string                     `json:"locale,omitempty"`
}

// SiteReaderStartAnswer names the read.
type SiteReaderStartAnswer struct {
	ReadID          string `json:"read_id"`
	SnapshotVersion int    `json:"snapshot_version"`
}

// SiteReaderFeedRequest is one chunk, in order, of the uploaded export file, base64; Last closes the file.
type SiteReaderFeedRequest struct {
	ReadID string `json:"read_id"`
	Seq    int    `json:"seq"`
	Chunk  string `json:"chunk"`
	Last   bool   `json:"last,omitempty"`
}

// SiteReaderFeedAnswer says how far the file has been accepted.
type SiteReaderFeedAnswer struct {
	Accepted int `json:"accepted"`
}

// SiteReaderNextRequest asks for the piece with this sequence number: a repeat returns the same piece.
type SiteReaderNextRequest struct {
	ReadID string `json:"read_id"`
	Seq    int    `json:"seq"`
}

// SiteReaderProgress is where the read is.
type SiteReaderProgress struct {
	Phase string `json:"phase,omitempty"`
	Done  int    `json:"done,omitempty"`
	Total int    `json:"total,omitempty"`
}

// SiteReaderLoss is something the reader could not carry: a code and its data. Core writes the sentence in the reader's language.
type SiteReaderLoss struct {
	Code  string                     `json:"code"`
	Count int                        `json:"count,omitempty"`
	Data  map[string]json.RawMessage `json:"data,omitempty"`
}

// SiteReaderPiece is the answer to next. Piece holds members of siteimport.Site (Core decodes, caps and cleans them); Last says no
// piece follows.
type SiteReaderPiece struct {
	Seq             int                        `json:"seq"`
	SnapshotVersion int                        `json:"snapshot_version"`
	Last            bool                       `json:"last"`
	Piece           map[string]json.RawMessage `json:"piece"`
	Progress        *SiteReaderProgress        `json:"progress,omitempty"`
	Losses          []SiteReaderLoss           `json:"losses,omitempty"`
}

// SiteReaderCancelRequest stops a read.
type SiteReaderCancelRequest struct {
	ReadID string `json:"read_id"`
}

// SiteReaderCancelAnswer is the answer to cancel.
type SiteReaderCancelAnswer struct {
	OK bool `json:"ok"`
}
