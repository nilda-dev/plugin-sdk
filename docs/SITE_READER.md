# Reading another platform's site — `site.reader/v1`

**Experimental.** A plugin that READS a platform (WordPress export files and live WordPress first; Shopify, Wix, Squarespace
later) hands Core's importer a neutral description of the site in pieces. Core checks each piece, writes the site as drafts and
keeps the report, the publish act and the undo — the plugin writes nothing into Core. The plugin **provides** `site.reader/v1`;
**only Core asks it** (every method is agent class `none`: no assistant tool and no other plugin can start a read with an owner's
password), always one named provider, fallback `block`.

| Method | What it does |
|---|---|
| `sources` | the platforms the plugin reads and the fields the owner fills in for each; Core draws them |
| `start` | begin one read; the same idempotency key returns the same read; an unasked read is dropped after 30 minutes |
| `feed` | the uploaded export file, in order, in chunks of at most 1 MiB; a repeated `seq` is accepted once |
| `next` | the piece with this `seq`: `siteimport.Site` members in snapshot encoding, progress, losses as codes with data |
| `cancel` | stop a read and drop what the plugin holds |

**The model** is `siteimport.Site` (this SDK), the shape Core's snapshot version 4 stores. Every answer names its `snapshot_version`;
Core refuses one it cannot read. **Sequence numbers:** a retry after a timeout returns the same piece, never skips or repeats one; a
plugin restarted since `start` answers `reader_restarted`, never a finished read. **Limits:** a piece ≤ 4 MiB, one element ≤ 1 MiB.

**What Core does not take from the plugin:** the media policy and whether the site's hosts are the owner's, the site key that
namespaces the import's source map, who read it, and the caps — Core derives them from the address the owner approved and its own
checks, and cleans every text again on arrival. **Credentials:** the owner's application password is passed per call and held by the
plugin in memory for the read only; it is never logged and never part of a piece. **Sentences:** the plugin returns loss codes and
data; Core writes the sentences, in the reader's language, so a stored warning can be shown again in another one.

Status: experimental until its first plugin ships green on it. See `docs/plans/site-import-content/PLAN.md` in Core.
