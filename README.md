# Hotspot Platform

A multi-tenant platform that lets **box owners** sell WiFi access from their
Airtel 4G/5G Smart Box. End users connect to the owner's WiFi, pay with M-Pesa,
and get internet for a set time, data amount and speed.

```
Airtel Smart Box ──Ethernet──► MikroTik router ──WiFi──► user's phone
                                    │
                                    │ RADIUS (UDP 1812/1813) over WireGuard
                                    ▼
                    Our cloud: Go API + FreeRADIUS + PostgreSQL
```

See [docs/architecture.md](docs/architecture.md) for the full picture.

---

## Quick start (Windows + WSL)

```bash
git clone git@github.com:Fredrick-911/hotspot-platform.git ~/projects/hotspot-platform
cd ~/projects/hotspot-platform

make radius-setup    # install + configure + test FreeRADIUS and PostgreSQL
make check           # format, vet and test the Go code
make seed-demo       # demo businesses, plans and two weeks of sales (development only)
make run             # start the API → http://127.0.0.1:8080/login
make test-api        # whole flow on the real database: login → pay → pass → revoke
```

Then open <http://127.0.0.1:8080/login> and sign in with the demo accounts printed by `make seed-demo`.
Customers' page: <http://127.0.0.1:8080/?tenant=kamau>. Details: **[docs/setup/02-dashboards.md](docs/setup/02-dashboards.md)**.

New to any of this? Follow **[docs/setup/01-wsl-freeradius.md](docs/setup/01-wsl-freeradius.md)** step by step.

Run `make` to list every command.

---

## Repository layout

```
hotspot-platform/
├── cmd/
│   └── api/                  Program entry point (main.go) — starts the HTTP server
├── internal/                 All Go code. "internal" means other projects can't import it.
│   ├── config/               Reads settings from environment variables
│   ├── httpapi/              Web layer: customer portal, 3 dashboards, JSON APIs
│   │   ├── templates/        Pages (login, admin, tenant, account, portal)
│   │   └── static/           theme.css (sky-blue), core.js and one script per page
│   ├── auth/                 Passwords (PBKDF2), signed cookies, login rate limits
│   ├── dashboard/            Read models behind the dashboards (sales, customers, routers)
│   ├── events/               In-memory live updates (Server-Sent Events)
│   ├── tenant/               Box owners, subdomains, tenant middleware
│   ├── plan/                 Internet plans: price, time, data, speed
│   ├── device/               MikroTik routers: secrets, setup script, heartbeat
│   ├── payment/              M-Pesa STK Push (PayHero)
│   ├── session/              Payment → RADIUS user → expiry; revoke; Reaper
│   └── radius/               Disconnect-Request messages sent to routers
├── cmd/seed/                 Demo data for development (`make seed-demo`)
├── migrations/               Our own tables; embedded in the API and applied at startup
├── radius/                   FreeRADIUS config templates + dev seed data
│   ├── clients.conf.tmpl     Which routers may talk to FreeRADIUS
│   ├── mods-available/
│   │   └── sql.tmpl          Connects FreeRADIUS to PostgreSQL
│   └── sql/seed-dev.sql      Test plans and users
├── scripts/
│   ├── wsl/                  Numbered setup scripts (01 → 04), run by `make radius-setup`
│   └── ops/                  Server-side helpers (router-list sync for FreeRADIUS)
├── deploy/                   deploy.sh, systemd units, Dockerfile
├── docs/
│   ├── architecture.md       How the whole system fits together
│   ├── setup/                Step-by-step guides
│   └── adr/                  Architecture Decision Records — why we chose things
├── .github/
│   ├── workflows/ci.yml      Automatic checks on every push and pull request
│   ├── pull_request_template.md
│   ├── ISSUE_TEMPLATE/
│   └── CODEOWNERS            Who reviews which folders
├── .env.example              List of settings. Copied to .env (never committed)
├── Makefile                  Shortcuts for every common task
├── CONTRIBUTING.md           How we work together: branches, commits, reviews
└── go.mod                    Go module definition
```

### Why it is laid out this way

- **One folder per business feature** inside `internal/` (tenant, plan, payment...). Two people can work on `payment` and `plan` at the same time without touching the same files.
- **Config lives in the repo as templates**, never as hand-edited server files, so every machine (laptops, CI, production) is configured the same way.
- **Secrets live only in `.env`**, which Git ignores.
- **CI runs the exact same setup scripts developers run**, so "works on my machine" problems show up in the pull request.

---

## Roadmap

| Phase | Goal | Status |
|---|---|---|
| 0 | Foundations + hardware proof (FreeRADIUS, repo, CI, MikroTik test) | 🟡 In progress |
| 1 | Core backend: tenants, subdomains, plans, router registration | ✅ |
| 2 | Payment → online: M-Pesa, sessions, captive portal, auto-disconnect | ✅ (hardware test of Disconnect pending) |
| 3 | Admin, owner and customer dashboards, monitoring, security hardening | 🟡 dashboards done; payouts, wildcard HTTPS, backups next |
| 4 | Launch: load test, M-Pesa production, legal pages, soft launch | ⚪ |

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening your first pull request.