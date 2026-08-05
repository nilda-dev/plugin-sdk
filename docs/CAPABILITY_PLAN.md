# Nilda — Plugin Capability Plan

> **Do this BEFORE SSO** (`core` D-34). Owner's sequencing, 2026-08-05.
>
> This lives in `plugin-sdk` because the vocabulary is the SDK's contract: a capability is a promise to
> a developer writing Go against `plugin-sdk`, and Core's `internal/plugin/schema_capabilities.go` is
> the half that ENFORCES it. Both halves change together, and `internal/plugin/sdk_mirror_test.go`
> already fails if they drift.
>
> The marketplace infrastructure is built — 7,658 lines of it: gRPC over a separate process, signing,
> a signed package format, `nilda plugin new/build/check/dev/publish`, and a review pipeline. Three
> first-party plugins already run on it (`commerce`, `booking`, `forms`), which is what proves the
> transport works.
>
> What is missing is not infrastructure. It is the VOCABULARY: the list of things a plugin is allowed
> to contribute. Today that list cannot express the two things developers most often build.

---

## 1. The number that decides this

A marketplace whose doors are mostly closed is dead on arrival, and the owner's objection was exactly
that: *"we don't know what a developer wants to build."*

We do not have to guess. **261 unique plugins** exist in Strapi's marketplace (pulled from
`market-api.strapi.io`, 2026-08-04; 2,915 raw records, deduplicated per package). Every one of them is
a developer who wanted to contribute something specific to a headless CMS, and their descriptions say
what. Sorted by what each one needs the host to let it do:

| What the plugin wants to contribute | Plugins | Nilda today |
|---|---:|---|
| **An HTTP route / API surface** | **88** | ✅ `route` |
| **A custom FIELD type** | **73** | ❌ **no capability exists** |
| **An admin UI page or panel** | **32** | ❌ **removed 2026-07-30** |
| **A login / auth provider** | **15** | ❌ **no capability exists** |
| Events / webhooks / monitoring | 10 | ✅ `events` + `hooks` |
| A search provider | 6 | ❌ none |
| Media / image processing | 4 | ✅ `media.write` |
| Translation | 4 | partial (`content.write`) |
| A storage / upload provider | 3 | ❌ none |
| *(uncategorised)* | 26 | — |

**120 of the 219 categorised plugins — 55% — could not be built against Nilda today.**

The three biggest gaps are the first three rows, and they are 120 of the 219 between them.

---

## 2. What Nilda has, and where it stops

Nineteen capabilities exist (`internal/plugin/schema_capabilities.go`):

```
content.read   users.read     media.read      taxonomy.read   menus.read
content.write  media.write    taxonomy.write  menus.write
events         hooks          datastore       kv              schedule
route          widget         render.assets   email           abilities
```

That is a real vocabulary — richer than the "widget only" a first reading of `main.go` suggests, and
`commerce` uses six of them (`datastore`, `route`, `content.read`, `events`, `hooks`, and `payments`,
of which see §5).

The shape of the gap is specific: **a plugin can serve its own pages and own its own data, but it
cannot add anything to a screen Core already renders.** `widget` is the one exception — it puts a
plugin's markup on the page-builder canvas — and it is exactly the pattern the two biggest gaps need
copied.

---

## 3. The work, in priority order

### 3.1 `field` — a plugin contributes a content-type field type — **73 plugins** — ✅ **BUILT 2026-08-06**

The single largest category, and the one where the pattern already exists.

A colour picker, a country picker, an icon picker, a map coordinate, a tag input, a rich-text editor,
a UUID generator. Every one is: *render a control in the admin, validate the value, store it, hand it
back on read.*

`widget` already does the equivalent for the page-builder canvas — `widget.describe` returns a typed
config schema and `widget.render` returns sanitized markup, namespaced under the plugin's key so a
plugin can never shadow a Core widget (`internal/plugin/widgets.go`). `field` is that shape pointed at
`internal/contenttype` instead of `internal/pagebuilder`:

**As built, and the middle line above is wrong.** `field.render` cannot exist: the admin is a React
application, so markup a plugin sends can be displayed but captures no value, and the only thing that would
actually work is the plugin's own JavaScript in the admin origin — a session-stealing primitive handed to
every author. Nothing in this catalogue ships code into a browser, and a field type is not the place to
start.

So a field type is DECLARED, like everything else here. It names one of Core's 26 controls as its `base` and
Core draws that; two hooks carry what a declaration cannot:

