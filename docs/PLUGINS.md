# Nilda CMS — Plugins (NOT Core — build LATER)

> **⚠️ Read this first:** Nothing in this file is Core. **Do NOT build any of it
> until Core is 100% finished.** This file exists only so the
> plugins are recorded in one place and kept OUT of the Core build. When building
> Core, ignore this file entirely.
>
> **Why plugins are separate:** Core must stay lean and extremely fast. A plugin
> is anything that is optional, not needed by every site, or too heavy to run on
> every request. Plugins run as separate sandboxed **gRPC** processes — if one
> crashes or is slow, Core and the site keep running.
>
> **Plugins are Go-only (for now):** per the Go-only plugin policy (SPEC_98), every
> plugin here is written in **Go** and ships as a self-contained binary — no extra
> runtime on the end user's server. This includes the big ones below (E-commerce,
> Newsletter & Membership, etc.): they are large Go plugins on top of Core, exactly
> like WooCommerce is a big plugin on top of WordPress. Multi-language plugins could
> be enabled later, but are out of scope now. Revisitable.

---

## The rule that decides Core vs Plugin

```
Ask two questions about a feature:

1. Does the CMS fail to work without it?  (database, auth, content, media…)
2. Does it run on the hot path — on every request/page load?
   (cache, SEO render, main redirect…)

YES to either  → Core (must be native + fast, no way around it)
NO to both     → Plugin (optional, sandboxed, only loaded if installed)

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
A plugin that needs its own data gets its OWN scoped store via capabilities
(`kv` → Dragonfly, `datastore` → a dedicated Postgres schema) — isolated from Core's
tables. Each plugin ships from its OWN git repo against a versioned plugin-sdk (SPEC_98).

┌──────────────── Core (fast, native) ────────────────┐
│  Stable API Layer  +  event bus  +  gRPC host       │
└───────────────┬─────────────────────────────────────┘
                │ gRPC (separate process, sandboxed)
                ▼
        ┌───────────────┐
        │    Plugin     │  ← optional, isolated
        └───────────────┘

Key performance rule: plugins listen to events ASYNC. Core does its work and
fires an event; the plugin reacts afterward. The visitor never waits for a
plugin. A crashing/slow plugin can't take down the site.
```

---

## Our own plugins (we build + sell these — Phase 2, after Core)

| Plugin | What it does | Model |
|--------|-------------|-------|
| **Newsletter & Membership** (flagship) | Ghost-style: bulk newsletters, visual drag-drop email builder (MJML), A/B subject testing, campaign analytics, free/paid membership tiers, paywall/content gating, Stripe payments, offers/promotions, subscriber analytics. (Core ships the full transactional email layer + deliverability + base signup form + basic subscriber count; this plugin adds marketing-email tooling.) | Paid |
| **E-commerce** | Products, cart, orders, checkout, Stripe + PayPal gateways, inventory. Also brings e-commerce SEO: Product schema (price/stock/GTIN/MPN), shopping/merchant feed, variant canonicals, out-of-stock handling. | Paid |
| **SEO Pro** | The Pro tier of the built-in SEO (also sold standalone). Adds off-site tools that need external data: backlink monitoring, disavow, competitor analysis. | Paid |
| **Local SEO** | Google Business Profile sync, NAP consistency, store locator + map, multi-location landing pages, per-location schema | Paid |
| **Booking & Reservation** | Appointments, slots, calendar, reminders | Paid |
| **Digital File Sales** | Sell + deliver downloadable files, license keys | Paid |
| **Video / Media Pro** | Heavy media beyond Core: video transcoding, HLS adaptive streaming, auto-subtitles (speech-to-text), custom video player, asset rights-management/expiry (enterprise DAM) | Paid |
| **Enterprise Auth / SSO** | Enterprise identity beyond Core auth: SAML SSO, OIDC provider, SCIM directory sync, LDAP/Active Directory, adaptive/risk-based auth. (Core already ships passkeys, TOTP, magic link, social login, RBAC — this is for enterprise buyers with an IdP.) | Paid |
| **Analytics (privacy-first)** | Heavier analytics beyond built-in: heatmaps, session recording, A/B testing, real-user site-speed monitoring (RUM), deeper product analytics | Paid/Freemium |
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
- A/B Testing
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

## The Marketplace (an open bazaar — Phase 2)

Plugins and themes above are sold/distributed through the Nilda Marketplace
(part of nilda.dev, built in Phase 2 — see NILDA_PRD.md §10). Each creator picks
their own model: **free, paid, or freemium** (free base + paid Pro). Ours = 100%
margin; third-party = we take 30% commission. Free items cost nothing. This is
like the WordPress plugin directory or an app store — every model coexists.

---

## Reminder

**Build order:** Core first (all Core SPECs), fully finished and tested, THEN start
plugins. When writing/So building Core, this file is out of scope. It's here only
so nothing is forgotten and so Core stays clean.
