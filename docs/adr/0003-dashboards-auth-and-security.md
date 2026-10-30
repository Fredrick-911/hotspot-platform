# ADR 0003 — Dashboards, login and security model

**Status:** accepted

## Context
Phase 3 needs three dashboards (platform admin, box owner, customer) on top of the payment/RADIUS core.
The captive portal must work inside a router's walled garden (only our host is reachable), and the
team is small, so operations must stay simple.

## Decisions
1. **Server-rendered pages + small vanilla JS, embedded in the Go binary.** No Node build, no CDN,
   no web fonts: it works in the walled garden, deploys as one file, and a strict
   `Content-Security-Policy` (`script-src 'self'`, no inline script or style) is possible. All data
   is inserted with `textContent`, never `innerHTML`.
2. **Two kinds of login.** Staff (admin/owner): email + password, PBKDF2-SHA256 (600,000 rounds, stdlib),
   HMAC-signed `HttpOnly; SameSite=Lax` cookie, user re-loaded on every request so deactivation is immediate.
   Customers: phone + 4-digit PIN (the PIN shown after payment), cookie bound to one tenant, guessing
   rate-limited per IP and per phone.
3. **Tenant isolation by construction.** Owner endpoints take the tenant from the logged-in user, never
   from the request. Admin and owner APIs live under different prefixes and role checks.
4. **CSRF:** every state-changing browser call must carry `X-Requested-With: hotspot` (plus SameSite cookies).
   Exceptions with their own proof: PayHero webhook (HMAC token in the callback URL, per payment reference) and
   router heartbeat (RADIUS secret).
5. **One function turns a payment into a pass** (`completePayment`), idempotent, called by webhook, status
   poller and the dev shortcut. It also stores `fee_cents` using the tenant's commission at that moment.
6. **Money is measured at payment time.** Changing a commission or a plan never rewrites history; passes keep
   a snapshot of what was sold.
7. **Live updates over SSE from an in-memory hub** — fine for one API server; revisit (Redis / LISTEN-NOTIFY) before scaling out.
8. **Own migration runner** (embedded `.sql`, applied at start, advisory-locked) instead of goose: no extra tool or Up/Down syntax to learn.
9. **FreeRADIUS router list** is read from the `nas` table at start only (verified: reload ignores it), so a
   one-minute timer restarts FreeRADIUS when the list changes.

## Consequences
- Rate limits and live events are per-process memory (lost on restart; not shared between servers).
- A rotated `SESSION_SECRET` logs everyone out and invalidates PayHero callback URLs of payments still pending
  (the status poller still completes them via the PayHero API).