# Running the dashboards

## 1. One-time (development)
```bash
make radius-setup          # database + FreeRADIUS (skip if already done)
make seed-demo             # demo businesses, routers, plans and two weeks of sales
make run                   # http://127.0.0.1:8080
```
Sign in at <http://127.0.0.1:8080/login>:

| Role | Email | Password | Opens |
|---|---|---|---|
| Admin | `admin@demo.local` | `demo-password-123` | `/admin` |
| Owner | `jane@demo.local` | `demo-password-123` | `/dashboard` |

Customer page (no login): <http://127.0.0.1:8080/?tenant=kamau>. With no PayHero keys in `.env` and
`APP_ENV=development`, the payment dialog shows **Simulate payment (development only)**.

## 2. First admin on a real server
In `/opt/hotspot-platform/.env` set `APP_ENV=production`, `SESSION_SECRET` (`openssl rand -hex 32`),
`BOOTSTRAP_ADMIN_EMAIL`, `BOOTSTRAP_ADMIN_PASSWORD` (10+ characters), restart the API, sign in,
then **delete the password line** from `.env`. The admin then adds each business (**Businesses → Add a business**) and
gives the owner the one-time password shown.

## 3. Adding a router
Owner → **Routers → Register a router** → paste the generated script into WinBox → New Terminal.
Within a minute the router shows **Online**. FreeRADIUS learns the router's secret via the timer
`hotspot-radius-sync.timer` (installed by `deploy.sh`); to do it by hand: `make radius-sync`.

## 4. Tests
`make check` (fast, no database) and `make test-api` (full flow on the real database).