* `field.choices` → a picker whose options come from the plugin (a country list, a warehouse list)
* `field.validate` → accept or reject a value, with a message, in CORE's save path

The declaration itself carries the key, the label, the help, the base, and the plugin's own per-field
settings — everything the third hook was for.

The existing field vocabulary is 26 types (`plugin-sdk/widgets.go`, mirrored by
`internal/plugin/sdk_mirror_test.go` in both directions), so the seam for "what a field IS" is already
written down and guarded.

**Watch:** validation must run in Core's save path, not only in the admin. A field type whose
validation lives in the browser is a field type that stores anything a script posts.

*It did run there, and still almost did not.* The registered validator captured the context of the
lifecycle request that triggered the refresh, which is cancelled the moment that request returns — so every
validation afterwards failed with "context canceled" and, by the rule that keeps a crashed plugin from
blocking writers, accepted the value. Silent, total, and invisible to every unit test, because a test's
context is alive when the check runs. Found by installing a real plugin and watching a refused value save.

### 3.2 `admin_page` — a plugin contributes an admin screen — **32 plugins** — **BUILT 2026-08-05**

`admin.pages` existed and was **deleted on 2026-07-30 because it was a name with nothing behind it** —
the capability could be declared and approved, and no code anywhere dispatched it. The deletion was
right. What has to come back is the implementation, not the string.

A plugin that owns data (`datastore`) and serves routes (`route`) still has nowhere to put a human
interface. That is why `commerce` has a `/shop` prefix and no admin screen.

The constraint that makes this safe is the same one `widget` already lives under: the plugin returns
**content, not code**. Core renders it inside its own shell, sanitized, with the plugin's key
namespacing anything it names. An admin page that could ship arbitrary JavaScript into the admin
origin would hand every plugin author a session-stealing primitive.

**Watch:** a capability is not done until something dispatches it. The liveness test added on
2026-07-30 (`capability must dispatch`) exists precisely so this cannot regress into a name again.

**Built, and it came back the way that note demanded — with its dispatch, in one change.** The manifest
declares `admin_pages`; Core validates them at install, registers each field with its own settings store and
each plugin with a `plugin.<key>.configure` permission, serves the sections over `/plugin-admin`, and the
admin renders one screen for all of them (`web/admin/src/screens/PluginPage.tsx`). The plugin ships nothing
into the browser — it declares, Core draws — which is the same constraint `widget` lives under, one step
stricter: a widget returns sanitized HTML, an admin page returns no markup at all.

Four decisions worth keeping:

* **The section is named after the PLUGIN**, not a label it picks. That is what stops two plugins both
  calling their sidebar row "Settings", and it means the owner can always answer "where did this come from?"
  by reading the row.
* **A plugin cannot choose where it sits.** Its section goes below Core's own rows, above Account, ordered by
  name. WordPress lets every plugin pass a menu position; they collide, and the owner ends up with a rail
  nobody arranged. Someone who uses a plugin daily drags it up themselves — the per-user arrangement built
  the same day (`web/admin/src/layout/navlayout.ts`) is what makes that acceptable rather than a limitation.
* **A per-plugin permission**, `plugin.<key>.configure`. Gating on `plugin.manage` would mean the only person
  who can set the shop's payment key is the person who can uninstall Nilda's plugins.
* **Credentials are Core's.** A `secret` field is encrypted at rest, never sent to the browser, and delivered
  to the PLUGIN in plaintext at Init. It cannot have a default — that would ship one shared key to every site
  that installs the plugin — and saving restarts the plugin, so no running process holds a replaced key.

**A plugin's own strings ARE translatable** (built 2026-08-05, the same day the gap was found by opening a
plugin's page with the panel in Persian). The manifest carries `translations`: locale → the declared English
text → the translation, keyed by source exactly as the admin's own catalogue is, so an untranslated string
falls back to what the author wrote rather than to a blank or a key. Resolved in the BROWSER, because the
authority on which language the admin is in is the admin — it is a per-user preference the server never sees
and it changes without a reload. A key is never translated: the action on the wire, the stored value of a
choice and the sidebar id all stay the declared ones, so a translation cannot change what a button does or
where a section sits.

Still to build on this foundation: a `list` page kind, so a shop can show Products and Orders rather than
only Settings. The page-kind field exists and an unknown kind is dropped rather than rendered blank, so it
slots in without a redesign.

### 3.3 `auth_provider` — a plugin contributes a way to log in — **15 plugins** — ✅ **BUILT 2026-08-05**

Smaller by count and larger by consequence: it was what blocked three separate things at once.

