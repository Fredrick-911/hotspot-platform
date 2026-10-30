# Architecture

## The business in one paragraph

A **box owner** has an Airtel 4G/5G Smart Box and pays for its data. They plug
a **MikroTik router** into it. Members of the public join the MikroTik's WiFi,
land on a **captive portal** at `ownername.yourdomain.com/portal`, pay with
**M-Pesa**, and get internet for a set time, data amount and speed. The
platform keeps a commission and pays the owner the rest.

## Components

```
┌────────────────── Owner's location ──────────────────┐
│  Airtel Smart Box ──Ethernet──► MikroTik router       │
│  (internet via SIM)             • WiFi + Hotspot      │
│                                 • enforces speed/time │
│                                 • RADIUS client       │
└───────────────────────────────────────┬───────────────┘
                                        │ WireGuard tunnel (router dials out;
                                        │ works behind Airtel's CGNAT)
┌──────────────────── Our cloud VPS ────▼───────────────┐
│  Caddy (HTTPS, *.yourdomain.com) ──► Go API           │
│                                       │                │
│  FreeRADIUS ◄──── PostgreSQL ◄────────┤                │
│  (UDP 1812/1813)  radcheck, radreply, │                │
│                   radacct + our tables│                │
└──────────────────────────────────────┬────────────────┘
                                       │ HTTPS callbacks (signed URL)
                                  PayHero → M-Pesa STK
```

| Component | Job |
|---|---|
| **MikroTik** | Runs the WiFi, redirects unpaid users to the portal, asks FreeRADIUS about each login, enforces limits, reports usage |
| **FreeRADIUS** | Answers MikroTik's login questions using the database, works out time and data **left**, stores usage reports |
| **PostgreSQL** | FreeRADIUS's tables + our own (tenants, plans, payments, devices) |
| **Go API** | Owner signup, plans, payments, portal pages; writes each purchase into the RADIUS tables; kicks users when plans end |
| **Caddy** | HTTPS for the main domain and every owner subdomain |
| **WireGuard** | Secure tunnel so routers behind CGNAT can reach our cloud |

## Key flow: buy a plan → get online

1. Phone joins WiFi → MikroTik redirects to `kamau.yourdomain.com/portal`.
2. User picks a plan → Go API sends an M-Pesa STK Push → user enters their PIN.
3. M-Pesa calls our callback URL → Go API marks the payment paid.
4. Go API creates a **unique username for this purchase** and writes it to `radcheck`
   (`Cleartext-Password`, `Expiration`, `Max-Total-Octets`) and `radreply` (speed, report interval) — `session.Provision`.
   The same call records the platform fee (the tenant's commission at that moment) on the payment.
5. Portal logs the phone in through the MikroTik → MikroTik asks FreeRADIUS → FreeRADIUS works out
   **time left** and **data left** → **Access-Accept** with `Session-Timeout`, `Mikrotik-Total-Limit`
   (+ `Gigawords` over 4 GiB) and the speed limit.
6. MikroTik sends usage every 60 s → `radacct`.
7. Time or data used up → MikroTik cuts the user itself. If a session is still online afterwards,
   `session.Reaper` finds it and the Go API sends a Disconnect-Request (backstop).
8. The user reconnects → FreeRADIUS sends only what is **left** (or rejects). A plan never restarts.

## How FreeRADIUS and the Go API share work

The **database is the contract**. The Go API never talks to FreeRADIUS directly.
It writes rows; FreeRADIUS reads them on every login.

| Table | Written by | Read by | Holds |
|---|---|---|---|
| `radcheck` | Go API | FreeRADIUS | per purchase: password, `Expiration` (unix seconds), `Max-Total-Octets` |
| `radreply` | Go API | FreeRADIUS | per purchase: speed limit, report interval |
| `nas` | Go API (router registration) | FreeRADIUS (at start) | each router's address and RADIUS secret |
| `radacct` | FreeRADIUS | FreeRADIUS, Go API | sessions and data used; the data counter sums it |

Why allowances live on the purchase and not on the plan: see [ADR 0002](adr/0002-plan-expiry-and-data-quota.md).

## Dashboards

Three audiences, one Go binary, no JavaScript framework (see [ADR 0003](adr/0003-dashboards-auth-and-security.md)):

| Who | Where | Sees |
|---|---|---|
| **Platform admin** | `/login` → `/admin` | all businesses, platform fees, every payment and router; adds businesses, sets commission, suspends, resets owner passwords |
| **Box owner** (tenant) | `/login` → `/dashboard` | their own sales (paid / platform fee / you keep), customers (online, data used, revoke), plans, routers (+ setup script), announcements |
| **Customer** | `/` (portal) and `/account` on the owner's address | plans and M-Pesa checkout; their pass: time left, data left, receipts |

Rules that keep tenants apart: every owner query filters on the tenant id stored in the logged-in
user's record (the browser never names a tenant); customer cookies are bound to one tenant; admins
and owners use different API prefixes (`/api/admin`, `/api/tenant`).

Live updates (a new sale appears without refreshing) use Server-Sent Events from an in-memory hub.
That works for **one** API process; several API servers would need Redis or PostgreSQL LISTEN/NOTIFY.

Routers report in once a minute (`/api/device/heartbeat`, proved by the router's RADIUS secret) so
the dashboards can show online/offline. FreeRADIUS reads routers from the `nas` table only at start,
so `scripts/ops/radius-sync-clients.sh` (a systemd timer) restarts it when that list changes.

## Decisions

See [adr/](adr/) for why each major choice was made.