# Database migrations

Versioned changes to **our own** tables (tenants, plans, payments, devices...).
Starts in Phase 1, using [goose](https://github.com/pressly/goose).

File naming: `NNNN_short_description.sql`, e.g. `0001_create_tenants.sql`.

Rules:

- Never edit a migration that is already on `main`. Add a new one.
- Every table we own gets a `tenant_id` column.

FreeRADIUS's own tables (`radcheck`, `radacct`, ...) are **not** managed here.
They come from FreeRADIUS's official schema, loaded by
`scripts/wsl/02-setup-database.sh`.
