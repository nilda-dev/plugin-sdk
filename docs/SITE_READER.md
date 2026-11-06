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
plugin restarted since `start` answers `reader_restarted`, never a finished read. **Limits:** a piece ≤ 4 MiB, one element ≤ 1 MiB;
Core also bounds the whole read. **Every size is of the JSON bytes on the wire, as the SDK's dispatch encodes the answer**
(`DispatchServiceHook`): `<`, `>` and `&` are written as the characters themselves, not HTML-escaped to six bytes each (U+2028 and
U+2029 stay escaped), and Core measures the bytes it reads. A piece's members are `json.RawMessage`, which the dispatch only compacts:
an escape the plugin wrote into a member itself goes on the wire as written and counts at its written length, so a reader measures
its members as it encodes them and encodes them without HTML escaping to get the full bounds.

**What Core does not take from the plugin:** the media policy and whether the site's hosts are the owner's, the site key that
namespaces the import's source map, who read it, and the caps — Core derives them from the address the owner approved and its own
checks, and cleans every text again on arrival. **Credentials:** the owner's application password is passed per call and held by the
plugin in memory for the read only; it is never logged and never part of a piece. **Sentences:** the plugin returns loss codes and
data; Core writes the sentences, in the reader's language, so a stored warning can be shown again in another one.

Status: experimental until its first plugin ships green on it. See `docs/plans/site-import-content/PLAN.md` in Core.

## Refusals: `code: sentence`, said in Core's words

A reader that says no to a call (a wrong password, a file that is not an export, a read it no longer holds, too busy) returns an
error from its method; the SDK hands the error's text to Core as the call's answer. Core reads that text as a **refusal** only in
one shape (Core's `internal/plugin/servicerefusal.go`):

- `code: sentence` — the code, a colon and one space, then the sentence. The **code** matches `^[a-z][a-z0-9_]{4,59}$` (lower case,
  5 to 60 characters, starting with a letter) **and contains an underscore**: `reader_busy`, `export_too_large`. The underscore is
  what tells a deliberate refusal from an ordinary Go error (`x509: …`, `dial tcp: …`, `json: …`), which can name an internal host
  and is never shown: any text of another shape stays a plain error, the owner sees Core's generic failure and the text goes to the
  log only. Only the method's own error is read so; a panic, a deadline or a transport error is never a refusal.
- The **sentence** is English, one short sentence, and never quotes the site's content (a title, a body, a file name from the
  export) or any credential, address or host. Core reads at most 64 KiB of it and shows at most 300 characters (runes), after
  cleaning it: Unicode folding (NFKC), entities decoded, markup taken out, any `<`, `>` left made a space, every address cut
  (anything with `://`, a known scheme such as `mailto:`, `www.…`, `user@host`, an IP or host name followed by a port or a path),
  control, bidi and invisible characters removed. Core also takes the owner's own secret values out of it; that is Core's safety net,
  not leave to send them.
- **What the owner reads.** For a code Core knows (`reader_restarted`, `reader_busy`, the export file's and the live read's codes),
  Core's own sentence, translated into the reader's language; the plugin's sentence is not used. For any other code, the plugin's
  cleaned sentence quoted inside a translated frame of Core's ("The import plugin stopped the read and gave this reason, in its own
  words: “…”. Nothing was imported; …"): the frame is translated, the plugin's words are not. A refusal with nothing left to show
  after cleaning is the generic failure. The code goes to Core's log; the sentence never does.

A code that should have its own translated sentence is asked for in Core first, as for a loss. The codes plugin-site-import sends
are listed in its README's [Refusals](https://github.com/nilda-dev/plugin-site-import#refusals-code-sentence-card-p1k) section.

## Losses: codes and data, said in Core's words

A loss is `{code, count, data}`; the plugin never sends a sentence. Core writes it, in the reader's language, from the code:

- **A code Core knows.** When `code` is one of Core's own not-carried reasons (they start with `nc_`: `nc_sticky`, `nc_post_types`,
  `nc_trashed`, …) and `data` holds `"kind"` (a lowercase word such as `"flag"`, `"post_type"`, `"field"`) and, usually, `"name"`
  (text), Core takes it as its own entry, so the sentence — and its translation — is the one the built-in WordPress reader's loss
  has always had. The name is made plain text and cut to 100 characters on arrival.
- **Anything else** (an unknown code, a code that does not start with `nc_`, a missing or malformed `kind`, or `nc_vocab_refused`,
  which is Core's own phase refusing a vocabulary) is said with one generic sentence that carries the code and the data
  ("The reader reported N things it could not carry (…)"). It is never dropped and never repeated as the plugin wrote it.
- `count` is clamped to 1…1,000,000; at most 1,000 losses ride in one piece.

The set of codes Core has sentences for is Core's (`internal/importer/notcarried*.go` and the sentences registered beside the
readers); a plugin that wants a new sentence asks for a new code in Core first.
