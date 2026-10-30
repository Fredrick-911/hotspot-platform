# FreeRADIUS configuration

Templates for the files we change in `/etc/freeradius/3.0/`. Everything else
stays as Ubuntu installs it.

| Template | Installed as | Purpose |
|---|---|---|
| `clients.conf.tmpl` | `clients.conf` | Which routers may talk to FreeRADIUS, and their shared secrets |
| `mods-available/sql.tmpl` | `mods-available/sql` | Connects FreeRADIUS to PostgreSQL |
| `dictionary.tmpl` | `dictionary` | Our own attribute `Max-Total-Octets` (64-bit data allowance) |
| `mods-available/data_quota.tmpl` | `mods-available/data_quota` | `sqlcounter`: data used so far, from `radacct` |
| `policy.d/hotspot.tmpl` | `policy.d/hotspot` | Data-left logic, incl. `Mikrotik-Total-Limit-Gigawords` for plans over 4 GiB |
| `sql/seed-dev.sql` | rows in the database | Test plans and users (development only) |

`scripts/wsl/03-configure-freeradius.sh` also enables `data_quota` and adds one
line (`hotspot_quota`) after `logintime` in `sites-available/default`.

`${RADIUS_DB_PASSWORD}`-style placeholders are filled from `.env`.
Placeholders like `${modconfdir}` belong to FreeRADIUS and are left alone.

## How a plan is enforced

A **purchase** is one RADIUS username with these `radcheck` rows (written by `internal/session`):

| Attribute | Example | Meaning |
|---|---|---|
| `Cleartext-Password` | `Kd83mPq0Zx41nB7c` | what the portal submits to the MikroTik login |
| `Expiration` | `1793000000` | plan end, **unix seconds** (never a date string, see below) |
| `Max-Total-Octets` | `524288000` | total data allowed in bytes; leave out for unlimited |

On every login FreeRADIUS sends back what is **left**, so reconnecting never resets a plan:

| Reply | Comes from |
|---|---|
| `Session-Timeout` = seconds until `Expiration` | built-in `expiration` module |
| `Mikrotik-Total-Limit` = bytes left, low 32 bits | `data_quota` (`sqlcounter`) |
| `Mikrotik-Total-Limit-Gigawords` = bytes left / 4 GiB | `policy.d/hotspot.tmpl` |
| `Access-Reject` once time or data is used up | `expiration` / `data_quota` |

Plans (`radgroupreply`) hold only the speed limit and report interval. Never put
`Session-Timeout` or `Mikrotik-Total-Limit` in a plan: they restart at the full
value on every login.

### Two traps this setup avoids

- **Date strings in `Expiration`** (`Oct 30 2026 14:00:00`) are read in the
  *server's* local time zone. On a server not set to UTC, every plan silently
  ends hours early or late. Unix seconds have no time zone.
- **`sqlcounter` alone cuts a plan over 4 GiB to its low 32 bits.** A 10 GiB
  plan would be sent as 2 GiB. The policy adds the Gigawords attribute.

**After editing anything here:** `make radius-config && make radius-test`

Full guide: [docs/setup/01-wsl-freeradius.md](../docs/setup/01-wsl-freeradius.md)

## Routers (NAS clients) and restarts

`sql.tmpl` sets `read_clients = yes`, so routers registered in the dashboard (table `nas`) are
loaded from the database. FreeRADIUS reads that table **only at start** — a reload (HUP) does not
pick up new routers. `scripts/ops/radius-sync-clients.sh` (systemd timer, or `make radius-sync`)
restarts FreeRADIUS when the router list changes. Dev groups `dev-*` in `seed-dev.sql` are for
`radtest` only; real purchases get per-user rows from `session.Provision`.