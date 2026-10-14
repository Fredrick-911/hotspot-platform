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
make run             # start the API → http://localhost:8080/health
```

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
│   ├── httpapi/              HTTP routes and handlers (/health so far)
│   ├── tenant/               Box owners, subdomains, tenant middleware      [Phase 1]
│   ├── plan/                 Internet plans: price, time, data, speed        [Phase 1]
│   ├── device/               MikroTik routers: secrets, tunnels, status      [Phase 1]
│   ├── payment/              M-Pesa STK Push and callbacks                   [Phase 2]
│   ├── session/              Payment → RADIUS user → expiry                  [Phase 2]
│   └── radius/               Disconnect / CoA messages sent to routers       [Phase 2]
├── migrations/               Versioned database changes for our own tables
├── radius/                   FreeRADIUS config templates + dev seed data
│   ├── clients.conf.tmpl     Which routers may talk to FreeRADIUS
│   ├── mods-available/
│   │   └── sql.tmpl          Connects FreeRADIUS to PostgreSQL
│   └── sql/seed-dev.sql      Test plans and users
├── scripts/
│   └── wsl/                  Numbered setup scripts (01 → 04), run by `make radius-setup`
├── deploy/                   Dockerfile (later: docker-compose, Caddy, WireGuard)
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
| 1 | Core backend: tenants, subdomains, plans, router registration | ⚪ |
| 2 | Payment → online: M-Pesa, sessions, captive portal, auto-disconnect | ⚪ |
| 3 | Owner dashboard, monitoring, security hardening, payouts | ⚪ |
| 4 | Launch: load test, M-Pesa production, legal pages, soft launch | ⚪ |

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening your first pull request.