* **SSO (D-34)** — decided as Pro, could not be a plugin, so it either lived in Core or waited for this.
  **Built as `nilda-sso` on 2026-08-06** (§3.4), which is what retired the built-in one.
* **`internal/strongauth`** (passkeys + TOTP, 1,441 lines, `go-webauthn` and `pquerna/otp`) could not move out.
* **`internal/social`** (Google/GitHub login, 953 lines) could not move out.

Built on the owner's instruction — **do not write SSO into Core because the plugin system is short; finish
the plugin system so SSO lives in its own repository from day one.** SPEC_98 §5.15 is the record; what
matters for an author is the shape.

**The seam, and why it can be trusted.** A plugin returns an ASSERTION and Core decides everything that
follows. It cannot mint a session, name a Nilda user, set a role, or see the CSRF `state`. On the `oidc`
flow it hands Core the identity provider's own signed `id_token` and CORE verifies it against the keys that
provider publishes — so the guarantee is arithmetic rather than a promise: a hostile plugin would need the
IdP's private key. The `oauth2` flow, for a provider with no signed token, returns attributes marked as
unverifiable.

Four invariants, each of which is a way somebody signs in as a person they are not if it is missing:

1. **Core assigns the namespace** (`<plugin_key>.<local>`), from the ambient plugin key. `user_identities`
   is keyed on the provider name, so a plugin naming itself `google` would inherit everybody who ever
   linked Google — before any email check runs.
2. **The claims come from a verified token**, never from the plugin's attribute fields when a token exists.
3. **The redirect target is validated** against the issuer's discovery document (`oidc`) or the declared
   `network` list (`oauth2`). The start endpoint is public, so an unchecked answer is an open redirect from
   the site's own login button.
4. **The site owner chooses the verifier.** The manifest names WHERE the issuer and client id are typed;
   the values live in Core's settings store, writable only by an administrator.

**Revocation, and the rule to remember: never mint, may revoke.** `core.RevokeIdentity(provider, subject,
reason)` ends every session of the person behind one of your own identities — for back-channel logout, or
whatever your directory tells you. It is safe to delegate for exactly one reason: nothing on the plugin
channel creates authority, so the worst a hostile plugin achieves is signing people out. It is also what
makes SSO mean what it promises, because login-time-only integration cannot deliver "deprovisioned there,
revoked here".

**And the SDK does the protocol for you.** `nilda.ServeAuthProvider` dispatches the three hooks so you write
three methods, and `nilda.NewOIDCClient` does discovery, the authorize URL and the code exchange — the three
HTTP calls every author re-derives and some get wrong. Nilda has the evidence: Core shipped a function
called `OIDC` for a year that threw the ID token away.

### 3.4 Then, and only then, prove it by extraction

With the three above in place, move the packages listed next to D-34 out of Core and into plugins.
They are the proof the vocabulary is real: if `nilda-graphql` works as a plugin, the `route`
capability is genuinely sufficient; if it does not, we learn it on our own code rather than from a
community developer's bug report.

**This ordering is the owner's correction to an earlier suggestion of mine.** I proposed extracting
GraphQL first, to discover the gaps. The gaps do not need discovering — the table in §1 already lists
them — so extraction is the *verification* step, not the exploration step.

#### It worked, and here is what it caught — `nilda-sso`, 2026-08-06

The first real plugin written against `auth_provider` lives in its own repository, and writing it found
**three gaps that every test fixture had missed**, because a fixture is written to fit what exists:

1. **A sign-in plugin cannot name its own directory.** The issuer is a URL the site owner types after
   installing it, different at every company — so `network` would have to be a wildcard, the exact
   declaration the egress policy exists to make unnecessary. Core now allows the issuer host it reads from
   its own settings store. Before the fix, `nilda-sso` could not make one HTTP call with a valid manifest.
2. **A plugin did not know its own route prefix.** The proxy forwards the whole path, so a handler at
   `/backchannel-logout` never saw `/sso/backchannel-logout`. `InitRequest.route_prefix` + `core.Route(...)`.
3. **A machine could not POST to a plugin.** No CSRF token, so 403 — for an identity provider's logout
   notice and for every payment webhook any plugin could serve. A plugin declares `webhook_paths`; Core
   exempts exactly those and sends no identity headers on them.

None of the three is about SSO. All three block any plugin that talks to a third party or receives a
callback, which is most application-class plugins — and none would have surfaced without extracting
something real.

**The built-in generic `oidc` provider was deleted in the same pass.** It did what the plugin does, less
well, from environment variables. Extraction that leaves the original behind is not extraction.

