package nilda

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
)

// WHAT A HANDLE PRINTS. Core, API and OIDCClient each hold a credential: the datastore role's password inside
// Core.DatastoreDSN (and the settings the owner typed, which can be an API key), the API's token, the OIDC client's
// secret. A struct prints its fields through fmt — %v, %+v, %#v and every other verb reach unexported ones too — and
// slog's text handler writes a value with %+v, so `core.Log().Info("init", "core", core)` put the Postgres password in
// the operator's log in clear (the 2026-11-16 hunt, finding 1).
//
// So each prints as its String, whatever the verb, and logs as a group of the same safe fields: what the handle IS,
// never what it holds.

const redactedMark = "[redacted]"

// formatRedacted writes s for any verb — gos for %#v — so no verb reaches the struct's own fields.
func formatRedacted(f fmt.State, verb rune, s, gos string) {
	if verb == 'v' && f.Flag('#') {
		_, _ = io.WriteString(f, gos)
		return
	}
	_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), s)
}

// dsnPasswordKV is a password in Postgres's keyword/value form: password=secret or password='a secret'.
var dsnPasswordKV = regexp.MustCompile(`(?i)(\bpassword\s*=\s*)('(?:[^'\\]|\\.)*'|[^\s]*)`)

// redactDSN is a Postgres DSN with its password written as [redacted], in either of Postgres's forms: the URL
// (postgres://role:secret@host/db, or ?password=secret) and keyword/value (host=… password=secret). The rest stays,
// because the host, the database and the role are what an operator reading the line needs.
func redactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.Host != "" {
		if u.User != nil {
			if _, has := u.User.Password(); has {
				u.User = url.UserPassword(u.User.Username(), redactedMark)
			}
		}
		if q := u.Query(); q.Has("password") {
			q.Set("password", redactedMark)
			u.RawQuery = q.Encode()
		}
		// url.String escapes the mark's brackets; the line is for a person to read.
		return strings.ReplaceAll(u.String(), url.QueryEscape(redactedMark), redactedMark)
	}
	return dsnPasswordKV.ReplaceAllString(dsn, "${1}"+redactedMark)
}

// String is the Core as a log line or %v shows it: its key, version, grants and addresses, and the datastore DSN with
// its password redacted. The owner's settings, the API token and the host connection are never printed.
func (c Core) String() string {
	return fmt.Sprintf("{PluginKey:%s NildaVersion:%s Granted:%v DatastoreDSN:%s RoutePrefix:%s SiteURL:%s KVNamespace:%s PreviousVersion:%s}",
		c.PluginKey, c.NildaVersion, c.Granted, redactDSN(c.DatastoreDSN), c.RoutePrefix, c.SiteURL, c.KVNamespace,
		c.PreviousVersion)
}

// GoString is %#v's form of String.
func (c Core) GoString() string {
	return fmt.Sprintf("nilda.Core{PluginKey:%q, NildaVersion:%q, Granted:%#v, DatastoreDSN:%q, RoutePrefix:%q, SiteURL:%q, KVNamespace:%q, PreviousVersion:%q}",
		c.PluginKey, c.NildaVersion, c.Granted, redactDSN(c.DatastoreDSN), c.RoutePrefix, c.SiteURL, c.KVNamespace,
		c.PreviousVersion)
}

// Format prints String for every verb (GoString for %#v), so no verb reaches the DSN's password or the settings.
func (c Core) Format(f fmt.State, verb rune) { formatRedacted(f, verb, c.String(), c.GoString()) }

// coreShown is the part of a Core that may be shown, the DSN's password redacted.
type coreShown struct {
	PluginKey, NildaVersion string
	Granted                 []string
	DatastoreDSN            string
	RoutePrefix, SiteURL    string
	KVNamespace             string
	PreviousVersion         string
}

func (c Core) shown() coreShown {
	return coreShown{PluginKey: c.PluginKey, NildaVersion: c.NildaVersion, Granted: c.Granted,
		DatastoreDSN: redactDSN(c.DatastoreDSN), RoutePrefix: c.RoutePrefix, SiteURL: c.SiteURL,
		KVNamespace: c.KVNamespace, PreviousVersion: c.PreviousVersion}
}

// LogValue logs the Core as a group of String's fields. A nil Core logs as "<nil>".
func (c *Core) LogValue() slog.Value {
	if c == nil {
		return slog.StringValue("<nil>")
	}
	s := c.shown()
	return slog.GroupValue(
		slog.String("PluginKey", s.PluginKey),
		slog.String("NildaVersion", s.NildaVersion),
		slog.Any("Granted", s.Granted),
		slog.String("DatastoreDSN", s.DatastoreDSN),
		slog.String("RoutePrefix", s.RoutePrefix),
		slog.String("SiteURL", s.SiteURL),
		slog.String("KVNamespace", s.KVNamespace),
		slog.String("PreviousVersion", s.PreviousVersion),
	)
}

// MarshalJSON writes String's fields, so a Core inside a value a JSON log handler marshals keeps its password too.
func (c Core) MarshalJSON() ([]byte, error) { return json.Marshal(c.shown()) }
