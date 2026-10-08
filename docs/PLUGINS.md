# Nilda — Plugins (NOT Core — the catalogue of what belongs in a plugin)

> **Where this stands (checked against the remotes 2026-09-24):** nothing in this file is Core. The plugin
> runtime and this SDK are built, and five first-party plugins are built on them, each in its own repository
> under `github.com/nilda-dev` (`plugin-<name>`): `commerce` (the shop), `booking` (bookings and reservations), `forms` (the
> advanced forms tier), `sso` (sign-in through a company's identity provider) and `frames` (turns a video
> into the image sequence the page builder's `scroll_sequence` widget plays). Everything else below is
> planned, not built. The file keeps these features recorded in one place — and OUT of Core.
>
> **Why plugins are separate:** Core must stay lean and extremely fast. A plugin
> is anything that is optional, not needed by every site, or too heavy to run on
> every request. Plugins run as separate **gRPC** processes — if one
> crashes or is slow, Core and the site keep running.
>
> **Go is the supported language (for now):** per the plugin policy in Core's SPEC_98 §1, Nilda officially
> supports Go plugins only, and every plugin here is written in **Go** and ships as a self-contained binary —
> no extra runtime on the end user's server. This includes the big ones below (E-commerce, Newsletter &
> Membership, etc.): they are large Go plugins on top of Core, exactly like WooCommerce is a big plugin on
> top of WordPress. The protocol itself is not Go's: `docs/ANY_LANGUAGE.md` is the whole contract for a
> plugin in another language, which runs with the same capabilities and isolation — but installs only on a
> site in sideload mode, never through the marketplace, which takes Go builds only. Revisitable.

---

## The rule that decides Core vs Plugin

```
Ask two questions about a feature:

1. Does the CMS fail to work without it?  (database, auth, content, media…)
2. Does it run on the hot path — on every request/page load?
   (cache, SEO render, main redirect…)

YES to either  → Core (must be native + fast, no way around it)
NO to both     → Plugin (optional, fault-isolated, only loaded if installed)

Never split one feature across both (no "hybrid") — it creates sync bugs,
hard debugging, and hard testing. Each feature lives in ONE place.
```

Everything in this file answered **NO to both** → so it's a plugin. Core never
carries this weight; only sites that install a plugin pay its cost.

---

## How plugins connect to Core (so Core stays fast)

```
Core exposes a Stable API Layer (REST + GraphQL) and gRPC hooks/events.
Plugins ONLY talk to that layer — never to Core internals or Core's DB directly.
A plugin that needs its own data gets a scoped store via capabilities (`kv` → Dragonfly,
`datastore` → a Postgres schema Core owns, holding tables Core created from the plugin's
manifest declaration) — isolated from Core's tables. The plugin gets DML and no DDL, so
uninstalling it revokes access and KEEPS the rows: the data is the site owner's. Each plugin ships from its OWN git repo against a versioned plugin-sdk (SPEC_98).

┌──────────────── Core (fast, native) ────────────────┐
│  Stable API Layer  +  event bus  +  gRPC host       │
└───────────────┬─────────────────────────────────────┘
                │ gRPC (separate process, fault-isolated)
                ▼
        ┌───────────────┐
        │    Plugin     │  ← optional, isolated
        └───────────────┘

Key performance rule: Core never waits on a plugin without a limit. Events
fire after Core has done its own work. The hooks a page is rendered through
(widgets, footer scripts, the shop's products) run while it renders, inside
budgets: 5 s a call (PLUGIN_CALL_TIMEOUT), 10 s for every subscriber of one
hook or event together, 1.5 s for everything one public page asks of plugins.
Past a budget the page renders without that plugin, and a crashing or slow
plugin draws nothing where it would have — it cannot take the site down.
```

---

## Our own plugins (we build + sell these)

Built so far: E-commerce (`commerce`), Booking & Reservation (`booking`), the OpenID Connect sign-in part of
Enterprise Auth / SSO (`sso`), the advanced forms tier (`forms`), and `frames` — which is not in this table:
it makes the image sequences Core's `scroll_sequence` widget plays. The rest of this table is planned.