### 3.5 Lower priority, recorded so they are not lost

`search_provider` (6) and `storage_provider` (3) are small categories, and both already have a Core
seam shape (`internal/search` has a Postgres and a Meilisearch backend; `pkg/storage` has filesystem
and MinIO). Neither blocks anything else. They become worth doing when somebody asks.

---

## 3.6 Three SDK gaps that are NOT capabilities

Audited 2026-08-05 across the whole SDK (2,428 lines), the gRPC contract (`contract/plugin.proto`) and
the three first-party plugins. These are not missing capabilities — they are missing *equipment* for
capabilities that already exist, so they make things already promised hard to use.

**Two things I expected to be missing turned out to be there**, and are recorded so nobody re-proposes
them: `Core.DatastoreDSN` hands the plugin its scoped least-privilege DSN (own schema only), so a
plugin opens its own connection with whatever driver it likes; and `route` is fully equipped via
`StartHTTP`, which binds an ephemeral localhost port that only Core's reverse proxy can reach.

### `log` — a plugin has no way to say anything — **BUILT 2026-08-05**

**Zero** logging symbols in the SDK, and no field for it in the contract. A plugin runs in a separate
process; `fmt.Println` from there lands wherever the host happened to leave stdout pointing — and stdout
is not free to write to at all, because it carries the go-plugin handshake.

The consequence was not aesthetic. When a plugin misbehaved on somebody's production site, there was no
supported way for its author to have left a trace, and no way for the site owner to give them one.

**Built:** `nilda.Log()` / `core.Log()` return an `*slog.Logger` (`plugin-sdk/log.go`). The author writes
`slog`, which is the standard library's logger and what Core itself uses; hclog is the wire format between
the two processes and stays an implementation detail, so a plugin does not take hclog into its dependency
graph for one log line. Usable before `Init` and on a nil `Core` — a plugin failing during startup is
exactly when its author most needs to have written something down.

