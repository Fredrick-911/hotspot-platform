# ADR 0001 — MikroTik + FreeRADIUS + PostgreSQL for access control

- **Status:** Accepted
- **Date:** 2026-10-13

## Context

Airtel Smart Boxes have no remote API, so we cannot create users or limit
speeds on them. We need per-user speed, time and data limits, usage
accounting, and the ability to disconnect a user remotely.

## Decision

- Put a **MikroTik RouterOS** device behind every Smart Box. Its Hotspot feature
  handles the captive portal redirect and enforces limits in hardware.
- Use **FreeRADIUS 3.x** as the authentication and accounting server.
- Store RADIUS users in **PostgreSQL** using FreeRADIUS's standard schema, so the
  Go backend controls access just by writing rows.
- Model each **plan as a RADIUS group** (`radgroupreply`), and put each paying
  user in their plan's group (`radusergroup`).

## Consequences

- ✅ Industry-standard protocol; limits are enforced on the router, not in our code.
- ✅ The Go backend needs no RADIUS server code. It only needs the database, plus
  a small client for Disconnect/CoA messages.
- ⚠️ Each location needs a MikroTik (extra hardware cost for owners).
- ⚠️ FreeRADIUS uses UDP, so hosting must be a VPS, not a web-only platform.