| Plugin | What it does | Model |
|--------|-------------|-------|
| **Newsletter & Membership** (flagship) | Ghost-style: bulk newsletters, visual drag-drop email builder (MJML), A/B subject testing, campaign analytics, free/paid membership tiers, paywall/content gating, paid tiers taken through the site's payment gateway plugins (Core's payment contract, D-84), offers/promotions, subscriber analytics. (Core ships the full transactional email layer + deliverability + base signup form + basic subscriber count; this plugin adds marketing-email tooling.) | Paid |
| **E-commerce** | Products, cart, orders, checkout, inventory. Card payment comes from the site's gateway plugins (Stripe, PayPal — each its own plugin, through Core's payment contract, D-84: built in Core and in plugin-sdk v0.10.0, docs/PAYMENTS.md). Also brings e-commerce SEO: Product schema (price/stock/GTIN/MPN), shopping/merchant feed, variant canonicals, out-of-stock handling. | Paid |
| **SEO Pro** | The Pro tier of the built-in SEO (also sold standalone). Adds off-site tools that need external data: backlink monitoring, disavow, competitor analysis. | Paid |
| **Local SEO** | Google Business Profile sync, NAP consistency, store locator + map, multi-location landing pages, per-location schema | Paid |
| **Booking & Reservation** | Appointments, slots, calendar, reminders | Paid |
| **Digital File Sales** | Sell + deliver downloadable files, license keys | Paid |
| **Video / Media Pro** | Heavy media beyond Core: video transcoding, HLS adaptive streaming, auto-subtitles (speech-to-text), custom video player, asset rights-management/expiry (enterprise DAM) | Paid |
| **Enterprise Auth / SSO** | Enterprise identity beyond Core auth: SAML SSO, OIDC provider, SCIM directory sync, LDAP/Active Directory, adaptive/risk-based auth. (Core already ships passkeys, TOTP, magic link, social login, RBAC — this is for enterprise buyers with an IdP.) | Paid |
| **Analytics (privacy-first)** | Heavier analytics beyond built-in: heatmaps, session recording, deeper product analytics. (A/B testing and real-user site-speed monitoring are **in Core** — the A/B section and its readout ship with the Pro page builder, and RUM vitals are free in `perfvitals`. Both were on this list before they were built.) | Paid/Freemium |
| **AI Support Chatbot (visitor-facing)** | Public site chatbot answering from your content — conversation memory, multi-operator handoff, ticketing. (The ADMIN assistant chatbot is in Core; this heavier visitor/support one is the plugin.) | Paid |
| **Multi-language (advanced)** | Beyond Core i18n — translation management extras | Paid/Freemium |
| **Crypto Dashboard** | Niche industry example | Paid |
| **Bookmark / Reading List** | Visitors save content to read later | Free/Freemium |
| **QR Code / Short URL / Vanity URL** | Short links + QR + click analytics. Its redirect only runs on sites that install it, so Core stays fast. | Free/Freemium |
| **Fediverse / ActivityPub** | Federate with Mastodon & the fediverse (follow, reply, boost). Heavy protocol, niche → out of Core. | Free/Freemium |
| **Heatmap / Session Recording / Form Analytics** | Hotjar-style behavior tracking. Deliberately a plugin — injecting per-visitor tracking is exactly the weight that must NEVER touch Core. | Paid |

---

## Other plugins (us or third parties, later)

- CRM
- ~~A/B Testing~~ — **built into Core**: the A/B section and its sticky per-visitor pick shipped with
  SPEC_121 R8 and have been gated on the Pro page-builder key since 2026-07-24; the per-variant readout
  was built 2026-08-02 and joined that same key on 2026-09-20, so the two halves of one capability sit
  on one entitlement. Struck rather than deleted because it was listed here as a plugin's job for long
  enough that somebody may go looking.
- AI Visibility Monitoring (track how your content appears in ChatGPT / Perplexity / AI Overviews — needs an external service)
- AI Full-Site Builder Agent (autonomous "build my whole site from a brief" agent, Angie-style; runs on Core's AI Abilities — heavier, so it's a plugin)
- AI Sentiment Analysis dashboards (sentiment trends across content + comments)
- Multi-channel AI auto-publish (publish → auto-translate + push to social/other channels)
- Push notifications (FCM / APNs)
- SMS
- Real-time collaboration extras
- Social share buttons
- Quiz / Survey / Poll
- Reviews & Ratings (star ratings, review moderation)
- Community (member directory, follow users, activity feed, badges, gamification, leaderboards)
- Personalization / Content targeting (geo, behavior, A/B)
- Frontend editing / user content submission (UGC)
- Automation (IFTTT-style rules, triggers, scheduled actions) + chat notification
  channels (Slack / Teams / Discord — route Nilda notifications to team chat)
- Report Builder (custom reports, chart builder, scheduled analytics emails)
- Multi-site / multi-brand governance (manage many sites/brands from one install,
  shared content across sites, cross-brand standards + roll-up dashboards — the
  per-site editorial workflow, approval chains, SLA, and content-ops dashboard are
  already Core-Pro; this plugin is for running a fleet of sites/brands together)
- Industry-specific tools

---

## The Marketplace (an open bazaar)

Plugins and themes above are sold/distributed through the Nilda Marketplace
(part of nilda.dev — Central's marketplace, which the admin's Market screen browses
and buys from; see Core's `docs/files/NILDA_PRD.md` §9 "Marketplace" and the first of its two §10s,
"Revenue Model"). Each creator picks
their own model: **free, paid, or freemium** (free base + paid Pro). Ours = 100%
margin; third-party = we take 30% commission. Free items cost nothing. This is
like the WordPress plugin directory or an app store — every model coexists.

---

## Reminder

**Core stays lean.** Anything here answered NO to both questions at the top, so it lives in a plugin and
never in Core — however much of it gets built, and in whatever order. The file is here so nothing is
forgotten and so Core stays clean.