**And it uncovered a Core defect that made the whole path moot.** Proving it end to end showed the call
succeed, the hook run, and the line never arrive. go-plugin has *two* stderr paths: the process pipe,
which Core read, and a gRPC **stdio stream** carrying everything the plugin writes after startup — which
is delivered to `ClientConfig.SyncStderr` and **defaults to `io.Discard`**. Core had never set it, so every
line any plugin had ever written was silently thrown away. Fixed in `core/internal/plugin/pluginlog.go`:
the lines are parsed and re-emitted through Core's own logger, attributed to the plugin by a key **Core**
sets (a line claiming another plugin's name is filed under the one that wrote it), and non-JSON output is
kept at debug rather than dropped — printing crudely is how most people debug.

### `settings` — nowhere for the site owner to put an API key — **BUILT 2026-08-05, as part of `admin_page`**

The manifest had no settings schema and `InitRequest` no config field, so the moment a plugin talked to a
third party — Sentry, Algolia, Mux, a payment gateway — there was no answer to "where does the owner type
the key?".

**It turned out not to be a separate gap.** A settings form with no section to live in is a screen with no
door: it would have had to be jammed into Core's own Settings screen, where an owner cannot tell which rows
came from something they installed and a plugin's credentials sit among Nilda's. Settings is one PAGE inside
§3.2's `admin_page`, and both shipped together.

**Built:** a plugin declares `admin_pages` in its manifest (`AdminPageSpec`); Core validates it at install,
draws it with the panel's own controls, and stores the values in **Core's own settings store** —
`plugin.<key>.<page>.<field>`, group `plugin:<key>`. Nothing new was written for the storage, and that is the
point: encryption at rest, masking on read, exclusion from exports and the audit event are all SPEC_04's,
already reviewed. A `secret` field never reaches the browser (the screen is told only that a value is
configured) and reaches the PLUGIN in plaintext at Init, which is the correct direction. Saving restarts the
plugin, so a running process can never hold a credential the owner has replaced.

Two things the design refuses on purpose: a plugin cannot choose where its section sits in the sidebar (it
goes below Core's rows; a person who uses it daily drags it up themselves), and a secret cannot have a
default (that would ship one shared credential to every site that installs the plugin).

### `migrate` — **RESOLVED 2026-08-05, and the gap was smaller than this entry claimed**

What this entry said: "Version 2 wants one more column, and there is no mechanism at all." That was written
from reading the SDK, and it is **wrong**. Checked against a real Postgres before building anything
(`core/internal/plugin/schemaevolution_test.go`):

* a new COLUMN arrives on update
* a new TABLE arrives on update
* a new INDEX arrives on update
* an existing row picks up the declared DEFAULT rather than a NULL nobody asked for
* re-running the same declaration three times is a no-op, so a relaunch, a re-enable and a double-clicked
  button are all safe

Core's `TableDDL` is additive and idempotent, and an update relaunches the plugin, which re-provisions. So
the schema half was already built; nobody needed a migration runner, and building one would have been a
second mechanism beside a working one.

**What genuinely could not be done, and now can: the DATA half.** Filling a new column from an old one,
normalising a stored value, re-keying a row — decisions only the plugin's code can make. A plugin was never
told it had just been upgraded, so an author's choice was to re-run the work on every launch or to invent a
private version table, and two hundred authors inventing that is two hundred chances to get it wrong.

`InitRequest` now carries `previous_version`, read through `core.IsFirstRun()`, `core.UpgradedFrom(…)` and
`core.IsUpgrade(…)`. Core records it **after Init returns**, which is the point: a plugin the supervisor
restarts after a crash is told it is running the version it already initialised at, so a one-time step does
not run twice. An Init that FAILS leaves the record alone and the step is retried — the right direction,
because half-finished is the one state a plugin cannot detect from inside.

**What remains impossible, deliberately.** Dropping a column, retyping one, dropping a table. No destructive
DDL exists anywhere in the plugin path, which is what makes "could this plugin destroy the site owner's
orders" answerable without reading any SQL. An author needing a different shape declares a new table and
moves the rows with the DML they already have — and now knows exactly when to do it.

### Priority: these come FIRST

Ahead of `field`, `admin_page` and `auth_provider`. Those three add new things a plugin can do; these
three make things ALREADY PROMISED usable. `commerce` declares `datastore` today and has no supported
way to evolve its schema, and no plugin can be configured by the person who installed it.

## 3.7 The SDK repository has to be public before the marketplace opens

Not a capability and not code — a release step, recorded because it is invisible from inside the team and
total from outside it.

`plugin-sdk` is private. On a machine with SSH access to the group, the whole loop works and was verified
end to end on 2026-08-05 from a throwaway module with `GOWORK=off` (so the workspace could not resolve the
local copy and hide a broken pin):

```
nilda plugin new my-seo   →  5 files
go mod tidy               →  downloads gitlab.com/nilda-sdk/plugin-sdk v0.3.0
go build ./...            →  ok
go test ./...             →  ok  my_seo  0.520s
```

A developer outside the group gets `unknown revision` on the first command, because `go get` cannot read a
private repository. There is no error message that explains this and no way for them to work around it.

Nothing to do while building — the owner's call, 2026-08-05 — but it gates the marketplace: on the day
someone outside the team is invited to write a plugin, this has to already be true.

*(Same day, same audit: the `v0.3.0` tag existed only locally and pointed three commits behind — before
`abilities`, before the 7→26 field vocabulary, before the signed-package work. Moving it was safe precisely
because it had never been pushed, so no proxy had ever cached it. `ProtocolVersion` stayed at 2: the changes
were additive.)*

## 4. Definition of done

* Every capability in the table either exists or is recorded here with the reason it does not.
* Each new capability is **dispatched by real code**, proven by the liveness test — a capability that
  can be declared, approved, and does nothing is worse than one that does not exist, because the
  review process then vouches for it.
* Each ships with a first-party consumer, because a seam whose only user is a test is a seam nobody
  has proved.
* `plugin-sdk` exports the Go-side types for each, mirrored both ways by
  `internal/plugin/sdk_mirror_test.go` so the two definitions cannot drift.
* The marketplace review gates (`nilda plugin check`) understand each new capability, including what
  it may NOT do.

---

## 5. One thing to fix on the way past

`commerce/manifest.json` declares **`payments`**, and `payments` **is not one of the nineteen**. The
catalogue's own comment records why it is gone:

> *"a plugin that asked for `payments` was approved to take something no code granted… reserved for a
> SPEC_107 that was never written."*

So a first-party plugin currently declares a capability that does not exist, and — because it is not
in the catalogue — either its install is being approved against a name nothing checks, or it is being
rejected and nobody has noticed. **Verify which, then either remove the line from the manifest or
write the capability.** It is the same failure `admin.pages` had, still live in our own plugin.

---

*Created 2026-08-05. Sequenced BEFORE D-34 (SSO) by the owner. Source data for §1:
`market-api.strapi.io/plugins`, pulled 2026-08-04.*
