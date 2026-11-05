# Golden sites (site-import stage 2, card K6a)

For each fixture of the importer's corpus — the WXR export files and the fake WordPress's live read — the `Site` that Core's
READER produces, in the snapshot encoding (`writeSite`), pretty-printed. The fake server's random address is replaced by
`https://example.test` (and `example.test` in site keys).

Why files: the plugin `site-import` copies the readers, Core's tests cannot run the plugin's code and the plugin cannot import
Core's, so "the copy reads exactly as Core does" is proved by both repositories reproducing these bytes. `MANIFEST.sha256` is what the
plugin repository copies; a golden cannot change on one side without the other noticing.

Regenerate: `UPDATE_GOLDEN=1 go test ./internal/importer/ -run TestTheGoldenSites` — a statement about the reader, not a guard to move to
go green: say in the commit why a golden changed.
