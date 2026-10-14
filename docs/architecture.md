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
│                                       ▼                │
│                                     Redis (cache)      │
└──────────────────────────────────────┬────────────────┘
                                       │ HTTPS callbacks
                                  M-Pesa (Daraja)
```

| Component | Job |
|---|---|
| **MikroTik** | Runs the WiFi, redirects unpaid users to the portal, asks FreeRADIUS about each login, enforces limits, reports usage |
| **FreeRADIUS** | Answers MikroTik's login questions using the database, stores usage reports |
| **PostgreSQL** | FreeRADIUS's tables + our own (tenants, plans, payments, devices) |
| **Go API** | Owner signup, plans, payments, portal pages; writes users into RADIUS tables; kicks users when plans end |
| **Caddy** | HTTPS for the main domain and every owner subdomain |
| **WireGuard** | Secure tunnel so routers behind CGNAT can reach our cloud |

## Key flow: buy a plan → get online

1. Phone joins WiFi → MikroTik redirects to `kamau.yourdomain.com/portal`.
2. User picks a plan → Go API sends an M-Pesa STK Push → user enters their PIN.
3. M-Pesa calls our callback URL → Go API marks the payment paid.
4. Go API inserts the user into `radcheck` + `radusergroup` (plan).
5. Portal logs the phone in through the MikroTik → MikroTik asks FreeRADIUS → **Access-Accept** with limits.
6. MikroTik sends usage every 60 s → `radacct`.
7. Time or data used up → MikroTik cuts the user (or Go API sends a Disconnect-Request).

## How FreeRADIUS and the Go API share work

The **database is the contract**. The Go API never talks to FreeRADIUS directly.
It writes rows; FreeRADIUS reads them on every login.

| Table | Written by | Read by | Holds |
|---|---|---|---|
| `radcheck` | Go API | FreeRADIUS | username + password |
| `radusergroup` | Go API | FreeRADIUS | which plan a user is on |
| `radgroupreply` | Go API (when a plan is created) | FreeRADIUS | a plan's limits |
| `radreply` | Go API (special cases) | FreeRADIUS | per-user limits |
| `radacct` | FreeRADIUS | Go API | sessions and data used |

## Decisions

See [adr/](adr/) for why each major choice was made.
