# ADR 0002 — Plan time and data are enforced per purchase, not per plan

- **Status:** Accepted
- **Date:** 2026-10-24

## Context

ADR 0001 put each plan in a RADIUS group and sent the plan's `Session-Timeout`
and `Mikrotik-Total-Limit` on every login. Both attributes restart from the
full value on every login, because the MikroTik only knows about the current
session. A user could disconnect and reconnect to get a fresh hour or a fresh
500 MB.

## Decision

- Each purchase gets its **own RADIUS username**. Usage is counted per username.
- The purchase's allowance is stored as **check items** in `radcheck`:
  - `Expiration` = plan end as **unix seconds**.
  - `Max-Total-Octets` = total bytes allowed (our own 64-bit attribute; omitted = unlimited).
- FreeRADIUS computes what is **left** on every login:
  - time: the built-in `expiration` module replies `Session-Timeout` = seconds left, or rejects.
  - data: a `sqlcounter` over `radacct` rejects when used >= allowed, else replies
    `Mikrotik-Total-Limit` = bytes left. `policy.d/hotspot.tmpl` adds
    `Mikrotik-Total-Limit-Gigawords` because the base attribute is 32-bit.
- Plans (`radgroupreply`) keep only the speed limit and `Acct-Interim-Interval`.
- `internal/session.Reaper` is the backstop: it finds sessions still online
  after their time or data ran out and asks for a Disconnect-Request (CoA).

## Consequences

- Reconnecting never resets a plan. Verified by `scripts/wsl/04-test-radius.sh`.
- Plans over 4 GiB work. `sqlcounter` alone silently truncates them (a 10 GiB
  plan was sent as 2 GiB), which is why the policy exists.
- `Expiration` must be unix seconds. A date string is read in the server's
  local time zone and shifts every plan by the zone offset.
- Usernames must be unique per purchase and use only `a-z`, `0-9`, `-`
  (`session.NewCredentials` does this). Reusing a username carries old usage
  into the new plan. A future MAC-based login would need counters that only
  count usage since the purchase.
- The plan clock starts at **payment**, not at first login. Starting at first
  login would need a different counter and is a product decision.
- Data left is read from `radacct`, which can lag up to one interim interval
  (60 s). A router that loses power without a Stop packet can leak up to that much.
- The counter query is duplicated in `data_quota.tmpl` and `policy.d/hotspot.tmpl`,
  because `sqlcounter` does not expose its result. Change both together.