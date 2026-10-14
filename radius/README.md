# FreeRADIUS configuration

Templates for the files we change in `/etc/freeradius/3.0/`. Everything else
stays as Ubuntu installs it.

| Template | Installed as | Purpose |
|---|---|---|
| `clients.conf.tmpl` | `clients.conf` | Which routers may talk to FreeRADIUS, and their shared secrets |
| `mods-available/sql.tmpl` | `mods-available/sql` | Connects FreeRADIUS to PostgreSQL |
| `sql/seed-dev.sql` | rows in the database | Test plans and users (development only) |

`${RADIUS_DB_PASSWORD}`-style placeholders are filled from `.env`.
Placeholders like `${modconfdir}` belong to FreeRADIUS and are left alone.

**After editing anything here:** `make radius-config && make radius-test`

Full guide: [docs/setup/01-wsl-freeradius.md](../docs/setup/01-wsl-freeradius.md)
