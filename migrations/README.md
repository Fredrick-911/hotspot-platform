# Database migrations

Versioned changes to **our own** tables (tenants, plans, payments, sessions, users, devices...).

- Plain `.sql` files named `NNNNN_short_description.sql`, applied in name order, each exactly once.
- They are compiled into the API binary (`migrations/embed.go`) and applied when it starts
  (`AUTO_MIGRATE=true`, the default). Applied files are listed in the `schema_migrations` table and
  a database lock stops two API instances running the same file at once.
- Never edit a migration that is already on `main`. Add a new one. Write them so they are safe to run twice (`IF NOT EXISTS`).
- Every table we own gets a `tenant_id` column (except platform-level ones such as admin users).

FreeRADIUS's own tables (`radcheck`, `radacct`, ...) are **not** managed here. They come from
FreeRADIUS's official schema, loaded by `scripts/wsl/02-setup-database.sh`. Run that first: our
migrations grant rights to the `radius` user and add an index on `radacct`.