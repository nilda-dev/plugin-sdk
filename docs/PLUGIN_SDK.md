# Nilda CMS — Plugin SDK & Repo Topology

> Companion to SPEC_98 (Plugin System). This doc is the HOME for the `plugin-sdk` module + the
> repo-per-plugin decision referenced by SPEC_98, SPEC_112, and PLUGINS.md. It records what is
> DECIDED and, honestly, what is still OPEN (must be resolved before coding SPEC_98).
>
> **Status:** DESIGN decided; the gRPC contract + SDK code are NOT built yet (SPEC_98 is scheduled
> after SPEC_97; current work is earlier). English-only per the project docs rule.

---

## 1. Purpose

`plugin-sdk` is a standalone, versioned Go module that BOTH Core and every plugin import. It is the
single source of truth for the Core↔plugin gRPC contract, and it hides all gRPC/protobuf plumbing so
a plugin author writes plain Go. Same shape as Terraform providers (each provider its own repo + a
shared `terraform-plugin-sdk`) — on the same hashicorp/go-plugin base.

## 2. Decisions (settled)

- **One plugin model (no tiers).** Every plugin is a Go binary, out-of-process, over gRPC. Light vs
  heavy = only which capabilities it declares (SPEC_98 §0).
- **Repo-per-plugin.** Each plugin ships from its OWN git repo, built against a pinned `plugin-sdk`
  version. Core keeps only `/plugins/_reference/` (a tiny example) for tests.
- **Capability catalog (opt-in, deny-by-default, lazy-provisioned):**
  `content.read` · `users.read` · `media.read` · `events` · `hooks` · `admin.pages` ·
  `datastore` (own Postgres schema, SPEC_02) · `route` (URL prefix, SPEC_113/73) ·
  `kv` (Dragonfly namespace, SPEC_35) · `payments` (SPEC_107).
- **Storage tiers:** small state → `kv`; transactional data → `datastore`; nothing → light.
- **The developer never writes gRPC.** The SDK exposes: a typed `core` client (only the granted
  capabilities are reachable), handler registration (hooks/events/routes/widgets), and `Serve()`
  which does the handshake + serving. gRPC is the wire, not the developer surface.

## 3. Developer surface (target shape — subject to the open questions in §5)

Light plugin (declares nothing heavy → runs light):

```go
// manifest: capabilities: [content.read, hooks]
func main() { nilda.Serve(&SeoTweak{}) }

func (p *SeoTweak) OnContentSaved(ctx nilda.Ctx, c nilda.Content) error { /* react */ return nil }
```

App plugin (declares datastore + route → resident app):

```go
// manifest: capabilities: [datastore, route:/shop, users.read, payments]
func main() { nilda.ServeApp(&Shop{}) }

func (s *Shop) Init(core nilda.Core) error {
    s.db = core.Datastore.DB()              // own schema (scoped credential)
    core.Routes.Mount("/shop", s.router())  // Core reverse-proxies /shop/* here
    core.Events.On("content.published", s.reindex)
    return nil
}
```

The `core` object only exposes the capabilities the manifest declared + the owner approved — the
compiler/autocomplete guide the developer; the runtime denies anything undeclared (deny-by-default).

## 4. Versioning & compatibility

- `plugin-sdk` is semver-versioned. The gRPC contract carries a PROTOCOL VERSION (Terraform-style):
  Core advertises the protocol versions it supports; a plugin built against an incompatible protocol
  is rejected at load with a clear error — no silent breakage.
- A plugin manifest declares: sdk/protocol version, a Nilda compatibility range, and its capabilities.

## 5. OPEN design questions (MUST resolve before coding SPEC_98)

Not yet solved — listed here honestly so they are not discovered mid-implementation. This is the real
remaining risk surface.

1. **The gRPC contract/proto itself** — exact services, messages, streaming vs unary for events.
2. **`route` ↔ admin-UI integration** — the admin is a React SPA (SPEC_76). How does a plugin's admin
   page appear inside it? Server-rendered page reverse-proxied + embedded, an iframe, or a registered
   SPA remote — and how auth/session/CSRF propagate across the proxy. UNSOLVED, non-trivial.
3. **`datastore` migrations** — how a plugin ships + runs migrations for its OWN schema; rollback; how
   Core creates the scoped role and restricts `search_path` to that schema only.
4. **Event delivery guarantees** — at-least-once? behaviour when the plugin was down (replay/backlog)?
5. **Resource-budget negotiation** — how a `route`/`datastore` app requests a higher memory/CPU budget
   and how Core approves + enforces it.
6. **Payments capability surface** — exact SPEC_107 surface exposed to a plugin (tokenized, no PAN).
7. **Hot-path reads** — a storefront page needing live price/stock is served by the plugin via
   `route`; confirm no synchronous Core→plugin call ever sits on Core's OWN hot path.

## 6. Relationships

- **SPEC_98** — the plugin runtime (host, capabilities, sandbox) this SDK is the contract for.
- **SPEC_112** — the first plugins (E-commerce / Form Pro / Booking), each in its own repo against this SDK.
- **SPEC_114 (Headless SDK)** — a DIFFERENT thing (API/mobile client SDK); do not confuse with `plugin-sdk`.
- **PLUGINS.md** — the plugin catalog. **SPEC_110** — marketplace distribution. **SPEC_115** — pre-install security scan.
