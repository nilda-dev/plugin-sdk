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

### 3.1 `field` — a plugin contributes a content-type field type — **73 plugins**

The single largest category, and the one where the pattern already exists.

A colour picker, a country picker, an icon picker, a map coordinate, a tag input, a rich-text editor,
a UUID generator. Every one is: *render a control in the admin, validate the value, store it, hand it
back on read.*

`widget` already does the equivalent for the page-builder canvas — `widget.describe` returns a typed
config schema and `widget.render` returns sanitized markup, namespaced under the plugin's key so a
plugin can never shadow a Core widget (`internal/plugin/widgets.go`). `field` is that shape pointed at
`internal/contenttype` instead of `internal/pagebuilder`:

* `field.describe` → the field type's key, label, and its own settings schema
* `field.render` → the admin control (sanitized, namespaced)
* `field.validate` → accept or reject a value, with a message

The existing field vocabulary is 26 types (`plugin-sdk/widgets.go`, mirrored by
`internal/plugin/sdk_mirror_test.go` in both directions), so the seam for "what a field IS" is already
written down and guarded.

**Watch:** validation must run in Core's save path, not only in the admin. A field type whose
validation lives in the browser is a field type that stores anything a script posts.

### 3.2 `admin_page` — a plugin contributes an admin screen — **32 plugins**

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

### 3.3 `auth_provider` — a plugin contributes a way to log in — **15 plugins**

Smaller by count and larger by consequence: **it is what currently blocks three separate things.**

Nothing in the nineteen lets a plugin say "this person authenticated, mint them a session". So:

* **SSO (D-34)** — decided as Pro, cannot be a plugin, so it either lives in Core or waits for this.
* **`internal/strongauth`** (passkeys + TOTP, 1,441 lines, pulls `go-webauthn` and `pquerna/otp`)
  cannot move out.
* **`internal/social`** (Google/GitHub login, 953 lines) cannot move out.

This is the capability with the highest security bar in the whole list. A plugin that can mint
sessions can impersonate anyone, so the seam has to hand Core a *verified identity claim* and let
**Core** decide whether a session follows — never let the plugin construct one.

### 3.4 Then, and only then, prove it by extraction

With the three above in place, move the packages listed next to D-34 out of Core and into plugins.
They are the proof the vocabulary is real: if `nilda-graphql` works as a plugin, the `route`
capability is genuinely sufficient; if it does not, we learn it on our own code rather than from a
community developer's bug report.

**This ordering is the owner's correction to an earlier suggestion of mine.** I proposed extracting
GraphQL first, to discover the gaps. The gaps do not need discovering — the table in §1 already lists
them — so extraction is the *verification* step, not the exploration step.

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

### `log` — a plugin has no way to say anything

**Zero** logging symbols in the SDK, and no field for it in the contract. A plugin runs in a separate
process; `fmt.Println` from there lands wherever the host happened to leave stdout pointing. Core
already uses `hashicorp/go-hclog`, which is designed for exactly this and already on the wire — the SDK
simply does not hand it over.

The consequence is not aesthetic. When a plugin misbehaves on somebody's production site, there is no
supported way for its author to have left a trace, and no way for the site owner to give them one.

### `settings` — nowhere for the site owner to put an API key

The manifest has no settings schema and `InitRequest` has no config field. `WidgetDef.Config` exists
but is per-widget instance, not per-plugin.

So the moment a plugin talks to a third party — which is most of the integrations the marketplace
exists for; Sentry, Algolia, Mux, a payment gateway — there is no answer to "where does the owner type
the key?". The plugin's only options today are an environment variable the owner cannot set from the
admin, or its own hand-rolled settings screen, which it also cannot have (§3.2).

Shape it as: the manifest declares a schema (the same `ObjectSchema` the SDK already builds for widget
config and ability inputs), Core renders it, stores it, and passes the values on `InitRequest` —
**with secret-typed fields encrypted at rest and masked on read**, like every other secret setting
(SPEC_04). A plugin config that prints an API key back to the screen is a credential leak with a UI.

### `migrate` — `datastore` creates tables and cannot change them

A plugin declares tables and Core provisions the schema. Version 2 wants one more column, and there is
no mechanism at all. Its author's only route is to run DDL by hand at init, which is the pattern that
gives two instances a race and a half-migrated schema.

Core has a migration runner (`internal/db`) and the plugin has its own isolated schema, so the pieces
exist. What is missing is the contract: ordered, versioned, applied once under a lock, recorded per
plugin — the same properties Core's own migrations have, because a plugin's data deserves them for the
same reasons.

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
